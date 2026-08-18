package pluginsdk

import "testing"

func TestDualStackListenBindingRequiresTCPUDPAndPort(t *testing.T) {
	if err := (DualStackListenBinding{Port: 8388, TCP: true, UDP: true}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (DualStackListenBinding{Port: 8388, TCP: true}).Validate(); err == nil {
		t.Fatal("TCP-only binding was accepted")
	}
	if err := (DualStackListenBinding{Port: 0, TCP: true, UDP: true}).Validate(); err == nil {
		t.Fatal("port 0 was accepted")
	}
}

func TestJoinShareHostPortRejectsWildcard(t *testing.T) {
	if _, err := JoinShareHostPort("0.0.0.0", 8388); err == nil {
		t.Fatal("wildcard share host was accepted")
	}
	got, err := JoinShareHostPort("203.0.113.10", 8388)
	if err != nil || got != "203.0.113.10:8388" {
		t.Fatalf("got %q err=%v", got, err)
	}
	got, err = JoinShareHostPort("2001:db8::1", 8388)
	if err != nil || got != "[2001:db8::1]:8388" {
		t.Fatalf("got %q err=%v", got, err)
	}
}

func TestValidL4BackendHostMatchesReverseL4Bound(t *testing.T) {
	if !ValidL4BackendHost("203.0.113.10") || !ValidL4BackendHost("ss.example.com") {
		t.Fatal("expected valid L4 hosts")
	}
	if ValidL4BackendHost("http://ss.example.com") || ValidL4BackendHost("ss.example.com/path") {
		t.Fatal("scheme or path host was accepted")
	}
}
