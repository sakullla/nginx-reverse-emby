package upstream

import (
	"net/http/httptest"
	"testing"
)

func TestClassifyHTTPRequestRangeAsBulk(t *testing.T) {
	req := httptest.NewRequest("GET", "http://edge.example/Items/1", nil)
	req.Header.Set("Range", "bytes=0-1048575")

	if got := ClassifyHTTPRequest(req); got != TrafficClassBulk {
		t.Fatalf("ClassifyHTTPRequest() = %q, want %q", got, TrafficClassBulk)
	}
}

func TestClassifyHTTPRequestWithoutRangeAsInteractive(t *testing.T) {
	req := httptest.NewRequest("GET", "http://edge.example/Users/Public", nil)

	if got := ClassifyHTTPRequest(req); got != TrafficClassInteractive {
		t.Fatalf("ClassifyHTTPRequest() = %q, want %q", got, TrafficClassInteractive)
	}
}

func TestClassifyHTTPRequestNilAsUnknown(t *testing.T) {
	if got := ClassifyHTTPRequest(nil); got != TrafficClassUnknown {
		t.Fatalf("ClassifyHTTPRequest(nil) = %q, want %q", got, TrafficClassUnknown)
	}
}

func TestClassifyL4UDPAsBulk(t *testing.T) {
	if got := ClassifyL4("udp", 0, 0); got != TrafficClassBulk {
		t.Fatalf("ClassifyL4() = %q, want %q", got, TrafficClassBulk)
	}
}

func TestClassifyL4TCPByteThresholdAsBulk(t *testing.T) {
	if got := ClassifyL4("tcp", 128*1024, 0); got != TrafficClassBulk {
		t.Fatalf("ClassifyL4() = %q, want %q", got, TrafficClassBulk)
	}
}

func TestClassifyL4TCPDurationThresholdAsBulk(t *testing.T) {
	if got := ClassifyL4("tcp", 0, 5); got != TrafficClassBulk {
		t.Fatalf("ClassifyL4() = %q, want %q", got, TrafficClassBulk)
	}
}

func TestClassifyL4TCPBelowThresholdsAsInteractive(t *testing.T) {
	if got := ClassifyL4("tcp", 128*1024-1, 4); got != TrafficClassInteractive {
		t.Fatalf("ClassifyL4() = %q, want %q", got, TrafficClassInteractive)
	}
}

func TestClassifyL4UnknownProtocolAsUnknown(t *testing.T) {
	if got := ClassifyL4("sctp", 128*1024, 5); got != TrafficClassUnknown {
		t.Fatalf("ClassifyL4() = %q, want %q", got, TrafficClassUnknown)
	}
}
