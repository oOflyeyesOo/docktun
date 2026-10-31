package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type dockerPort struct {
	IP          string `json:"IP"`
	PrivatePort int    `json:"PrivatePort"`
	PublicPort  int    `json:"PublicPort"`
	Type        string `json:"Type"`
}

type dockerContainer struct {
	Labels          map[string]string `json:"Labels"`
	ID              string            `json:"Id"`
	Names           []string          `json:"Names"`
	State           string            `json:"State"`
	Ports           []dockerPort      `json:"Ports"`
	NetworkSettings struct {
		Networks map[string]struct {
			IPAddress string `json:"IPAddress"`
		} `json:"Networks"`
	} `json:"NetworkSettings"`
}

type containerRoute struct {
	Name       string
	IP         string
	Port       int
	PublicPort int
	PublicIP   string
}

func (cr *containerRoute) TargetURL() string {
	if cr.PublicPort > 0 {
		host := cr.PublicIP
		if host == "" || host == "0.0.0.0" {
			host = "127.0.0.1"
		}
		if host == "::" {
			host = "::1"
		}
		return "http://" + net.JoinHostPort(host, strconv.Itoa(cr.PublicPort))
	}
	if cr.IP != "" && cr.Port > 0 {
		return "http://" + net.JoinHostPort(cr.IP, strconv.Itoa(cr.Port))
	}
	if cr.Port > 0 {
		return fmt.Sprintf("http://127.0.0.1:%d", cr.Port)
	}
	return ""
}

type mDNSManager struct {
	mu          sync.RWMutex
	routes      map[string]*containerRoute
	containers  map[string]bool
	hostIP      net.IP
	domain      string
	docktunPort string
}

func newMDNSManager(domain, docktunPort string) *mDNSManager {
	if domain == "" {
		domain = "local"
	}
	if docktunPort == "" {
		docktunPort = "8080"
	}
	domain = strings.TrimPrefix(domain, ".")
	return &mDNSManager{
		routes:      make(map[string]*containerRoute),
		containers:  make(map[string]bool),
		hostIP:      getHostIP(),
		domain:      strings.ToLower(domain),
		docktunPort: docktunPort,
	}
}

func isVirtualInterface(name string) bool {
	prefixes := []string{"docker", "br-", "veth", "tailscale", "tun", "wg", "dummy", "virbr", "lo"}
	for _, p := range prefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

func isDockerBridgeIP(ip net.IP) bool {
	v4 := ip.To4()
	if v4 == nil {
		return false
	}
	return v4[0] == 172 && (v4[1] >= 17 && v4[1] <= 31) && v4[3] == 1
}

func isPrivateRFC1918(ip net.IP) bool {
	v4 := ip.To4()
	if v4 == nil {
		return false
	}
	if v4[0] == 10 {
		return true
	}
	if v4[0] == 172 && v4[1] >= 16 && v4[1] <= 31 {
		return true
	}
	if v4[0] == 192 && v4[1] == 168 {
		return true
	}
	return false
}

func getHostIP() net.IP {
	if envIP := strings.TrimSpace(os.Getenv("HOST_IP")); envIP != "" {
		if ip := net.ParseIP(envIP); ip != nil {
			if v4 := ip.To4(); v4 != nil {
				return v4
			}
		}
	}

	conn, err := net.Dial("udp", "1.1.1.1:80")
	if err == nil {
		defer conn.Close()
		localAddr, ok := conn.LocalAddr().(*net.UDPAddr)
		if ok {
			if v4 := localAddr.IP.To4(); v4 != nil && !v4.IsLoopback() && !isDockerBridgeIP(v4) {
				return v4
			}
		}
	}

	interfaces, err := net.Interfaces()
	if err == nil {
		var candidate net.IP
		for _, ifi := range interfaces {
			if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagLoopback != 0 {
				continue
			}
			if isVirtualInterface(strings.ToLower(ifi.Name)) {
				continue
			}

			addrs, err := ifi.Addrs()
			if err != nil {
				continue
			}
			for _, addr := range addrs {
				ipnet, ok := addr.(*net.IPNet)
				if !ok || ipnet.IP.IsLoopback() {
					continue
				}
				v4 := ipnet.IP.To4()
				if v4 == nil || isDockerBridgeIP(v4) {
					continue
				}
				if isPrivateRFC1918(v4) {
					return v4
				}
				if candidate == nil {
					candidate = v4
				}
			}
		}
		if candidate != nil {
			return candidate
		}
	}

	return net.ParseIP("127.0.0.1").To4()
}

func getLANInterface(hostIP net.IP) *net.Interface {
	if hostIP == nil || hostIP.IsLoopback() {
		return nil
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, ifi := range interfaces {
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok {
				if ipnet.IP.Equal(hostIP) {
					return &ifi
				}
			}
		}
	}
	return nil
}

func dockerClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", "/var/run/docker.sock")
			},
		},
		Timeout: 5 * time.Second,
	}
}

