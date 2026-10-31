package main

import (
	"encoding/binary"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestProxyKeepsLocalHostnameForApplicationRedirects(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://"+r.Host+"/login?next=home", http.StatusFound)
	}))
	defer origin.Close()
	u, _ := url.Parse(origin.URL)
	port, _ := strconv.Atoi(u.Port())
	for _, host := range []string{"web.local", "docktun.local"} {
		t.Run(host, func(t *testing.T) {
			mgr := newMDNSManager("local", u.Port())
			mgr.routes["web"] = &containerRoute{Name: "web", IP: "127.0.0.1", Port: port}
			request := httptest.NewRequest(http.MethodGet, "http://"+host+"/", nil)
			response := httptest.NewRecorder()
			mgr.ServeHTTP(response, request)
			if got, want := response.Header().Get("Location"), "http://"+host+"/login?next=home"; got != want {
				t.Fatalf("redirect leaked upstream host/port: got %q, want %q", got, want)
			}
		})
	}
}

func TestBuildResponseRFC6762(t *testing.T) {
	targetIP := net.ParseIP("192.168.1.166").To4()

	// 1. Multicast response (ID MUST be 0, QDCOUNT MUST be 0)
	respMcast := buildResponse(12345, "docktun.local", targetIP, false)
	if len(respMcast) < 12 {
		t.Fatalf("response too short: %d bytes", len(respMcast))
	}
	idMcast := binary.BigEndian.Uint16(respMcast[0:2])
	if idMcast != 0 {
		t.Errorf("multicast response ID must be 0, got %d", idMcast)
	}
	flags := binary.BigEndian.Uint16(respMcast[2:4])
	if flags != 0x8400 {
		t.Errorf("expected flags 0x8400, got 0x%04X", flags)
	}
	qdCount := binary.BigEndian.Uint16(respMcast[4:6])
	if qdCount != 0 {
		t.Errorf("RFC 6762 violation: QDCOUNT must be 0 in mDNS response, got %d", qdCount)
	}
	anCount := binary.BigEndian.Uint16(respMcast[6:8])
	if anCount != 1 {
		t.Errorf("expected ANCOUNT = 1, got %d", anCount)
	}

	// Verify Answer section
	name, qEnd, err := parseQName(respMcast, 12)
	if err != nil {
		t.Fatalf("failed to parse answer name: %v", err)
	}
	if !strings.EqualFold(name, "docktun.local") {
		t.Errorf("expected answer name docktun.local, got %s", name)
	}
	ansType := binary.BigEndian.Uint16(respMcast[qEnd : qEnd+2])
	if ansType != 1 {
		t.Errorf("expected A record type 1, got %d", ansType)
	}
	ansClass := binary.BigEndian.Uint16(respMcast[qEnd+2 : qEnd+4])
	if ansClass != 0x8001 {
		t.Errorf("expected class 0x8001 (IN + Cache-Flush), got 0x%04X", ansClass)
	}
	ttl := binary.BigEndian.Uint32(respMcast[qEnd+4 : qEnd+8])
	if ttl != 120 {
		t.Errorf("expected TTL 120, got %d", ttl)
	}
	rdLen := binary.BigEndian.Uint16(respMcast[qEnd+8 : qEnd+10])
	if rdLen != 4 {
		t.Errorf("expected RDLENGTH 4, got %d", rdLen)
	}
	rdData := respMcast[qEnd+10 : qEnd+14]
	if !net.IP(rdData).Equal(targetIP) {
		t.Errorf("expected IP %s, got %s", targetIP, net.IP(rdData))
	}

	// 2. Unicast response (ID echoed, QDCOUNT MUST be 0)
	respUnicast := buildResponse(54321, "my-app.local", targetIP, true)
	idUnicast := binary.BigEndian.Uint16(respUnicast[0:2])
	if idUnicast != 54321 {
		t.Errorf("unicast response should echo queryID 54321, got %d", idUnicast)
	}
	qdCountUni := binary.BigEndian.Uint16(respUnicast[4:6])
	if qdCountUni != 0 {
		t.Errorf("unicast response QDCOUNT must be 0, got %d", qdCountUni)
	}
}

