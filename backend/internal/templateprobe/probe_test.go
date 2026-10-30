package templateprobe

import (
	"net"
	"strconv"
	"testing"
)

func TestFindPortsDetectsListenersAndReleasesReservations(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	result := FindPorts(Request{Groups: []Group{{ID: "app", Preferred: port, Uses: []Use{{IP: "127.0.0.1", Protocol: "tcp"}}}, {ID: "other", Preferred: port, Uses: []Use{{IP: "127.0.0.1", Protocol: "tcp"}}}}})
	if len(result.Errors) > 0 || result.Ports["app"] == strconv.Itoa(port) || result.Ports["app"] == result.Ports["other"] {
		t.Fatalf("occupied/duplicate port selected: %+v", result)
	}
	released, err := net.Listen("tcp4", "127.0.0.1:"+result.Ports["app"])
	if err != nil {
		t.Fatal("probe leaked a socket", err)
	}
	released.Close()
}

func TestFindPortsKeepsTCPAndUDPOnOnePort(t *testing.T) {
	result := FindPorts(Request{Groups: []Group{{ID: "dns", Preferred: 18530, Uses: []Use{{IP: "127.0.0.1", Protocol: "tcp"}, {IP: "127.0.0.1", Protocol: "udp"}}}}, Used: []Use{{IP: "0.0.0.0", Protocol: "tcp", Port: 18530}}})
	if len(result.Errors) > 0 || result.Ports["dns"] == "18530" {
		t.Fatal(result)
	}
}
