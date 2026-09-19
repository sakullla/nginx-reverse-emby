package http

import (
	"io"
	stdhttp "net/http"
	"net/http/httptest"
	"testing"
)

func TestApplyTransportOptionsDisablesHTTP2ForUpstreamRequests(t *testing.T) {
	backend := httptest.NewUnstartedServer(stdhttp.HandlerFunc(func(writer stdhttp.ResponseWriter, request *stdhttp.Request) {
		_, _ = io.WriteString(writer, request.Proto)
	}))
	backend.EnableHTTP2 = true
	backend.StartTLS()
	defer backend.Close()

	transport := NewSharedTransport()
	transport.TLSClientConfig = backend.Client().Transport.(*stdhttp.Transport).TLSClientConfig.Clone()
	protocols := new(stdhttp.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetHTTP2(true)
	protocols.SetUnencryptedHTTP2(true)
	transport.Protocols = protocols
	ApplyTransportOptions(transport, TransportOptions{DisableHTTP2: true})
	if transport.ForceAttemptHTTP2 {
		t.Fatal("ForceAttemptHTTP2 remained enabled")
	}
	if transport.TLSNextProto != nil {
		t.Fatal("TLSNextProto remained configured after disabling HTTP/2")
	}
	if transport.Protocols.HTTP2() || transport.Protocols.UnencryptedHTTP2() {
		t.Fatal("Transport.Protocols still allows HTTP/2")
	}

	response, err := (&stdhttp.Client{Transport: transport}).Get(backend.URL)
	if err != nil {
		t.Fatalf("upstream request failed: %v", err)
	}
	defer response.Body.Close()
	if response.ProtoMajor != 1 {
		t.Fatalf("upstream protocol = %s, want HTTP/1.1", response.Proto)
	}
}

func TestClassedTransportsHonorConfiguredConnectionCap(t *testing.T) {
	base := NewSharedTransport()
	base.MaxConnsPerHost = 7

	interactive, bulk := NewClassedDirectTransports(base)
	if interactive.MaxConnsPerHost != 7 || bulk.MaxConnsPerHost != 7 {
		t.Fatalf("classed connection caps = interactive:%d bulk:%d, want 7:7", interactive.MaxConnsPerHost, bulk.MaxConnsPerHost)
	}
}

func TestClassedTransportsKeepDefaultConnectionCaps(t *testing.T) {
	interactive, bulk := NewClassedDirectTransports(NewSharedTransport())
	if interactive.MaxConnsPerHost != 16 || bulk.MaxConnsPerHost != 64 {
		t.Fatalf("default classed connection caps = interactive:%d bulk:%d, want 16:64", interactive.MaxConnsPerHost, bulk.MaxConnsPerHost)
	}
}

func TestClassedTransportsHonorRaisedConnectionCap(t *testing.T) {
	base := NewSharedTransport()
	base.MaxConnsPerHost = 128

	interactive, bulk := NewClassedDirectTransports(base)
	if interactive.MaxConnsPerHost != 128 || bulk.MaxConnsPerHost != 128 {
		t.Fatalf("raised classed connection caps = interactive:%d bulk:%d, want 128:128", interactive.MaxConnsPerHost, bulk.MaxConnsPerHost)
	}
}