func pickBestPort(ports []dockerPort) (int, int) {
	if len(ports) == 0 {
		return 80, 0
	}

	// 1. Strongly prefer published Host Ports (PublicPort > 0) so host networking routes directly to 127.0.0.1:PublicPort
	commonPorts := []int{80, 8080, 3000, 3001, 5000, 8000, 8888, 9000, 443}
	for _, cp := range commonPorts {
		for _, p := range ports {
			if p.PublicPort > 0 && p.PrivatePort == cp && (p.Type == "tcp" || p.Type == "") {
				return p.PrivatePort, p.PublicPort
			}
		}
	}
	for _, p := range ports {
		if p.PublicPort > 0 && (p.Type == "tcp" || p.Type == "") {
			return p.PrivatePort, p.PublicPort
		}
	}

	// 2. Fallback to private ports if no published host port
	for _, cp := range commonPorts {
		for _, p := range ports {
			if p.PrivatePort == cp && (p.Type == "tcp" || p.Type == "") {
				return p.PrivatePort, p.PublicPort
			}
		}
	}

	for _, p := range ports {
		if p.Type == "tcp" || p.Type == "" {
			return p.PrivatePort, p.PublicPort
		}
	}

	return ports[0].PrivatePort, ports[0].PublicPort
}

func (m *mDNSManager) refreshContainers(client *http.Client) {
	resp, err := client.Get("http://localhost/containers/json")
	if err != nil {
		log.Printf("[zeroconf] Docker socket read error: %v", err)
		return
	}
	defer resp.Body.Close()

	var list []dockerContainer
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return
	}

	newContainers := make(map[string]bool)
	newRoutes := make(map[string]*containerRoute)

	for _, c := range list {
		if c.Labels["docktun.storage-check"] != "" || c.Labels["docktun.update.target"] != "" {
			continue
		}
		var internalIP string
		// Prefer docktun or cloudflared bridge network IP if available
		if netw, ok := c.NetworkSettings.Networks["docktun"]; ok && netw.IPAddress != "" {
			internalIP = netw.IPAddress
		} else if netw, ok := c.NetworkSettings.Networks["cloudflared"]; ok && netw.IPAddress != "" {
			internalIP = netw.IPAddress
		} else {
			for _, netw := range c.NetworkSettings.Networks {
				if netw.IPAddress != "" {
					internalIP = netw.IPAddress
					break
				}
			}
		}

		privPort, pubPort := pickBestPort(c.Ports)
		publicIP := ""
		for _, binding := range c.Ports {
			if binding.PrivatePort == privPort && binding.PublicPort == pubPort && (binding.Type == "tcp" || binding.Type == "") {
				publicIP = binding.IP
				break
			}
		}

		for _, rawName := range c.Names {
			clean := strings.ToLower(strings.TrimPrefix(rawName, "/"))
			if clean != "" {
				newContainers[clean] = true
				newRoutes[clean] = &containerRoute{
					Name:       clean,
					IP:         internalIP,
					Port:       privPort,
					PublicPort: pubPort,
					PublicIP:   publicIP,
				}
				dashName := strings.ReplaceAll(clean, "_", "-")
				if dashName != clean {
					newContainers[dashName] = true
					newRoutes[dashName] = newRoutes[clean]
				}
			}
		}
	}

	m.mu.Lock()
	m.containers = newContainers
	m.routes = newRoutes
	m.hostIP = getHostIP()
	m.mu.Unlock()
}

