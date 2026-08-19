package pluginsdk

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestHostRuntimeClientUsesPrivateEndpointAndCredential(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix sockets are exercised by the packaged Linux runtime")
	}
	directory := t.TempDir()
	socket := filepath.Join(directory, "host.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != PluginHostCallPath || request.Header.Get(HeaderPluginHostCredential) != "attempt-cookie" {
			http.Error(writer, "denied", http.StatusForbidden)
			return
		}
		_ = json.NewEncoder(writer).Encode(HostRuntimeResponse{Payload: json.RawMessage(`{"ready":true}`)})
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	cookieFile := filepath.Join(directory, "cookie")
	if err := os.WriteFile(cookieFile, []byte("attempt-cookie"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvPluginHostEndpoint, "unix:"+socket)
	t.Setenv("NRE_PLUGIN_COOKIE_FILE", cookieFile)
	client, err := NewHostRuntimeClientFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Ready bool `json:"ready"`
	}
	if err := client.Call(context.Background(), HostRuntimeCall{Operation: "state.get", Payload: json.RawMessage(`{"key":"catalog"}`)}, &result); err != nil {
		t.Fatal(err)
	}
	if !result.Ready {
		t.Fatal("host runtime response was not decoded")
	}
}

func TestHostRuntimeCallRejectsUnboundedOrInvalidPayload(t *testing.T) {
	if err := (HostRuntimeCall{Operation: "state.get", Payload: json.RawMessage(`{"key":`)}).Validate(); err == nil {
		t.Fatal("invalid JSON payload was accepted")
	}
	if err := (HostRuntimeCall{Operation: "state.get", OperationID: "contains\nnewline"}).Validate(); err == nil {
		t.Fatal("invalid operation identity was accepted")
	}
	if err := (&HostRuntimeClient{}).Call(context.Background(), HostRuntimeCall{Operation: "state.get"}, nil); err == nil {
		t.Fatal("unconfigured host runtime client was accepted")
	}
}
