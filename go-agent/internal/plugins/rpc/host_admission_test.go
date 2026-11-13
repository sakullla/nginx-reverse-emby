//go:build !windows

package rpc

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	managed "github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/network"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestHostAdmissionOverloadUsesRetryableWireError(t *testing.T) {
	manager := managed.NewManager()
	defer manager.Close()
	host := &Host{managed: manager, revoked: map[string]model.PluginGenerationRevokeRequest{}}
	candidate := HostCandidate{InstanceID: "instance", Generation: "generation", Scopes: []string{sdk.PermissionScopedSecretRead}, services: &runtimeServices{}}
	ready := make(chan struct{})
	attempt := &hostAttempt{cleanup: func() error { return nil }, handleReady: ready}
	environment, err := host.startHostRuntime(candidate, attemptSecurity{endpointDirectory: t.TempDir(), dial: DialConfig{Cookie: "test-private-credential"}}, attempt)
	if err != nil {
		t.Fatal(err)
	}
	defer attempt.cleanup()
	endpoint := strings.TrimPrefix(environment[0], sdk.EnvPluginHostEndpoint+"=unix:")
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", endpoint)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	request := func(operation string) (int, []byte, error) {
		payload, _ := json.Marshal(sdk.HostRuntimeCall{Operation: operation, Payload: json.RawMessage(`{}`)})
		r, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://host"+sdk.PluginHostCallPath, strings.NewReader(string(payload)))
		r.Header.Set(sdk.HeaderPluginHostCredential, "test-private-credential")
		response, err := client.Do(r)
		if err != nil {
			return 0, nil, err
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		return response.StatusCode, body, err
	}
	var workers sync.WaitGroup
	defer func() { cancel(); close(ready); workers.Wait() }()
	for i := 0; i < 128; i++ {
		workers.Add(1)
		go func() { defer workers.Done(); request(sdk.HostRuntimeScopedSecret) }()
	}
	for {
		status, body, err := request("state.get")
		if err != nil {
			t.Fatal(err)
		}
		if status == http.StatusTooManyRequests {
			var response sdk.HostRuntimeResponse
			if json.Unmarshal(body, &response) != nil || response.Validate() != nil || response.Error == nil || response.Error.Code != sdk.ErrorResourceExhausted || !response.Error.Retryable {
				t.Fatalf("overload must be a retryable wire error, got %q", body)
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
}