func (m *mDNSManager) isKnown(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	clean := strings.ToLower(strings.TrimSpace(name))
	if clean == "docktun" {
		return true
	}
	if m.containers[clean] {
		return true
	}
	dash := strings.ReplaceAll(clean, "_", "-")
	under := strings.ReplaceAll(clean, "-", "_")
	return m.containers[dash] || m.containers[under]
}

func (m *mDNSManager) getRoute(name string) (*containerRoute, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	clean := strings.ToLower(strings.TrimSpace(name))
	if r, ok := m.routes[clean]; ok {
		return r, true
	}
	if r, ok := m.routes[strings.ReplaceAll(clean, "_", "-")]; ok {
		return r, true
	}
	if r, ok := m.routes[strings.ReplaceAll(clean, "-", "_")]; ok {
		return r, true
	}
	return nil, false
}

func (m *mDNSManager) listAllRoutes() []*containerRoute {
	m.mu.RLock()
	defer m.mu.RUnlock()
	list := make([]*containerRoute, 0, len(m.routes))
	for _, r := range m.routes {
		list = append(list, r)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].Name < list[j].Name
	})
	return list
}

func newReverseProxy(targetURL *url.URL) *httputil.ReverseProxy {
	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	proxy.Transport = transport
	return proxy
}

// ServeHTTP acts as the HTTP reverse proxy for <name>.local requests on port 80.
func (m *mDNSManager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(host)

	// Determine destination container name
	var targetName string
	domainSuffix := "." + m.domain
	if strings.HasSuffix(host, domainSuffix) {
		targetName = strings.TrimSuffix(host, domainSuffix)
	} else if host == "localhost" || host == m.hostIP.String() || host == "docktun" {
		targetName = "docktun"
	}

	// 1. Route to DockTun Dashboard
	if targetName == "docktun" || targetName == "" {
		docktunURL, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%s", m.docktunPort))
		proxy := newReverseProxy(docktunURL)
		origDirector := proxy.Director
		proxy.Director = func(req *http.Request) {
			origDirector(req)
			// Preserve the public .local Host so application redirects stay portless.
			req.Header.Set("X-Forwarded-Host", r.Host)
			req.Header.Set("X-Forwarded-Proto", "http")
			if clientIP, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
				req.Header.Set("X-Forwarded-For", clientIP)
				req.Header.Set("X-Real-IP", clientIP)
			}
		}
		proxy.ServeHTTP(w, r)
		return
	}

	// 2. Route to matching Docker Container
	route, ok := m.getRoute(targetName)
	if ok {
		target := route.TargetURL()
		if target != "" {
			targetURL, err := url.Parse(target)
			if err == nil {
				proxy := newReverseProxy(targetURL)
				origDirector := proxy.Director
				proxy.Director = func(req *http.Request) {
					origDirector(req)
					// Preserve the public .local Host so application redirects stay portless.
					req.Header.Set("X-Forwarded-Host", r.Host)
					req.Header.Set("X-Forwarded-Proto", "http")
					if clientIP, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
						req.Header.Set("X-Forwarded-For", clientIP)
						req.Header.Set("X-Real-IP", clientIP)
					}
				}
				proxy.ServeHTTP(w, r)
				return
			}
		}
	}

	// 3. Fallback: Friendly DockTun Directory 404
	w.WriteHeader(http.StatusNotFound)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	routes := m.listAllRoutes()
	var links strings.Builder
	for _, rt := range routes {
		links.WriteString(fmt.Sprintf(
			`<li><a href="http://%s.%s" style="color:#06b6d4;text-decoration:none;font-weight:600;font-family:monospace;">%s.%s</a> &mdash; <span style="color:#94a3b8;font-size:12px;">Port: %d</span></li>`,
			rt.Name, m.domain, rt.Name, m.domain, rt.Port,
		))
	}

	fmt.Fprintf(w, `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>DockTun Local Gateway &mdash; Service Not Found</title>
  <style>
    body { background-color: #020617; color: #f8fafc; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; display: flex; align-items: center; justify-content: center; min-height: 100vh; margin: 0; padding: 20px; box-sizing: border-box; }
    .card { background-color: #0f172a; border: 1px solid #1e293b; border-radius: 16px; max-width: 560px; width: 100%%; padding: 32px; box-shadow: 0 25px 50px -12px rgba(0,0,0,0.5); }
    h1 { color: #f1f5f9; font-size: 20px; margin: 0 0 8px 0; }
    p { color: #94a3b8; font-size: 14px; margin: 0 0 24px 0; line-height: 1.5; }
    ul { list-style: none; padding: 0; margin: 0; display: flex; flex-direction: column; gap: 8px; }
    li { background: #1e293b; padding: 10px 14px; border-radius: 8px; display: flex; justify-content: space-between; align-items: center; }
    .badge { background: #083344; color: #22d3ee; border: 1px solid #0e7490; border-radius: 6px; padding: 2px 8px; font-size: 11px; font-weight: 600; text-transform: uppercase; }
    .footer { margin-top: 24px; padding-top: 16px; border-top: 1px solid #1e293b; display: flex; justify-content: space-between; align-items: center; font-size: 12px; color: #64748b; }
    .footer a { color: #22d3ee; text-decoration: none; }
  </style>
</head>
<body>
  <div class="card">
    <div style="display:flex;align-items:center;gap:8px;margin-bottom:12px;">
      <span class="badge">DockTun Local Gateway</span>
    </div>
    <h1>Service Not Found</h1>
    <p>No active Docker container matches <code style="color:#f43f5e;background:#881337;padding:2px 6px;border-radius:4px;">%s</code>.</p>
    <div style="font-size:12px;font-weight:600;text-transform:uppercase;letter-spacing:0.1em;color:#64748b;margin-bottom:8px;">Available Local Services:</div>
    <ul>
      <li><a href="http://docktun.%s" style="color:#06b6d4;text-decoration:none;font-weight:600;font-family:monospace;">docktun.%s</a> &mdash; <span style="color:#94a3b8;font-size:12px;">DockTun Dashboard</span></li>
      %s
    </ul>
    <div class="footer">
      <span>Powered by DockTun</span>
      <a href="http://docktun.%s">Open Dashboard &rarr;</a>
    </div>
  </div>
</body>
</html>`, targetName, m.domain, m.domain, links.String(), m.domain)
}

