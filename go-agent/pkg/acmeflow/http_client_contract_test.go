package acmeflow

import (
	"net/http"
	"testing"
	"time"
)

func TestACMEHTTPClientBoundedDefaultsAndConfiguredPreservation(t *testing.T) {
	t.Run("protocol default", func(t *testing.T) {
		client := NewProtocolClient(ClientConfig{}).(*protocolClient).client.HTTPClient
		assertBoundedACMEHTTPClient(t, client)
	})
	t.Run("protocol configured", func(t *testing.T) {
		configured := &http.Client{Timeout: 17 * time.Second}
		client := NewProtocolClient(ClientConfig{HTTPClient: configured}).(*protocolClient).client.HTTPClient
		if client != configured {
			t.Fatalf("HTTP client = %p, want configured client %p", client, configured)
		}
	})
	t.Run("profile default", func(t *testing.T) {
		assertBoundedACMEHTTPClient(t, profileHTTPClient(nil))
	})
	t.Run("profile configured clone", func(t *testing.T) {
		transport := &http.Transport{ResponseHeaderTimeout: 11 * time.Second}
		configured := &http.Client{Timeout: 17 * time.Second, Transport: transport}
		client := profileHTTPClient(configured)
		if client == configured || client.Timeout != configured.Timeout || client.Transport != transport {
			t.Fatalf("profile clone = %#v, configured = %#v", client, configured)
		}
		if configured.CheckRedirect != nil || client.CheckRedirect == nil {
			t.Fatal("profile clone mutated configured redirect policy or omitted its own")
		}
	})
}

func assertBoundedACMEHTTPClient(t *testing.T, client *http.Client) {
	t.Helper()
	if client == nil {
		t.Fatal("HTTP client is nil")
	}
	if client.Timeout != 2*time.Minute {
		t.Fatalf("HTTP client timeout = %v, want %v", client.Timeout, 2*time.Minute)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.ResponseHeaderTimeout != 30*time.Second {
		t.Fatalf("HTTP transport = %#v", client.Transport)
	}
}
