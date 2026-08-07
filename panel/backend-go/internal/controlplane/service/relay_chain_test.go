package service

import (
	"context"
	"errors"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestNormalizeRelayLayersInputRejectsFanoutOverLimit(t *testing.T) {
	t.Parallel()
	_, err := normalizeRelayLayersInput([][]int{
		{1, 2, 3, 4},
		{5, 6, 7, 8},
		{9, 10, 11},
	}, "tcp")
	if err == nil {
		t.Fatal("normalizeRelayLayersInput() error = nil")
	}
	if err.Error() != "invalid argument: relay_layers expand to more than 32 relay paths" {
		t.Fatalf("normalizeRelayLayersInput() error = %v", err)
	}
}

type relayAuthorizationStore struct {
	listeners []storage.RelayListenerRow
}

func (s relayAuthorizationStore) ListRelayListeners(context.Context, string) ([]storage.RelayListenerRow, error) {
	return s.listeners, nil
}

func TestValidateRelayChainRejectsUnauthorizedReferencedListener(t *testing.T) {
	wantErr := errors.New("hidden relay listener")
	ctx := WithResourceAuthorizer(t.Context(), func(_ context.Context, kind, id string) error {
		if kind == "relay_listener" && id == "hidden-agent:7" {
			return wantErr
		}
		return nil
	})
	err := validateRelayChainReferences(ctx, relayAuthorizationStore{listeners: []storage.RelayListenerRow{{ID: 7, AgentID: "hidden-agent", Enabled: true}}}, []string{"hidden-agent"}, []int{7}, relayChainValidationOptions{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("validateRelayChainReferences() error = %v, want authorization denial", err)
	}
}
