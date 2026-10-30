package templateprobe

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

type Use struct {
	IP       string `json:"ip"`
	Protocol string `json:"protocol"`
	Port     int    `json:"port"`
}
type Group struct {
	ID        string `json:"id"`
	Preferred int    `json:"preferred"`
	Uses      []Use  `json:"uses"`
}
type Request struct {
	Groups []Group `json:"groups"`
	Used   []Use   `json:"used"`
}
type Result struct {
	Ports  map[string]string `json:"ports"`
	Errors []string          `json:"errors"`
}

func Overlaps(a, b string) bool {
	return a == "" || b == "" || a == "0.0.0.0" || b == "0.0.0.0" || a == "::" || b == "::" || a == b
}

// Sockets are bound without accepting connections or serving any data. Keep
// selected bindings until all groups are checked, then release every socket.
func FindPorts(request Request) Result {
	result := Result{Ports: map[string]string{}, Errors: []string{}}
	if len(request.Groups) > 64 || len(request.Used) > 10000 {
		result.Errors = append(result.Errors, "too many port bindings")
		return result
	}
	closers := []func(){}
	defer func() {
		for _, close := range closers {
			close()
		}
	}()
	used := append([]Use{}, request.Used...)
	for _, group := range request.Groups {
		if len(group.Uses) == 0 || len(group.Uses) > 16 {
			result.Errors = append(result.Errors, "invalid port group")
			continue
		}
		start := group.Preferred
		if start < 1 || start > 65535 {
			start = 8080
		}
		found := false
		for attempt := 0; attempt < 256; attempt++ {
			port := start + attempt
			if port > 65535 {
				port = 1024 + (port - 65536)
			}
			candidate := []func(){}
			valid := true
			for _, use := range group.Uses {
				if use.Protocol != "tcp" && use.Protocol != "udp" {
					valid = false
					break
				}
				if use.IP != "" && net.ParseIP(use.IP) == nil {
					valid = false
					break
				}
				for _, other := range used {
					if other.Port == port && other.Protocol == use.Protocol && Overlaps(other.IP, use.IP) {
						valid = false
						break
					}
				}
				if !valid {
					break
				}
				addresses := []string{use.IP}
				if use.IP == "" {
					addresses = []string{"0.0.0.0", "::"}
				}
				for _, ip := range addresses {
					family := "4"
					if strings.Contains(ip, ":") {
						family = "6"
					}
					address := net.JoinHostPort(ip, strconv.Itoa(port))
					var close func()
					var err error
					if use.Protocol == "tcp" {
						var listener net.Listener
						listener, err = net.Listen("tcp"+family, address)
						if err == nil {
							close = func() { listener.Close() }
						}
					} else {
						var listener net.PacketConn
						listener, err = net.ListenPacket("udp"+family, address)
						if err == nil {
							close = func() { listener.Close() }
						}
					}
					if err != nil {
						if family == "6" && use.IP == "" && (strings.Contains(err.Error(), "address family not supported") || strings.Contains(err.Error(), "cannot assign requested address")) {
							continue
						}
						valid = false
						break
					}
					candidate = append(candidate, close)
				}
				if !valid {
					break
				}
			}
			if valid {
				closers = append(closers, candidate...)
				result.Ports[group.ID] = strconv.Itoa(port)
				for _, use := range group.Uses {
					use.Port = port
					used = append(used, use)
				}
				found = true
				break
			}
			for _, close := range candidate {
				close()
			}
		}
		if !found {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: no free port found near %d", group.ID, start))
		}
	}
	return result
}