func TestParseQName(t *testing.T) {
	// Construct DNS label for "web.local"
	var buf []byte
	buf = append(buf, 0x03)
	buf = append(buf, []byte("web")...)
	buf = append(buf, 0x05)
	buf = append(buf, []byte("local")...)
	buf = append(buf, 0x00)

	name, offset, err := parseQName(buf, 0)
	if err != nil {
		t.Fatalf("parseQName failed: %v", err)
	}
	if name != "web.local" {
		t.Errorf("expected web.local, got %s", name)
	}
	if offset != len(buf) {
		t.Errorf("expected offset %d, got %d", len(buf), offset)
	}
}

func TestIsKnown(t *testing.T) {
	mgr := newMDNSManager("local", "8080")
	mgr.containers["my_service"] = true
	mgr.containers["nginx-web"] = true

	// DockTun dashboard always known
	if !mgr.isKnown("docktun") {
		t.Errorf("docktun should always be known")
	}
	if !mgr.isKnown("DOCKTUN") {
		t.Errorf("docktun should be case-insensitive")
	}

	// Exact match
	if !mgr.isKnown("my_service") {
		t.Errorf("my_service should be known")
	}

	// Dash/underscore interchangeability
	if !mgr.isKnown("my-service") {
		t.Errorf("my-service should match my_service")
	}
	if !mgr.isKnown("nginx_web") {
		t.Errorf("nginx_web should match nginx-web")
	}

	// Unknown
	if mgr.isKnown("nonexistent") {
		t.Errorf("nonexistent should not be known")
	}
}

func TestTargetURL(t *testing.T) {
	crPub := &containerRoute{Name: "app", IP: "172.18.0.5", Port: 80, PublicPort: 3000}
	if crPub.TargetURL() != "http://127.0.0.1:3000" {
		t.Errorf("expected http://127.0.0.1:3000, got %s", crPub.TargetURL())
	}

	crPriv := &containerRoute{Name: "app", IP: "172.18.0.5", Port: 8080, PublicPort: 0}
	if crPriv.TargetURL() != "http://172.18.0.5:8080" {
		t.Errorf("expected http://172.18.0.5:8080, got %s", crPriv.TargetURL())
	}
}

func TestPublishedBindingAndIPv6Targets(t *testing.T) {
	for _, item := range []struct{ ip, want string }{
		{"192.168.1.20", "http://192.168.1.20:8080"},
		{"127.0.0.2", "http://127.0.0.2:8080"},
		{"0.0.0.0", "http://127.0.0.1:8080"},
		{"::", "http://[::1]:8080"},
		{"2001:db8::2", "http://[2001:db8::2]:8080"},
	} {
		got := (&containerRoute{PublicIP: item.ip, PublicPort: 8080}).TargetURL()
		if got != item.want {
			t.Errorf("binding %s: %s, want %s", item.ip, got, item.want)
		}
	}
	if got := (&containerRoute{IP: "fd00::2", Port: 80}).TargetURL(); got != "http://[fd00::2]:80" {
		t.Fatal(got)
	}
}

func TestProxyReachesSpecificallyBoundPublishedPort(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(r.Host))
	}))
	origin.Listener.Close()
	origin.Listener = listener
	origin.Start()
	defer origin.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	mgr := newMDNSManager("local", "8080")
	mgr.routes["web"] = &containerRoute{Name: "web", PublicIP: "127.0.0.1", PublicPort: port}
	response := httptest.NewRecorder()
	mgr.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://web.local/", nil))
	if response.Code != 200 || response.Body.String() != "web.local" {
		t.Fatalf("bound origin unavailable: %d %s", response.Code, response.Body.String())
	}
}

func TestGetHostIP(t *testing.T) {
	ip := getHostIP()
	if ip == nil {
		t.Fatalf("getHostIP returned nil")
	}
	if v4 := ip.To4(); v4 == nil {
		t.Fatalf("getHostIP did not return IPv4")
	}
	if isDockerBridgeIP(ip) {
		t.Errorf("getHostIP must not return Docker bridge IP, got %s", ip)
	}
}
