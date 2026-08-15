package rpc

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

type countingProviderCloser struct{ count atomic.Int32 }

func (closer *countingProviderCloser) Close() error {
	closer.count.Add(1)
	return nil
}

type providerReadWriteBody struct{ *bytes.Buffer }

func (body *providerReadWriteBody) Close() error { return nil }

func TestHTTPBackendProviderResponseLeaseReleasesExactlyOnce(t *testing.T) {
	t.Run("EOF and close", func(t *testing.T) {
		lease := &countingProviderCloser{}
		response := &http.Response{Body: io.NopCloser(bytes.NewReader([]byte("payload")))}
		WrapHTTPBackendProviderResponseLease(t.Context(), response, lease)
		payload, err := io.ReadAll(response.Body)
		if err != nil || string(payload) != "payload" {
			t.Fatalf("ReadAll() = %q, %v", payload, err)
		}
		_ = response.Body.Close()
		if got := lease.count.Load(); got != 1 {
			t.Fatalf("lease releases = %d, want 1", got)
		}
	})

	t.Run("upgrade preserves read write closer", func(t *testing.T) {
		lease := &countingProviderCloser{}
		body := &providerReadWriteBody{Buffer: bytes.NewBufferString("upgrade")}
		response := &http.Response{Body: body}
		WrapHTTPBackendProviderResponseLease(t.Context(), response, lease)
		if _, ok := response.Body.(io.ReadWriteCloser); !ok {
			t.Fatalf("wrapped body type = %T", response.Body)
		}
		_ = response.Body.Close()
		_ = response.Body.Close()
		if got := lease.count.Load(); got != 1 {
			t.Fatalf("upgrade lease releases = %d, want 1", got)
		}
	})

	t.Run("request cancellation", func(t *testing.T) {
		lease := &countingProviderCloser{}
		ctx, cancel := context.WithCancel(context.Background())
		response := &http.Response{Body: io.NopCloser(bytes.NewReader(nil))}
		WrapHTTPBackendProviderResponseLease(ctx, response, lease)
		cancel()
		deadline := time.Now().Add(time.Second)
		for lease.count.Load() == 0 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if got := lease.count.Load(); got != 1 {
			t.Fatalf("canceled lease releases = %d, want 1", got)
		}
	})
}

func TestStripUntrustedProviderHeadersRemovesForwardingInternalAndHopByHop(t *testing.T) {
	header := http.Header{
		"Forwarded":                 {"for=spoofed"},
		"X-Forwarded-For":           {"spoofed"},
		"X-Nre-Provider-Credential": {"spoofed"},
		"Connection":                {"keep-alive, X-Remove-Me"},
		"X-Remove-Me":               {"secret"},
		"Proxy-Authorization":       {"secret"},
		"Te":                        {"trailers"},
	}
	stripUntrustedProviderHeaders(header)
	for key := range header {
		t.Fatalf("header %q survived provider sanitization", key)
	}
}

func TestHTTPBackendProviderLeaseDrainWaitsForActiveLease(t *testing.T) {
	handle := &HTTPBackendProviderHandle{instance: &HostedInstance{}, providerID: "default"}
	lease, err := handle.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	drained := make(chan error, 1)
	go func() { drained <- handle.drain(t.Context()) }()
	select {
	case err := <-drained:
		t.Fatalf("drain returned before lease close: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	_ = lease.Close()
	if err := <-drained; err != nil {
		t.Fatal(err)
	}
	if _, err := handle.Acquire(); err == nil {
		t.Fatal("draining handle accepted a new lease")
	}
}

func TestHTTPBackendProviderAuthorityRejectsUnsafeValues(t *testing.T) {
	for _, authority := range []HTTPBackendProviderAuthority{
		{Scheme: "ftp", Host: "example.test"},
		{Scheme: "https", Host: "example.test\r\nInjected: yes"},
		{Scheme: "https", Host: "example.test;for=attacker"},
		{Scheme: "https", Host: "example.test, proto=http"},
		{Scheme: "https", Host: "example.test\\\";proto=http"},
		{Scheme: "https", Host: "example.test", ClientAddress: "attacker;for=203.0.113.1"},
	} {
		if err := authority.Validate(); err == nil {
			t.Fatalf("Validate(%#v) accepted unsafe authority", authority)
		}
	}
	if err := (HTTPBackendProviderAuthority{Scheme: "https", Host: "example.test", ClientAddress: "203.0.113.1:1234"}).Validate(); err != nil {
		t.Fatal(err)
	}
}