func parseQName(buf []byte, offset int) (string, int, error) {
	var parts []string
	idx := offset
	for {
		if idx >= len(buf) {
			return "", idx, fmt.Errorf("buffer overflow reading qname")
		}
		length := int(buf[idx])
		if length == 0 {
			idx++
			break
		}
		if length&0xC0 == 0xC0 {
			if idx+1 >= len(buf) {
				return "", idx, fmt.Errorf("truncated pointer")
			}
			ptr := int(binary.BigEndian.Uint16(buf[idx:idx+2]) & 0x3FFF)
			pName, _, err := parseQName(buf, ptr)
			if err != nil {
				return "", idx + 2, err
			}
			parts = append(parts, pName)
			idx += 2
			break
		}
		idx++
		if idx+length > len(buf) {
			return "", idx, fmt.Errorf("label exceeds packet length")
		}
		parts = append(parts, string(buf[idx:idx+length]))
		idx += length
	}
	return strings.Join(parts, "."), idx, nil
}

func buildResponse(queryID uint16, qname string, targetIP net.IP, isUnicast bool) []byte {
	resp := make([]byte, 12)
	if isUnicast {
		binary.BigEndian.PutUint16(resp[0:2], queryID)
	} else {
		binary.BigEndian.PutUint16(resp[0:2], 0)
	}
	binary.BigEndian.PutUint16(resp[2:4], 0x8400) // Standard query response, Authoritative
	binary.BigEndian.PutUint16(resp[4:6], 0)      // QDCOUNT MUST be 0 in mDNS responses (RFC 6762 Section 6)
	binary.BigEndian.PutUint16(resp[6:8], 1)      // ANCOUNT = 1

	// Answer Section
	for _, label := range strings.Split(strings.Trim(qname, "."), ".") {
		if len(label) == 0 {
			continue
		}
		resp = append(resp, byte(len(label)))
		resp = append(resp, []byte(label)...)
	}
	resp = append(resp, 0x00)

	// TYPE = 1 (A), CLASS = 0x8001 (IN, Flush Cache)
	resp = append(resp, 0x00, 0x01, 0x80, 0x01)

	// TTL = 120s
	ttlBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(ttlBytes, 120)
	resp = append(resp, ttlBytes...)

	// RDLENGTH = 4
	resp = append(resp, 0x00, 0x04)

	// RDATA = IPv4
	resp = append(resp, targetIP.To4()...)

	return resp
}

