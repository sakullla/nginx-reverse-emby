package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	modulepki "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/pki"
)

func TestRelaySecurityPrefetchClaimThrottle(t *testing.T) {
	client := &relaySecuritySyncClient{}
	if !client.claimPrefetchSync() {
		t.Fatal("first prefetch claim was denied")
	}
	if client.claimPrefetchSync() {
		t.Fatal("immediate repeat prefetch claim was admitted")
	}
	client.markSecuritySyncRecovered()
	if !client.claimPrefetchSync() {
		t.Fatal("prefetch claim was not re-armed after recovery")
	}
}

// A persistently broken panel-side enrollment must not double every heartbeat
// round trip: the first degraded heartbeat completes the enrollment attempt
// immediately, later heartbeats within the interval skip the extra round trip
// and report the verdict it would have produced, and the interval eventually
// admits another attempt.
func TestRelaySecuritySyncBoundsPrefetchWhileListenerCredentialStaysUnavailable(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	store := &fakeRemotePKIStore{
		active: map[string]modulepki.CredentialMetadata{
			remoteAgentPKIStorageIdentity: testAgentCredentialMetadata("agent-1", "agent-certificate", now.Add(-time.Hour), now.Add(90*24*time.Hour)),
		},
		security: modulepki.SecurityState{Snapshot: model.PKISecuritySnapshot{
			PKIDomainID: "domain-1", PKIEpoch: 1, SecurityRevision: 4, Full: true, IssuedAt: now,
			TrustRoots: []model.PKITrustRoot{{AuthorityID: "authority-1", Generation: 3, Status: "active"}},
		}},
	}
	handler := newRemotePKIHeartbeatHandler(store, "agent-1")
	handler.now = func() time.Time { return now }
	listener := model.RelayListener{
		ID: 71, AgentID: "agent-1", ListenHost: "0.0.0.0", BindHosts: []string{"0.0.0.0", "192.0.2.71"},
		PublicHost: "relay.example.test", Enabled: true, TLSMode: "pki_mtls",
		PKIIdentityID: "listener-identity-71", PKIIdentityState: "enrollment_required",
	}
	calls := 0
	delegate := syncClientFunc(func(context.Context, SyncRequest) (Snapshot, error) {
		calls++
		return Snapshot{RelayListeners: []model.RelayListener{listener}}, nil
	})
	current := now
	client := &relaySecuritySyncClient{delegate: delegate, pki: handler, now: func() time.Time { return current }}

	if _, err := client.Sync(t.Context(), SyncRequest{}); err == nil || !strings.Contains(err.Error(), "relay listener PKI credential is not ready") {
		t.Fatalf("first degraded Sync() error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("first degraded heartbeat round trips = %d, want 2", calls)
	}
	if _, err := client.Sync(t.Context(), SyncRequest{}); err == nil || !strings.Contains(err.Error(), "relay listener PKI credential is not ready") {
		t.Fatalf("throttled degraded Sync() error = %v", err)
	}
	if calls != 3 {
		t.Fatalf("throttled heartbeat round trips = %d, want 3 (no prefetch repeat)", calls)
	}
	current = current.Add(2 * relaySecurityPrefetchInterval)
	if _, err := client.Sync(t.Context(), SyncRequest{}); err == nil || !strings.Contains(err.Error(), "relay listener PKI credential is not ready") {
		t.Fatalf("post-interval degraded Sync() error = %v", err)
	}
	if calls != 5 {
		t.Fatalf("post-interval heartbeat round trips = %d, want 5 (prefetch admitted again)", calls)
	}
}
