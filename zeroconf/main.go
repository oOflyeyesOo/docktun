package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

type dockerContainer struct {
	ID    string   `json:"Id"`
	Names []string `json:"Names"`
	State string   `json:"State"`
}

type mDNSManager struct {
	mu         sync.RWMutex
	containers map[string]bool
	hostIP     net.IP
	domain     string
}

func newMDNSManager(domain string) *mDNSManager {
	if domain == "" {
		domain = "local"
	}
	domain = strings.TrimPrefix(domain, ".")
	return &mDNSManager{
		containers: make(map[string]bool),
		hostIP:     getHostIP(),
		domain:     strings.ToLower(domain),
	}
}

func getHostIP() net.IP {
	addrs, err := net.InterfaceAddrs()
	if err == nil {
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
				if v4 := ipnet.IP.To4(); v4 != nil {
					return v4
				}
			}
		}
	}
	return net.ParseIP("127.0.0.1").To4()
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

	newMap := make(map[string]bool)
	for _, c := range list {
		for _, rawName := range c.Names {
			clean := strings.ToLower(strings.TrimPrefix(rawName, "/"))
			if clean != "" {
				newMap[clean] = true
			}
		}
	}

	m.mu.Lock()
	m.containers = newMap
	m.hostIP = getHostIP()
	m.mu.Unlock()
}

func (m *mDNSManager) isKnown(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.containers[strings.ToLower(name)]
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
		// Handle pointer compression
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

func buildResponse(queryID uint16, qname string, targetIP net.IP) []byte {
	// DNS Header: ID, Flags(0x8400), QDCOUNT(1), ANCOUNT(1), NSCOUNT(0), ARCOUNT(0)
	resp := make([]byte, 12)
	binary.BigEndian.PutUint16(resp[0:2], queryID)
	binary.BigEndian.PutUint16(resp[2:4], 0x8400) // Standard query response, Authoritative
	binary.BigEndian.PutUint16(resp[4:6], 1)      // QDCOUNT
	binary.BigEndian.PutUint16(resp[6:8], 1)      // ANCOUNT

	// Append Question Section
	qnameOffset := len(resp)
	for _, label := range strings.Split(qname, ".") {
		if len(label) == 0 {
			continue
		}
		resp = append(resp, byte(len(label)))
		resp = append(resp, []byte(label)...)
	}
	resp = append(resp, 0x00) // End of QNAME

	// QTYPE = 1 (A), QCLASS = 1 (IN)
	resp = append(resp, 0x00, 0x01, 0x00, 0x01)

	// Append Answer Section
	// Name: pointer to QNAME (0xC000 | qnameOffset)
	ptr := uint16(0xC000 | qnameOffset)
	resp = append(resp, byte(ptr>>8), byte(ptr&0xFF))

	// TYPE = 1 (A), CLASS = 0x8001 (IN, Flush Cache)
	resp = append(resp, 0x00, 0x01, 0x80, 0x01)

	// TTL = 120 seconds
	ttlBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(ttlBytes, 120)
	resp = append(resp, ttlBytes...)

	// RDLENGTH = 4
	resp = append(resp, 0x00, 0x04)

	// RDATA = IPv4 (4 bytes)
	resp = append(resp, targetIP.To4()...)

	return resp
}

func main() {
	domain := os.Getenv("DOMAIN")
	if domain == "" {
		domain = "local"
	}
	mgr := newMDNSManager(domain)
	client := dockerClient()

	log.Printf("[docktun-zeroconf] Starting local mDNS responder for *.%s", domain)
	mgr.refreshContainers(client)
	log.Printf("[docktun-zeroconf] Host IP: %s | Tracking %d initial container(s)", mgr.hostIP, len(mgr.containers))

	// Background ticker to refresh containers from Docker
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			mgr.refreshContainers(client)
		}
	}()

	// Open mDNS UDP listener on 224.0.0.251:5353
	mcastAddr := &net.UDPAddr{
		IP:   net.ParseIP("224.0.0.251"),
		Port: 5353,
	}

	conn, err := net.ListenMulticastUDP("udp4", nil, mcastAddr)
	if err != nil {
		log.Fatalf("[docktun-zeroconf] Failed to bind mDNS multicast socket: %v", err)
	}
	defer conn.Close()

	// Ensure socket buffer is large enough
	_ = conn.SetReadBuffer(65536)

	buf := make([]byte, 1500)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-stop
		log.Println("[docktun-zeroconf] Shutting down mDNS responder...")
		conn.Close()
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

		// Check if it's a query: QR bit in Flags (byte 2 & 0x80 == 0)
		flags := binary.BigEndian.Uint16(buf[2:4])
		if flags&0x8000 != 0 {
			// This is a response, ignore
			continue
		}

		qdCount := binary.BigEndian.Uint16(buf[4:6])
		if qdCount == 0 {
			continue
		}

		queryID := binary.BigEndian.Uint16(buf[0:2])
		qname, qEnd, err := parseQName(buf, 12)
		if err != nil || qEnd+4 > n {
			continue
		}

		qType := binary.BigEndian.Uint16(buf[qEnd : qEnd+2])
		// Check for A query (1) or ANY (255)
		if qType != 1 && qType != 255 {
			continue
		}

		qnameLower := strings.ToLower(qname)
		expectedSuffix := "." + mgr.domain
		if !strings.HasSuffix(qnameLower, expectedSuffix) {
			continue
		}

		cName := strings.TrimSuffix(qnameLower, expectedSuffix)
		if mgr.isKnown(cName) {
			resp := buildResponse(queryID, qname, mgr.hostIP)
			// Send response to multicast or unicast sender
			_, _ = conn.WriteToUDP(resp, mcastAddr)
			if src != nil {
				_, _ = conn.WriteToUDP(resp, src)
			}
		}
	}
}