func main() {
	domain := os.Getenv("DOMAIN")
	if domain == "" {
		domain = "local"
	}
	httpPort := os.Getenv("HTTP_PORT")
	if httpPort == "" {
		httpPort = "80"
	}
	docktunPort := os.Getenv("DOCKTUN_PORT")
	if docktunPort == "" {
		docktunPort = "8080"
	}

	mgr := newMDNSManager(domain, docktunPort)
	client := dockerClient()

	log.Printf("[docktun-zeroconf] Starting local mDNS responder for *.%s", domain)
	mgr.refreshContainers(client)
	log.Printf("[docktun-zeroconf] Host IP: %s | Discovered %d initial container(s)", mgr.hostIP, len(mgr.containers))

	// Background ticker to refresh containers from Docker
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			mgr.refreshContainers(client)
		}
	}()

	// Start Portless HTTP Reverse Proxy on port 80 (or HTTP_PORT)
	httpServer := &http.Server{
		Addr:    ":" + httpPort,
		Handler: mgr,
	}
	go func() {
		log.Printf("[docktun-zeroconf] Starting portless HTTP reverse proxy on :%s", httpPort)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("[docktun-zeroconf] Notice: Could not bind HTTP reverse proxy to port :%s (%v). mDNS continues to resolve .local.", httpPort, err)
		}
	}()

	// Open mDNS UDP listener on 224.0.0.251:5353
	mcastAddr := &net.UDPAddr{
		IP:   net.ParseIP("224.0.0.251"),
		Port: 5353,
	}

	lanIf := getLANInterface(mgr.hostIP)
	conn, err := net.ListenMulticastUDP("udp4", lanIf, mcastAddr)
	if err != nil {
		log.Printf("[docktun-zeroconf] Notice: could not bind multicast on LAN interface %v: %v. Falling back to default...", lanIf, err)
		conn, err = net.ListenMulticastUDP("udp4", nil, mcastAddr)
		if err != nil {
			log.Fatalf("[docktun-zeroconf] Failed to bind mDNS multicast socket: %v", err)
		}
	}
	defer conn.Close()

	_ = conn.SetReadBuffer(65536)

	buf := make([]byte, 1500)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-stop
		log.Println("[docktun-zeroconf] Shutting down mDNS responder & HTTP gateway...")
		_ = httpServer.Close()
		_ = conn.Close()
		os.Exit(0)
	}()

	for {
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if n < 12 {
			continue
		}

		flags := binary.BigEndian.Uint16(buf[2:4])
		if flags&0x8000 != 0 {
			continue
		}

		qdCount := binary.BigEndian.Uint16(buf[4:6])
		if qdCount == 0 {
			continue
		}

		queryID := binary.BigEndian.Uint16(buf[0:2])
		offset := 12
		for q := 0; q < int(qdCount); q++ {
			if offset >= n {
				break
			}
			qname, qEnd, err := parseQName(buf, offset)
			if err != nil || qEnd+4 > n {
				break
			}
			qType := binary.BigEndian.Uint16(buf[qEnd : qEnd+2])
			offset = qEnd + 4

			if qType != 1 && qType != 255 {
				continue
			}

			qnameClean := strings.Trim(strings.ToLower(qname), ".")
			expectedSuffix := "." + mgr.domain
			if !strings.HasSuffix(qnameClean, expectedSuffix) {
				continue
			}

			cName := strings.TrimSuffix(qnameClean, expectedSuffix)
			if mgr.isKnown(cName) {
				mcastResp := buildResponse(queryID, qnameClean, mgr.hostIP, false)
				_, _ = conn.WriteToUDP(mcastResp, mcastAddr)
				if src != nil {
					unicastResp := buildResponse(queryID, qnameClean, mgr.hostIP, true)
					_, _ = conn.WriteToUDP(unicastResp, src)
				}
				break
			}
		}
	}
}
