package ddns

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func newEchoServer(t *testing.T, status int, body string) (*httptest.Server, *http.Client) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	client := server.Client()
	return server, client
}

func TestExtractIPv4PublicAPI(t *testing.T) {
	server, client := newEchoServer(t, http.StatusOK, "203.0.113.55")
	got := ExtractIPv4(context.Background(), model.DDNSFamily{Enabled: true, Source: "public_api"}, client, server.URL)
	if got != "203.0.113.55" {
		t.Fatalf("ExtractIPv4 = %q, want 203.0.113.55", got)
	}
}

func TestExtractIPv6PublicAPI(t *testing.T) {
	server, client := newEchoServer(t, http.StatusOK, "2001:db8::1")
	got := ExtractIPv6(context.Background(), model.DDNSFamily{Enabled: true, Source: "public_api"}, client, server.URL)
	if got != "2001:db8::1" {
		t.Fatalf("ExtractIPv6 = %q, want 2001:db8::1", got)
	}
}

func TestExtractPublicAPIFamilyMismatchDropsAddress(t *testing.T) {
	// IPv4 extractor must reject an IPv6 body and vice versa.
	server, client := newEchoServer(t, http.StatusOK, "2001:db8::9")
	if got := ExtractIPv4(context.Background(), model.DDNSFamily{Enabled: true, Source: "public_api"}, client, server.URL); got != "" {
		t.Fatalf("ExtractIPv4 with v6 body = %q, want empty", got)
	}

	server2, client2 := newEchoServer(t, http.StatusOK, "203.0.113.9")
	if got := ExtractIPv6(context.Background(), model.DDNSFamily{Enabled: true, Source: "public_api"}, client2, server2.URL); got != "" {
		t.Fatalf("ExtractIPv6 with v4 body = %q, want empty", got)
	}
}

func TestExtractPublicAPIFailureReturnsEmpty(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{name: "non-200", status: http.StatusInternalServerError, body: ""},
		{name: "garbage", status: http.StatusOK, body: "not-an-ip"},
		{name: "empty", status: http.StatusOK, body: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, client := newEchoServer(t, tc.status, tc.body)
			if got := ExtractIPv4(context.Background(), model.DDNSFamily{Enabled: true, Source: "public_api"}, client, server.URL); got != "" {
				t.Fatalf("ExtractIPv4 = %q, want empty", got)
			}
		})
	}
}

func TestExtractPublicAPIFallsThroughToNextURL(t *testing.T) {
	// First endpoint returns garbage; extraction must fall through to the
	// second comma-separated URL and return its address.
	badServer, client := newEchoServer(t, http.StatusOK, "not-an-ip")
	goodServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "203.0.113.77")
	}))
	t.Cleanup(goodServer.Close)
	urls := badServer.URL + ", " + goodServer.URL
	got := ExtractIPv4(context.Background(), model.DDNSFamily{Enabled: true, Source: "public_api"}, client, urls)
	if got != "203.0.113.77" {
		t.Fatalf("ExtractIPv4 multi-URL = %q, want 203.0.113.77", got)
	}
}

func TestExtractPublicAPIAllURLsFailReturnsEmpty(t *testing.T) {
	a, client := newEchoServer(t, http.StatusInternalServerError, "")
	b, _ := newEchoServer(t, http.StatusOK, "garbage")
	got := ExtractIPv4(context.Background(), model.DDNSFamily{Enabled: true, Source: "public_api"}, client, a.URL+","+b.URL)
	if got != "" {
		t.Fatalf("ExtractIPv4 all-fail = %q, want empty", got)
	}
}

func TestSplitPublicAPIURLs(t *testing.T) {
	cases := []struct {
		name string
		csv  string
		want []string
	}{
		{name: "single", csv: "https://api.ipify.org", want: []string{"https://api.ipify.org"}},
		{name: "multiple", csv: "https://a,https://b,https://c", want: []string{"https://a", "https://b", "https://c"}},
		{name: "trim spaces", csv: " https://a , https://b ", want: []string{"https://a", "https://b"}},
		{name: "drop empties", csv: "https://a,, https://b,", want: []string{"https://a", "https://b"}},
		{name: "dedup preserves order", csv: "https://a,https://b,https://a", want: []string{"https://a", "https://b"}},
		{name: "blank", csv: "   ", want: []string{}},
		{name: "empty", csv: "", want: []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := splitPublicAPIURLs(tc.csv)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("splitPublicAPIURLs(%q) = %#v, want %#v", tc.csv, got, tc.want)
			}
		})
	}
}

func TestExtractDisabledReturnsEmpty(t *testing.T) {
	server, client := newEchoServer(t, http.StatusOK, "203.0.113.55")
	got := ExtractIPv4(context.Background(), model.DDNSFamily{Enabled: false, Source: "public_api"}, client, server.URL)
	if got != "" {
		t.Fatalf("ExtractIPv4 with disabled family = %q, want empty", got)
	}
}

func TestExtractUnknownSourceReturnsEmpty(t *testing.T) {
	server, client := newEchoServer(t, http.StatusOK, "203.0.113.55")
	got := ExtractIPv4(context.Background(), model.DDNSFamily{Enabled: true, Source: "carrier-pigeon"}, client, server.URL)
	if got != "" {
		t.Fatalf("ExtractIPv4 with unknown source = %q, want empty", got)
	}
}

func TestExtractInterfaceMissingNameReturnsEmpty(t *testing.T) {
	if got := ExtractIPv4(context.Background(), model.DDNSFamily{Enabled: true, Source: "interface", Interface: "definitely-not-real-0"}, nil, ""); got != "" {
		t.Fatalf("ExtractIPv4 missing interface = %q, want empty", got)
	}
	if got := ExtractIPv6(context.Background(), model.DDNSFamily{Enabled: true, Source: "interface", Interface: "  "}, nil, ""); got != "" {
		t.Fatalf("ExtractIPv6 blank interface name = %q, want empty", got)
	}
}

// TestExtractInterfaceFindsRealAddress is a best-effort positive test: it scans
// the host interfaces for a usable global address and skips if the test
// environment exposes none (e.g. a minimal CI container with only loopback).
func TestExtractInterfaceFindsRealAddress(t *testing.T) {
	name := firstUsableInterface(t)
	if name == "" {
		t.Skip("no usable network interface with a global address available")
	}
	v4 := ExtractIPv4(context.Background(), model.DDNSFamily{Enabled: true, Source: "interface", Interface: name}, nil, "")
	v6 := ExtractIPv6(context.Background(), model.DDNSFamily{Enabled: true, Source: "interface", Interface: name}, nil, "")
	if v4 == "" && v6 == "" {
		t.Fatalf("expected at least one address from interface %q, got empty", name)
	}
	if v4 != "" && net.ParseIP(v4) == nil {
		t.Fatalf("ExtractIPv4 returned non-IP %q", v4)
	}
	if v6 != "" && net.ParseIP(v6) == nil {
		t.Fatalf("ExtractIPv6 returned non-IP %q", v6)
	}
}

func firstUsableInterface(t *testing.T) string {
	t.Helper()
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ip := addrIP(addr)
			if ip == nil || isUnusable(ip) {
				continue
			}
			return iface.Name
		}
	}
	return ""
}
