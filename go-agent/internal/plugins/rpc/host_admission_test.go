//go:build !windows

package rpc

import (
	"context"
	"encoding/json"
	"errors"
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

type admissionSecretRedeemer struct {
	entered chan struct{}
	release chan struct{}
}

func (*admissionSecretRedeemer) RedeemPluginSecrets(context.Context, model.PluginSecretRedemptionRequest) ([]model.PluginRedeemedSecret, error) {
	return nil, errors.New("unexpected unscoped secret request")
}

func (r *admissionSecretRedeemer) RedeemScopedPluginSecret(ctx context.Context, _ model.PluginSecretRedemptionRequest) (json.RawMessage, error) {
	r.entered <- struct{}{}
	select {
	case <-r.release:
		return nil, errors.New("fixture released")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestHostAdmissionOverloadUsesRetryableWireError(t *testing.T) {
	manager := managed.NewManager()
	defer manager.Close()
	host := &Host{managed: manager, revoked: map[string]model.PluginGenerationRevokeRequest{}}
	candidate := HostCandidate{InstanceID: "instance", Generation: "generation", Scopes: []string{sdk.PermissionScopedSecretRead}, services: &runtimeServices{}}
	candidate.Grants = []model.PluginGrantProjection{{Name: sdk.PermissionScopedSecretRead, ResourceKind: "secret-scope", ResourceID: "fixture"}}
	redeemer := &admissionSecretRedeemer{entered: make(chan struct{}, managed.MaxActiveOperations), release: make(chan struct{})}
	host.SetSecretRedeemer(redeemer)
	attempt := &hostAttempt{cleanup: func() error { return nil }}
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
	secretPayload, err := sdk.EncodeScopedSecretRequest(sdk.ScopedSecretRequest{
		Action:    sdk.ScopedSecretRead,
		Binding:   attempt.network.Binding(),
		Reference: sdk.ScopedSecretReference{InstanceID: "instance", ID: "secret", Scope: "fixture", Version: strings.Repeat("a", 43)},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := func(operation string) (int, []byte, error) {
		inner := json.RawMessage(`{}`)
		if operation == sdk.HostRuntimeScopedSecret {
			inner = secretPayload
		}
		payload, _ := json.Marshal(sdk.HostRuntimeCall{Operation: operation, Payload: inner})
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
	defer func() { cancel(); close(redeemer.release); workers.Wait() }()
	finished := make(chan error, managed.MaxActiveOperations)
	for i := 0; i < managed.MaxActiveOperations; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			_, _, err := request(sdk.HostRuntimeScopedSecret)
			finished <- err
		}()
	}
	// Wait until all work slots are actually held. Probing during setup can
	// reject one of the fixture's own requests and leave the pool unsaturated.
	for i := 0; i < managed.MaxActiveOperations; i++ {
		select {
		case <-redeemer.entered:
		case err := <-finished:
			t.Fatalf("blocking request returned before release: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	status, body, err := request("state.get")
	if err != nil {
		t.Fatal(err)
	}
	var response sdk.HostRuntimeResponse
	if status != http.StatusTooManyRequests || json.Unmarshal(body, &response) != nil || response.Validate() != nil || response.Error == nil || response.Error.Code != sdk.ErrorResourceExhausted || !response.Error.Retryable {
		t.Fatalf("overload must be a retryable wire error, got status %d: %q", status, body)
	}
}
