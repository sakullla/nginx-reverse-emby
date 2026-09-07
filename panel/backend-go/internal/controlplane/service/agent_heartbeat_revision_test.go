//go:build !integration

package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/revision"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type heartbeatPKIRevisionStore struct {
	agentStore
	row      storage.AgentRow
	snapshot storage.Snapshot
	payload  []byte
}

func (s *heartbeatPKIRevisionStore) ListAgents(context.Context) ([]storage.AgentRow, error) {
	return []storage.AgentRow{s.row}, nil
}

func (s *heartbeatPKIRevisionStore) SaveAgent(_ context.Context, row storage.AgentRow) error {
	s.row = row
	return nil
}

func (s *heartbeatPKIRevisionStore) ListHTTPRules(context.Context, string) ([]storage.HTTPRuleRow, error) {
	return nil, nil
}

func (s *heartbeatPKIRevisionStore) ListManagedCertificates(context.Context) ([]storage.ManagedCertificateRow, error) {
	return nil, nil
}

func (s *heartbeatPKIRevisionStore) LoadAgentSnapshot(context.Context, string, storage.AgentSnapshotInput) (storage.Snapshot, error) {
	return s.snapshot, nil
}

func (s *heartbeatPKIRevisionStore) EnsureAgentHeartbeatRevision(_ context.Context, _ string, snapshot storage.Snapshot, payload []byte, digest string, _ time.Time) (storage.AgentRevisionRow, error) {
	s.payload = append([]byte(nil), payload...)
	return storage.AgentRevisionRow{Revision: snapshot.Revision, SnapshotDigest: digest}, nil
}

type heartbeatRevisionPKIController struct {
	AgentPKIController
	state storage.PKICanonicalState
}

func (p heartbeatRevisionPKIController) ControlSyncAndPrepare(_ context.Context, agentID string, _ *storage.PKISecurityAcknowledgement, _ []PKIControlEnrollmentRequest, listeners []storage.RelayListener) (storage.PKISecuritySnapshot, []PKIControlCredential, []storage.RelayListener, error) {
	prepared, err := prepareRelayListenersWithPKIState(p.state, agentID, listeners)
	return storage.PKISecuritySnapshot{}, nil, prepared, err
}

func TestHeartbeatIssuesRevisionWithPreparedRelayIdentity(t *testing.T) {
	const agentID = "relay-agent"
	store := &heartbeatPKIRevisionStore{
		row: storage.AgentRow{ID: agentID, AgentToken: "test-token"},
		snapshot: storage.Snapshot{Revision: 116, RelayListeners: []storage.RelayListener{{
			ID: 2, AgentID: agentID, TLSMode: "pki_mtls", Enabled: true,
		}}},
	}
	certificateID := "listener-certificate"
	ownerDigest := sha256.Sum256([]byte(strings.Join([]string{
		"pki-identity-owner-v1", "test-domain", storage.PKIIdentityKindListener, agentID, "2",
	}, "\x00")))
	ownerKey := hex.EncodeToString(ownerDigest[:])
	svc := NewAgentService(config.Config{}, store)
	svc.pki = heartbeatRevisionPKIController{state: storage.PKICanonicalState{
		Settings: &storage.PKISettingsRow{PKIDomainID: "test-domain", UpgradeState: storage.PKIUpgradeStateTunnelMTLSOnly},
		Identities: []storage.PKIIdentityRow{{
			ID: "listener-identity", PKIDomainID: "test-domain", ActiveOwnerKey: &ownerKey,
			Kind: storage.PKIIdentityKindListener, AgentID: agentID,
			ListenerID: "2", State: storage.PKIIdentityStateActive, CurrentCertificateID: &certificateID,
		}},
	}}
	reply, err := svc.Heartbeat(t.Context(), HeartbeatRequest{AgentID: agentID, CurrentRevision: 114}, "test-token")
	if err != nil {
		t.Fatal(err)
	}
	var issued storage.Snapshot
	if err := json.Unmarshal(store.payload, &issued); err != nil {
		t.Fatal(err)
	}
	for source, listeners := range map[string][]storage.RelayListener{
		"heartbeat": reply.RelayListeners, "immutable artifact": issued.RelayListeners,
	} {
		if len(listeners) != 1 || listeners[0].PKIIdentityID != "listener-identity" ||
			listeners[0].PKIIdentityState != storage.PKIIdentityStateActive || listeners[0].PKICertificateID != certificateID {
			t.Fatalf("%s lost the canonical relay identity: %+v", source, listeners)
		}
	}
	_, digest, err := revision.CanonicalSnapshotPayload(issued)
	if err != nil || digest != reply.SnapshotDigest {
		t.Fatalf("issued digest = %q, heartbeat = %q, error = %v", digest, reply.SnapshotDigest, err)
	}
	if store.snapshot.RelayListeners[0].PKIIdentityID != "" {
		t.Fatal("heartbeat mutated the source listener slice")
	}
}

func TestHeartbeatComparableSnapshotIgnoresRelayPKIRuntimeOverlay(t *testing.T) {
	base := storage.Snapshot{Revision: 7, RelayListeners: []storage.RelayListener{{ID: 1, AgentID: "relay-agent"}}}
	decorated := base
	decorated.RelayListeners = append([]storage.RelayListener(nil), base.RelayListeners...)
	decorated.RelayListeners[0].PKIIdentityID = "identity-1"
	decorated.RelayListeners[0].PKIIdentityState = "active"
	decorated.RelayListeners[0].PKICertificateID = "certificate-1"

	_, baseDigest, err := revision.CanonicalSnapshotPayload(base)
	if err != nil {
		t.Fatal(err)
	}
	_, decoratedDigest, err := revision.CanonicalSnapshotPayload(decorated)
	if err != nil {
		t.Fatal(err)
	}
	if baseDigest == decoratedDigest {
		t.Fatal("relay PKI overlay unexpectedly left the immutable payload unchanged")
	}

	_, baseComparable, err := revision.CanonicalSnapshotPayload(heartbeatComparableSnapshot(base))
	if err != nil {
		t.Fatal(err)
	}
	_, decoratedComparable, err := revision.CanonicalSnapshotPayload(heartbeatComparableSnapshot(decorated))
	if err != nil {
		t.Fatal(err)
	}
	if baseComparable != decoratedComparable {
		t.Fatalf("heartbeat comparable digests differ: base=%s decorated=%s", baseComparable, decoratedComparable)
	}
	if decorated.RelayListeners[0].PKIIdentityID != "identity-1" || decorated.RelayListeners[0].PKICertificateID != "certificate-1" || decorated.RelayListeners[0].PKIIdentityState != "active" {
		t.Fatalf("comparison erased the live relay identity: %+v", decorated.RelayListeners[0])
	}
}

func TestBindHeartbeatRevisionKeepsLiveWhenComparableMatches(t *testing.T) {
	durable := storage.Snapshot{
		Revision: 7,
		Certificates: []storage.ManagedCertificateBundle{{
			ID: 1, Domain: "a.example",
		}},
		RelayListeners: []storage.RelayListener{{ID: 1, AgentID: "zouter"}},
	}
	live := durable
	live.VersionPackage = &storage.VersionPackage{
		URL:    "/panel-api/public/agent-assets/nre-agent-linux-amd64",
		SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	live.RelayListeners = append([]storage.RelayListener(nil), durable.RelayListeners...)
	live.RelayListeners[0].PKIIdentityID = "identity-1"
	live.RelayListeners[0].PKIIdentityState = "active"

	digest, bound, drifted, err := bindHeartbeatRevision(live, durable, "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB")
	if err != nil {
		t.Fatal(err)
	}
	if drifted {
		t.Fatal("comparable live overlay was treated as a durable drift")
	}
	if digest != "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" {
		t.Fatalf("digest = %q", digest)
	}
	if bound.VersionPackage != nil {
		t.Fatal("bound snapshot retained the version package overlay")
	}
	if len(bound.Certificates) != 1 || bound.Certificates[0].Domain != "a.example" {
		t.Fatalf("matched bind dropped live certificates: %+v", bound.Certificates)
	}
	if bound.RelayListeners[0].PKIIdentityID != "identity-1" || bound.RelayListeners[0].PKIIdentityState != "active" {
		t.Fatalf("matched bind dropped the authenticated relay identity: %+v", bound.RelayListeners[0])
	}
}

func TestBindHeartbeatRevisionPrefersDurableWhenLiveRematerializationDrifts(t *testing.T) {
	durable := storage.Snapshot{
		Revision:       7,
		DesiredVersion: "v1",
		Rules:          []storage.HTTPRule{{ID: 1, AgentID: "zouter", FrontendURL: "https://a.example"}},
	}
	live := durable
	live.PluginGenerations = []storage.PluginGeneration{{
		ID: "gen-live", InstanceID: "docker-app", PluginID: "docker-app", PackageDigest: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
	}}
	live.VersionPackage = &storage.VersionPackage{
		URL:    "/panel-api/public/agent-assets/nre-agent-linux-amd64",
		SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}

	digest, bound, drifted, err := bindHeartbeatRevision(live, durable, "DDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD")
	if err != nil {
		t.Fatal(err)
	}
	if !drifted {
		t.Fatal("live rematerialization was not treated as durable drift")
	}
	if digest != "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd" {
		t.Fatalf("digest = %q", digest)
	}
	if bound.VersionPackage != nil {
		t.Fatal("durable bind retained the version package overlay")
	}
	if len(bound.PluginGenerations) != 0 {
		t.Fatalf("drifted bind kept live plugin generations: %+v", bound.PluginGenerations)
	}
	if len(bound.Rules) != 1 || bound.Rules[0].FrontendURL != "https://a.example" {
		t.Fatalf("drifted bind lost durable rules: %+v", bound.Rules)
	}
}
