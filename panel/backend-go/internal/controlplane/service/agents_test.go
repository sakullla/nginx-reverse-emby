package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/revision"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type fakeStore struct {
	agents              []storage.AgentRow
	rulesByID           map[string][]storage.HTTPRuleRow
	l4RulesByID         map[string][]storage.L4RuleRow
	relayByID           map[string][]storage.RelayListenerRow
	managedCerts        []storage.ManagedCertificateRow
	events              []storage.AgentTrafficEventRow
	localState          storage.LocalAgentStateRow
	savedAgent          storage.AgentRow
	savedAgentCalls     int
	savedHeartbeatCalls int
	deletedAgentID      string
	snapshot            storage.Snapshot
	localSnapshot       storage.Snapshot
	loadSnapshotCalls   int
	lastSnapshotAgentID string
	lastSnapshotInput   storage.AgentSnapshotInput
	savedRuntimeState   storage.RuntimeState
	savedRuntimeAgentID string
	saveRuntimeCalls    int
	revisionPointers    map[string]storage.AgentRevisionPointerRow
	revisions           map[string]storage.AgentRevisionRow
	ensureRevisionCalls int
	issuedSnapshot      storage.Snapshot
	issuedPayload       []byte
	issuedDigest        string
	pluginReports       []storage.PluginGenerationReport
}

func (f *fakeStore) RecordPluginAgentRuntimeReport(_ context.Context, report storage.PluginGenerationReport) (storage.PluginAgentRuntimeStatusRow, bool, error) {
	f.pluginReports = append(f.pluginReports, report)
	return storage.PluginAgentRuntimeStatusRow{}, false, nil
}

func (f *fakeStore) GetAgentRevisionPointer(_ context.Context, agentID string) (storage.AgentRevisionPointerRow, bool, error) {
	row, found := f.revisionPointers[agentID]
	return row, found, nil
}

func (f *fakeStore) GetCoordinatorRevision(_ context.Context, agentID string, revision int64) (storage.AgentRevisionRow, bool, error) {
	row, found := f.revisions[fmt.Sprintf("%s/%d", agentID, revision)]
	return row, found, nil
}

func (f *fakeStore) RetryCoordinatorRevision(_ context.Context, agentID string, revision int64, _ time.Time) (storage.AgentRevisionRow, error) {
	row, found := f.revisions[fmt.Sprintf("%s/%d", agentID, revision)]
	if !found {
		return storage.AgentRevisionRow{}, errors.New("revision not found")
	}
	return row, nil
}

func (f *fakeStore) EnsureAgentHeartbeatRevision(_ context.Context, agentID string, snapshot storage.Snapshot, payload []byte, digest string, now time.Time) (storage.AgentRevisionRow, error) {
	f.ensureRevisionCalls++
	f.issuedSnapshot = snapshot
	f.issuedPayload = append([]byte(nil), payload...)
	f.issuedDigest = digest
	key := fmt.Sprintf("%s/%d", agentID, snapshot.Revision)
	if row, found := f.revisions[key]; found {
		if !strings.EqualFold(row.SnapshotDigest, digest) {
			return storage.AgentRevisionRow{}, errors.New("conflicting snapshot digest")
		}
		return row, nil
	}
	if f.revisions == nil {
		f.revisions = make(map[string]storage.AgentRevisionRow)
	}
	row := storage.AgentRevisionRow{AgentID: agentID, Revision: snapshot.Revision, SnapshotDigest: digest, CreatedAt: now, UpdatedAt: now}
	f.revisions[key] = row
	return row, nil
}

type agentPKIControlErrorStub struct {
	err error
}

func (s agentPKIControlErrorStub) RegisterAgent(context.Context, RegisterRequest, storage.AgentRow) (PKIRegistrationReply, error) {
	return PKIRegistrationReply{}, s.err
}

func (s agentPKIControlErrorStub) ControlSync(context.Context, string, *storage.PKISecurityAcknowledgement, []PKIControlEnrollmentRequest) (storage.PKISecuritySnapshot, []PKIControlCredential, error) {
	return storage.PKISecuritySnapshot{}, nil, s.err
}

func (agentPKIControlErrorStub) PrepareRelayListeners(_ context.Context, _ string, listeners []storage.RelayListener) ([]storage.RelayListener, error) {
	return listeners, nil
}

func (s agentPKIControlErrorStub) ControlSyncAndPrepare(context.Context, string, *storage.PKISecurityAcknowledgement, []PKIControlEnrollmentRequest, []storage.RelayListener) (storage.PKISecuritySnapshot, []PKIControlCredential, []storage.RelayListener, error) {
	return storage.PKISecuritySnapshot{}, nil, nil, s.err
}

type heartbeatPKIProjectionStub struct {
	calls *[]string
}

func (heartbeatPKIProjectionStub) RegisterAgent(context.Context, RegisterRequest, storage.AgentRow) (PKIRegistrationReply, error) {
	return PKIRegistrationReply{}, nil
}

func (s heartbeatPKIProjectionStub) ControlSync(context.Context, string, *storage.PKISecurityAcknowledgement, []PKIControlEnrollmentRequest) (storage.PKISecuritySnapshot, []PKIControlCredential, error) {
	if s.calls != nil {
		*s.calls = append(*s.calls, "control")
	}
	return storage.PKISecuritySnapshot{
		PKIDomainID: "pki-domain", PKIEpoch: 3, SecurityRevision: 7, Full: true,
		IssuedAt:           time.Date(2026, time.April, 11, 8, 0, 0, 0, time.UTC),
		TrustRoots:         []storage.PKITrustRoot{{AuthorityID: "ca-1", Generation: 2, Status: "active", CertificatePEM: "CA"}},
		RevokedIdentityIDs: []string{"identity-old"}, RevokedSerials: []string{"serial-old"},
		SignerGeneration: 2, Signature: []byte("signature"),
	}, nil, nil
}

func (s heartbeatPKIProjectionStub) PrepareRelayListeners(_ context.Context, _ string, listeners []storage.RelayListener) ([]storage.RelayListener, error) {
	if s.calls != nil {
		*s.calls = append(*s.calls, "prepare")
	}
	prepared := append([]storage.RelayListener(nil), listeners...)
	for index := range prepared {
		prepared[index].ListenPort = 9443
		prepared[index].TransportMode = "quic"
	}
	return prepared, nil
}

func (s heartbeatPKIProjectionStub) ControlSyncAndPrepare(ctx context.Context, agentID string, acknowledgement *storage.PKISecurityAcknowledgement, requests []PKIControlEnrollmentRequest, listeners []storage.RelayListener) (storage.PKISecuritySnapshot, []PKIControlCredential, []storage.RelayListener, error) {
	snapshot, credentials, err := s.ControlSync(ctx, agentID, acknowledgement, requests)
	if err != nil {
		return storage.PKISecuritySnapshot{}, credentials, nil, err
	}
	prepared, err := s.PrepareRelayListeners(ctx, agentID, listeners)
	return snapshot, credentials, prepared, err
}

func TestControlSyncCredentialValidationFailsClosedAcrossConcurrentRotation(t *testing.T) {
	credential := PKIControlCredential{RequestID: "request-1", Credential: storage.PKITunnelCredential{
		IdentityID: "identity-1", CertificateID: "certificate-old", AuthorityID: "authority-old", CAGeneration: 1,
	}}
	rotated := storage.PKICanonicalState{
		Authorities: []storage.PKIAuthorityRow{{ID: "authority-new", Generation: 2, Status: "active"}},
		Certificates: []storage.PKICertificateRow{{
			ID: "certificate-new", IdentityID: "identity-1", AuthorityID: "authority-new", CAGeneration: 2,
			Status: storage.PKICertificateStatusActive,
		}},
	}
	if err := validateControlCredentialsAgainstState([]PKIControlCredential{credential}, rotated); !errors.Is(err, storage.ErrPKIInvariant) {
		t.Fatalf("validation after concurrent rotation error = %v, want fail-closed invariant", err)
	}
	rotated.Authorities = append(rotated.Authorities, storage.PKIAuthorityRow{ID: "authority-old", Generation: 1, Status: "retiring"})
	rotated.Certificates = append(rotated.Certificates, storage.PKICertificateRow{
		ID: "certificate-old", IdentityID: "identity-1", AuthorityID: "authority-old", CAGeneration: 1,
		Status: storage.PKICertificateStatusActive,
	})
	currentCertificateID := "certificate-old"
	rotated.Identities = []storage.PKIIdentityRow{{
		ID: "identity-1", State: storage.PKIIdentityStateActive, CurrentCertificateID: &currentCertificateID,
	}}
	if err := validateControlCredentialsAgainstState([]PKIControlCredential{credential}, rotated); err != nil {
		t.Fatalf("validation with retained rotation trust state error = %v", err)
	}
}

type fakeHeartbeatTrafficService struct {
	ingestCalls []fakeHeartbeatTrafficIngest
	ingestErr   error
	summary     TrafficSummary
	summaryErr  error
}

type fakeHeartbeatTrafficIngest struct {
	agentID string
	stats   AgentStats
}

func (f *fakeHeartbeatTrafficService) IngestHeartbeat(_ context.Context, agentID string, stats AgentStats) error {
	f.ingestCalls = append(f.ingestCalls, fakeHeartbeatTrafficIngest{
		agentID: agentID,
		stats:   stats,
	})
	if f.ingestErr != nil {
		return f.ingestErr
	}
	return nil
}

func (f *fakeHeartbeatTrafficService) Summary(context.Context, string) (TrafficSummary, error) {
	if f.summaryErr != nil {
		return TrafficSummary{}, f.summaryErr
	}
	return f.summary, nil
}

func (f *fakeHeartbeatTrafficService) BlockState(context.Context, string) (bool, string, error) {
	if f.summaryErr != nil {
		return false, "", f.summaryErr
	}
	if !f.summary.Blocked {
		return false, "", nil
	}
	return true, f.summary.BlockReason, nil
}

func (f *fakeStore) ListAgents(context.Context) ([]storage.AgentRow, error) {
	return append([]storage.AgentRow(nil), f.agents...), nil
}

func (f *fakeStore) ListHTTPRules(_ context.Context, agentID string) ([]storage.HTTPRuleRow, error) {
	return append([]storage.HTTPRuleRow(nil), f.rulesByID[agentID]...), nil
}

func (f *fakeStore) LoadLocalAgentState(context.Context) (storage.LocalAgentStateRow, error) {
	return f.localState, nil
}

func (f *fakeStore) LoadLocalRuntimeState(context.Context) (storage.RuntimeState, error) {
	return f.savedRuntimeState, nil
}

func (f *fakeStore) SaveAgent(_ context.Context, row storage.AgentRow) error {
	f.savedAgent = row
	f.savedAgentCalls++
	for i := range f.agents {
		if f.agents[i].ID == row.ID {
			f.agents[i] = row
			return nil
		}
	}
	f.agents = append(f.agents, row)
	return nil
}

func (f *fakeStore) SaveAgentHeartbeat(_ context.Context, row storage.AgentRow) error {
	f.savedAgent = row
	f.savedHeartbeatCalls++
	for i := range f.agents {
		if f.agents[i].ID == row.ID {
			f.agents[i] = row
			return nil
		}
	}
	f.agents = append(f.agents, row)
	return nil
}

func (f *fakeStore) ListL4Rules(_ context.Context, agentID string) ([]storage.L4RuleRow, error) {
	return append([]storage.L4RuleRow(nil), f.l4RulesByID[agentID]...), nil
}

func (f *fakeStore) ListVersionPolicies(context.Context) ([]storage.VersionPolicyRow, error) {
	return nil, nil
}

func (f *fakeStore) SaveL4Rules(_ context.Context, agentID string, rows []storage.L4RuleRow) error {
	if f.l4RulesByID == nil {
		f.l4RulesByID = map[string][]storage.L4RuleRow{}
	}
	f.l4RulesByID[agentID] = append([]storage.L4RuleRow(nil), rows...)
	return nil
}

func (f *fakeStore) SaveVersionPolicies(context.Context, []storage.VersionPolicyRow) error {
	return nil
}

func (f *fakeStore) ListRelayListeners(_ context.Context, agentID string) ([]storage.RelayListenerRow, error) {
	return append([]storage.RelayListenerRow(nil), f.relayByID[agentID]...), nil
}

func (f *fakeStore) ListManagedCertificates(context.Context) ([]storage.ManagedCertificateRow, error) {
	return append([]storage.ManagedCertificateRow(nil), f.managedCerts...), nil
}

func (f *fakeStore) SaveRelayListeners(_ context.Context, agentID string, rows []storage.RelayListenerRow) error {
	if f.relayByID == nil {
		f.relayByID = map[string][]storage.RelayListenerRow{}
	}
	f.relayByID[agentID] = append([]storage.RelayListenerRow(nil), rows...)
	return nil
}

func (f *fakeStore) SaveManagedCertificates(_ context.Context, rows []storage.ManagedCertificateRow) error {
	f.managedCerts = append([]storage.ManagedCertificateRow(nil), rows...)
	return nil
}

func (f *fakeStore) SaveTrafficEvent(_ context.Context, row storage.AgentTrafficEventRow) error {
	f.events = append(f.events, row)
	return nil
}

func (f *fakeStore) LoadManagedCertificateMaterial(context.Context, string) (storage.ManagedCertificateBundle, bool, error) {
	return storage.ManagedCertificateBundle{}, false, nil
}

func (f *fakeStore) SaveManagedCertificateMaterial(context.Context, string, storage.ManagedCertificateBundle) error {
	return nil
}

func (f *fakeStore) CleanupManagedCertificateMaterial(context.Context, []storage.ManagedCertificateRow, []storage.ManagedCertificateRow) error {
	return nil
}

func (f *fakeStore) LoadAgentSnapshot(_ context.Context, agentID string, input storage.AgentSnapshotInput) (storage.Snapshot, error) {
	f.loadSnapshotCalls++
	f.lastSnapshotAgentID = agentID
	f.lastSnapshotInput = input
	return f.snapshot, nil
}

func (f *fakeStore) LoadLocalSnapshot(context.Context, string) (storage.Snapshot, error) {
	return f.localSnapshot, nil
}

func (f *fakeStore) SaveHTTPRules(_ context.Context, agentID string, rows []storage.HTTPRuleRow) error {
	if f.rulesByID == nil {
		f.rulesByID = map[string][]storage.HTTPRuleRow{}
	}
	f.rulesByID[agentID] = append([]storage.HTTPRuleRow(nil), rows...)
	return nil
}

func (f *fakeStore) SaveLocalRuntimeState(_ context.Context, agentID string, state storage.RuntimeState) error {
	f.savedRuntimeAgentID = agentID
	f.savedRuntimeState = state
	f.saveRuntimeCalls++
	return nil
}

func (f *fakeStore) DeleteAgent(_ context.Context, agentID string) error {
	f.deletedAgentID = agentID
	next := make([]storage.AgentRow, 0, len(f.agents))
	for _, row := range f.agents {
		if row.ID != agentID {
			next = append(next, row)
		}
	}
	f.agents = next
	return nil
}

func TestAgentServiceRegisterNormalizesURLAndDeduplicatesByURL(t *testing.T) {
	t.Parallel()
	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID:               "edge-existing",
			Name:             "Edge Existing",
			AgentURL:         "https://edge.example.com",
			AgentToken:       "token-old",
			CapabilitiesJSON: `["http_rules"]`,
			TagsJSON:         `["old"]`,
			Mode:             "master",
		}},
	}
	svc := NewAgentService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	agent, err := svc.Register(context.Background(), RegisterRequest{
		Name:         "Edge New",
		AgentURL:     " https://edge.example.com/ ",
		AgentToken:   "token-new",
		Tags:         []string{" edge ", "edge", "", "blue"},
		Capabilities: []string{"http_rules", "l4", "bad", "l4"},
	}, "")
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	if agent.ID != "edge-existing" {
		t.Fatalf("Register() reused wrong row: %+v", agent)
	}
	if store.savedAgent.AgentURL != "https://edge.example.com" {
		t.Fatalf("saved AgentURL = %q", store.savedAgent.AgentURL)
	}
	if store.savedAgent.Mode != "master" {
		t.Fatalf("saved Mode = %q", store.savedAgent.Mode)
	}
	if store.savedAgent.TagsJSON != `["edge","blue"]` {
		t.Fatalf("saved TagsJSON = %q", store.savedAgent.TagsJSON)
	}
	if store.savedAgent.CapabilitiesJSON != `["http_rules","l4"]` {
		t.Fatalf("saved CapabilitiesJSON = %q", store.savedAgent.CapabilitiesJSON)
	}
}

func TestAgentServiceRegisterFailsClosedWhenControlTokenEntropyFails(t *testing.T) {
	previousRandom := agentControlTokenRandom
	agentControlTokenRandom = strings.NewReader("")
	t.Cleanup(func() { agentControlTokenRandom = previousRandom })
	store := &fakeStore{}
	svc := NewAgentService(config.Config{}, store)
	if _, err := svc.Register(context.Background(), RegisterRequest{
		Name: "PKI edge", TunnelCSRPEM: "PUBLIC CSR",
	}, ""); err == nil || !strings.Contains(err.Error(), "generate agent control token") {
		t.Fatalf("Register(entropy failure) error = %v", err)
	}
	if store.savedAgentCalls != 0 || len(store.agents) != 0 {
		t.Fatalf("entropy failure persisted an agent: calls=%d rows=%+v", store.savedAgentCalls, store.agents)
	}
}

func TestAgentServiceHeartbeatDegradesPKIOnInternalEnrollmentFailure(t *testing.T) {
	t.Parallel()
	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID: "remote-pki-degraded", Name: "remote-pki-degraded", AgentToken: "token-pki-degraded",
			DesiredRevision: 2, CurrentRevision: 1, LastApplyStatus: "success",
		}},
		snapshot: storage.Snapshot{
			Revision: 2,
			Rules: []storage.HTTPRule{{
				ID: 9, FrontendURL: "https://ordinary.example.test",
				Backends: []storage.HTTPBackend{{URL: "http://127.0.0.1:8096"}},
			}},
			RelayListeners: []storage.RelayListener{{
				ID: 42, AgentID: "remote-pki-degraded", ListenHost: "0.0.0.0", ListenPort: 7443,
			}},
		},
	}
	svc := NewAgentService(config.Config{}, store)
	svc.SetPKIController(agentPKIControlErrorStub{
		err: fmt.Errorf("%w: persisted enrollment replay is invalid", ErrPKIEnrollmentRequest),
	})

	reply, err := svc.Heartbeat(t.Context(), HeartbeatRequest{
		CurrentRevision: 1, LastApplyStatus: "success",
	}, "token-pki-degraded")
	if err != nil {
		t.Fatalf("Heartbeat(internal PKI failure) error = %v", err)
	}
	if reply.PKIStatus == nil || reply.PKIStatus.Status != "degraded" || reply.PKIStatus.Code != "runtime_unavailable" {
		t.Fatalf("Heartbeat(internal PKI failure) PKIStatus = %+v", reply.PKIStatus)
	}
	if reply.RelayListeners == nil || len(reply.RelayListeners) != 0 {
		t.Fatalf("Heartbeat(internal PKI failure) relay listeners = %+v, want fail-closed", reply.RelayListeners)
	}
	if !reply.HasUpdate || len(reply.Rules) != 1 {
		t.Fatalf("Heartbeat(internal PKI failure) ordinary control payload = %+v", reply)
	}
}

func TestAgentServiceHeartbeatReturnsFullSnapshotSyncPayload(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.April, 11, 8, 30, 0, 0, time.UTC)
	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID:              "remote-a",
			Name:            "remote-a",
			AgentToken:      "token-remote-a",
			DesiredVersion:  "2.0.0",
			DesiredRevision: 2,
			CurrentRevision: 1,
			LastApplyStatus: "success",
		}},
		snapshot: storage.Snapshot{
			DesiredVersion: "2.0.0",
			Revision:       8,
			PluginPolicies: []storage.PluginPolicy{{
				ID: "waf-main", Revision: 7,
				Stages: []storage.PolicyStage{{
					Kind: "waf", PolicyID: "waf-main", PluginID: "official.waf", PluginVersion: "1.0.0",
					InstanceID: "waf-main", PackageDigest: strings.Repeat("a", 64), ArtifactPath: "packages/signer/digest/policy.wasm",
					ArtifactDigest: strings.Repeat("b", 64), SignatureVerified: true, SignerKeyID: "official", SignerFingerprint: strings.Repeat("c", 64),
					ABI: "nre:policy/v1", ExtensionPoints: []string{"http.request"}, GrantedScopes: []string{"http.inspect"},
					Config: json.RawMessage(`{"mode":"block"}`), ResourceBudget: storage.PolicyResourceBudget{TimeoutMS: 2, MemoryBytes: 1 << 20, Concurrency: 1, InputBytes: 4096, OutputBytes: 4096},
					FailurePolicy: storage.PolicyFailurePolicy{OnError: "fail-closed", OnBudget: "fail-closed", Restart: "never", CoreFallback: "preserve"},
				}},
			}},
			VersionPackage: &storage.VersionPackage{
				Platform: "linux-amd64",
				URL:      "https://example.com/agent-linux.tar.gz",
				SHA256:   "sha-linux",
				Filename: "agent-linux.tar.gz",
				Size:     123,
			},
			Rules: []storage.HTTPRule{{
				ID:          9,
				FrontendURL: "https://edge.example.com",
				Backends:    []storage.HTTPBackend{{URL: "http://127.0.0.1:8096"}},
				RelayLayers: [][]int{{11, 22}},
				Revision:    6,
			}},
			L4Rules: []storage.L4Rule{{
				ID:         2,
				Protocol:   "tcp",
				ListenHost: "0.0.0.0",
				ListenPort: 9000,
				Backends:   []storage.L4Backend{{Host: "127.0.0.1", Port: 9001}},
				Revision:   6,
			}},
			RelayListeners: []storage.RelayListener{{
				ID:            11,
				AgentID:       "remote-a",
				Name:          "relay-a",
				ListenHost:    "0.0.0.0",
				ListenPort:    7443,
				TransportMode: "quic",
				Revision:      4,
			}},
			Certificates: []storage.ManagedCertificateBundle{{
				ID:       21,
				Domain:   "__relay-ca.internal",
				Revision: 7,
				CertPEM:  "CERT",
				KeyPEM:   "KEY",
			}},
			CertificatePolicies: []storage.ManagedCertificatePolicy{{
				ID:              21,
				Domain:          "__relay-ca.internal",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "local_http01",
				Status:          "active",
				Revision:        7,
				Usage:           "relay_ca",
				CertificateType: "internal_ca",
			}},
		},
	}
	store.snapshot.AgentConfig.TrafficStatsEnabled = heartbeatBoolPtr(true)
	_, expectedSnapshotDigest, err := revision.CanonicalSnapshotPayload(store.snapshot)
	if err != nil {
		t.Fatal(err)
	}
	store.revisions = map[string]storage.AgentRevisionRow{"remote-a/8": {AgentID: "remote-a", Revision: 8, SnapshotDigest: expectedSnapshotDigest}}

	svc := NewAgentService(config.Config{}, store)
	svc.now = func() time.Time { return now }

	reply, err := svc.Heartbeat(context.Background(), HeartbeatRequest{
		CurrentRevision:  1,
		Version:          "1.4.0",
		Platform:         "linux-amd64",
		AgentURL:         "http://remote-a:8080",
		HasAgentURL:      true,
		Tags:             []string{"edge"},
		HasTags:          true,
		Capabilities:     []string{"http_rules", "l4"},
		HasCapabilities:  true,
		LastApplyStatus:  "success",
		LastApplyMessage: "",
		PluginStatuses: []storage.PluginRuntimeStatus{{
			InstanceID: "instance", PluginID: "plugin", OperationID: "operation", Revision: 8,
			GenerationID: strings.Repeat("d", 64), PackageDigest: strings.Repeat("e", 64), ArtifactDigest: strings.Repeat("f", 64),
			State: "degraded", Sequence: 2, SafeDetail: "restart backoff",
		}},
	}, "token-remote-a")
	if err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}
	if len(store.pluginReports) != 1 || store.pluginReports[0].AgentID != "remote-a" || store.pluginReports[0].SafeDetail != "restart backoff" {
		t.Fatalf("authenticated plugin runtime reports = %+v", store.pluginReports)
	}

	if !reply.HasUpdate {
		t.Fatalf("HasUpdate = false, want true")
	}
	if reply.DesiredRevision != 8 {
		t.Fatalf("DesiredRevision = %d", reply.DesiredRevision)
	}
	if reply.SnapshotDigest != expectedSnapshotDigest {
		t.Fatalf("SnapshotDigest = %q", reply.SnapshotDigest)
	}
	if store.ensureRevisionCalls != 1 {
		t.Fatalf("durable heartbeat revision issue calls = %d", store.ensureRevisionCalls)
	}
	if reply.DesiredVersion != "2.0.0" {
		t.Fatalf("DesiredVersion = %q", reply.DesiredVersion)
	}
	if reply.VersionPackage != "https://example.com/agent-linux.tar.gz" || reply.VersionSHA256 != "sha-linux" {
		t.Fatalf("version package fields = %q / %q", reply.VersionPackage, reply.VersionSHA256)
	}
	if reply.VersionPackageMeta == nil || reply.VersionPackageMeta.Platform != "linux-amd64" {
		t.Fatalf("VersionPackageMeta = %+v", reply.VersionPackageMeta)
	}
	if len(reply.Rules) != 1 || len(reply.L4Rules) != 1 || len(reply.RelayListeners) != 1 {
		t.Fatalf("sync arrays = %+v", reply)
	}
	if len(reply.Certificates) != 1 || len(reply.CertificatePolicies) != 1 {
		t.Fatalf("cert sync arrays = %+v", reply)
	}
	if len(reply.PluginPolicies) != 1 || reply.PluginPolicies[0].ID != "waf-main" || !reply.PluginPolicies[0].Stages[0].SignatureVerified {
		t.Fatalf("plugin policy sync payload = %+v", reply.PluginPolicies)
	}
	if store.loadSnapshotCalls != 1 || store.lastSnapshotAgentID != "remote-a" {
		t.Fatalf("LoadAgentSnapshot() calls = %d, agent_id = %q", store.loadSnapshotCalls, store.lastSnapshotAgentID)
	}
	if store.lastSnapshotInput.Platform != "linux-amd64" || store.lastSnapshotInput.DesiredVersion != "2.0.0" {
		t.Fatalf("snapshot input = %+v", store.lastSnapshotInput)
	}
	if store.savedAgentCalls != 1 {
		t.Fatalf("SaveAgent() calls = %d", store.savedAgentCalls)
	}
	if store.savedAgent.Version != "1.4.0" || store.savedAgent.Platform != "linux-amd64" || store.savedAgent.CurrentRevision != 1 {
		t.Fatalf("saved agent metadata = %+v", store.savedAgent)
	}
	if store.savedAgent.LastSeenAt != now.Format(time.RFC3339) {
		t.Fatalf("LastSeenAt = %q", store.savedAgent.LastSeenAt)
	}
}

func TestHeartbeatPersistsPreparedPKISnapshotBeforeReply(t *testing.T) {
	t.Parallel()
	store := &fakeStore{
		agents:   []storage.AgentRow{{ID: "edge-pki", Name: "edge-pki", AgentToken: "token-edge-pki", DesiredRevision: 5, CurrentRevision: 4, LastApplyStatus: "success"}},
		snapshot: storage.Snapshot{Revision: 5, PluginPolicies: []storage.PluginPolicy{}, RelayListeners: []storage.RelayListener{{ID: 1, AgentID: "edge-pki", ListenPort: 7443, TransportMode: "tcp"}}},
	}
	svc := NewAgentService(config.Config{}, store)
	var pkiCalls []string
	svc.SetPKIController(heartbeatPKIProjectionStub{calls: &pkiCalls})
	reply, err := svc.Heartbeat(t.Context(), HeartbeatRequest{CurrentRevision: 4, LastApplyStatus: "success"}, "token-edge-pki")
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.RelayListeners) != 1 || reply.RelayListeners[0].ListenPort != 9443 || reply.RelayListeners[0].TransportMode != "quic" {
		t.Fatalf("wire relay projection = %+v", reply.RelayListeners)
	}
	if len(store.issuedSnapshot.RelayListeners) != 1 || store.issuedSnapshot.RelayListeners[0].ListenPort != 7443 || store.issuedSnapshot.RelayListeners[0].TransportMode != "tcp" {
		t.Fatalf("issued immutable relay projection = %+v", store.issuedSnapshot.RelayListeners)
	}
	var durable storage.Snapshot
	if err := json.Unmarshal(store.issuedPayload, &durable); err != nil {
		t.Fatal(err)
	}
	if len(durable.RelayListeners) != 1 || durable.RelayListeners[0].ListenPort != 7443 || durable.RelayListeners[0].TransportMode != "tcp" {
		t.Fatalf("durable immutable relay projection = %+v", durable.RelayListeners)
	}
	if strings.Join(pkiCalls, ",") != "control,prepare" {
		t.Fatalf("PKI projection order = %v, want control then prepare", pkiCalls)
	}
	if reply.PKISecurity == nil {
		t.Fatal("wire PKI security is missing")
	}
	for label, got := range map[string]*storage.PKISecuritySnapshot{"wire": reply.PKISecurity} {
		if got.PKIDomainID != "pki-domain" || got.PKIEpoch != 3 || got.SecurityRevision != 7 || !got.Full ||
			!got.IssuedAt.Equal(time.Date(2026, time.April, 11, 8, 0, 0, 0, time.UTC)) || got.SignerGeneration != 2 ||
			len(got.TrustRoots) != 1 || got.TrustRoots[0].AuthorityID != "ca-1" || got.TrustRoots[0].Generation != 2 || got.TrustRoots[0].CertificatePEM != "CA" ||
			len(got.RevokedIdentityIDs) != 1 || got.RevokedIdentityIDs[0] != "identity-old" || len(got.RevokedSerials) != 1 || got.RevokedSerials[0] != "serial-old" ||
			string(got.Signature) != "signature" {
			t.Fatalf("%s PKI security projection = %+v", label, got)
		}
	}
	if store.issuedSnapshot.PKISecurity != nil || durable.PKISecurity != nil {
		t.Fatalf("dynamic PKI leaked into immutable snapshots: %+v / %+v", store.issuedSnapshot.PKISecurity, durable.PKISecurity)
	}
	if reply.TrafficStatsEnabled == nil || !*reply.TrafficStatsEnabled ||
		store.issuedSnapshot.AgentConfig.TrafficStatsEnabled != nil || durable.AgentConfig.TrafficStatsEnabled != nil {
		t.Fatalf("dynamic traffic projection leaked into immutable snapshots: reply=%v issued=%+v durable=%+v", reply.TrafficStatsEnabled, store.issuedSnapshot.AgentConfig, durable.AgentConfig)
	}
	payloadSum := sha256.Sum256(store.issuedPayload)
	if reply.SnapshotDigest != hex.EncodeToString(payloadSum[:]) || reply.SnapshotDigest != store.issuedDigest {
		t.Fatalf("wire/durable snapshot digest = %q / %q", reply.SnapshotDigest, store.issuedDigest)
	}
}

func TestAgentServiceConcurrentHeartbeatsMergeMasterCertificateReports(t *testing.T) {
	t.Parallel()

	store := newConcurrentManagedCertificateReportStore(storage.ManagedCertificateRow{
		ID:              81,
		Domain:          "merge.example.com",
		Enabled:         true,
		Scope:           "domain",
		IssuerMode:      "master_cf_dns",
		TargetAgentIDs:  `["edge-a","edge-b"]`,
		Status:          "pending",
		AgentReports:    `{}`,
		Usage:           "https",
		CertificateType: "acme",
		Revision:        9,
	})
	svc := NewAgentService(config.Config{}, store)
	svc.now = func() time.Time { return time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC) }

	start := make(chan struct{})
	ready := sync.WaitGroup{}
	ready.Add(2)
	errorsByAgent := make(chan error, 2)
	for _, agentID := range []string{"edge-a", "edge-b"} {
		agentID := agentID
		go func() {
			ready.Done()
			<-start
			errorsByAgent <- svc.reconcileManagedCertificatesFromHeartbeat(context.Background(), storage.AgentRow{ID: agentID}, HeartbeatRequest{
				ManagedCertificateReports: []ManagedCertificateHeartbeatReport{{
					ID:           81,
					Domain:       "merge.example.com",
					Status:       "active",
					LastIssueAt:  "2026-07-26T09:00:00Z",
					MaterialHash: "pending-material-hash",
				}},
			})
		}()
	}
	ready.Wait()
	close(start)
	for range 2 {
		if err := <-errorsByAgent; err != nil {
			t.Fatalf("reconcileManagedCertificatesFromHeartbeat() error = %v", err)
		}
	}

	rows := store.snapshot()
	if len(rows) != 1 {
		t.Fatalf("managed certificate rows = %+v", rows)
	}
	reports := managedCertificateFromRow(rows[0]).AgentReports
	if _, ok := reports["edge-a"]; !ok {
		t.Fatalf("edge-a report was lost: %+v", reports)
	}
	if _, ok := reports["edge-b"]; !ok {
		t.Fatalf("edge-b report was lost: %+v", reports)
	}
}

type concurrentManagedCertificateReportStore struct {
	*fakeStore
	mu         sync.Mutex
	rows       []storage.ManagedCertificateRow
	listCalls  int
	bothListed chan struct{}
}

func newConcurrentManagedCertificateReportStore(row storage.ManagedCertificateRow) *concurrentManagedCertificateReportStore {
	return &concurrentManagedCertificateReportStore{
		fakeStore:  &fakeStore{},
		rows:       []storage.ManagedCertificateRow{row},
		bothListed: make(chan struct{}),
	}
}

func (s *concurrentManagedCertificateReportStore) ListManagedCertificates(ctx context.Context) ([]storage.ManagedCertificateRow, error) {
	s.mu.Lock()
	rows := append([]storage.ManagedCertificateRow(nil), s.rows...)
	s.listCalls++
	if s.listCalls == 2 {
		close(s.bothListed)
	}
	bothListed := s.bothListed
	s.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-bothListed:
		return rows, nil
	}
}

func (s *concurrentManagedCertificateReportStore) SaveManagedCertificates(_ context.Context, rows []storage.ManagedCertificateRow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows = append([]storage.ManagedCertificateRow(nil), rows...)
	return nil
}

func (s *concurrentManagedCertificateReportStore) UpdateManagedCertificates(_ context.Context, update func([]storage.ManagedCertificateRow) ([]storage.ManagedCertificateRow, bool, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next, changed, err := update(append([]storage.ManagedCertificateRow(nil), s.rows...))
	if err != nil {
		return err
	}
	if changed {
		s.rows = append([]storage.ManagedCertificateRow(nil), next...)
	}
	return nil
}

func (s *concurrentManagedCertificateReportStore) snapshot() []storage.ManagedCertificateRow {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]storage.ManagedCertificateRow(nil), s.rows...)
}

func TestHeartbeatIngestsTrafficWhenModuleEnabled(t *testing.T) {
	t.Parallel()
	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID:              "remote-traffic",
			Name:            "remote-traffic",
			AgentToken:      "token-remote-traffic",
			DesiredRevision: 2,
			CurrentRevision: 1,
			LastApplyStatus: "success",
		}},
		snapshot: storage.Snapshot{DesiredVersion: "3.0.0", Revision: 2},
	}
	trafficSvc := &fakeHeartbeatTrafficService{}
	svc := NewAgentService(config.Config{TrafficStatsEnabled: true}, store)
	svc.SetTrafficService(trafficSvc)

	reply, err := svc.Heartbeat(context.Background(), HeartbeatRequest{
		CurrentRevision: 1,
		Stats: AgentStats{
			"status": "运行中",
			"traffic": map[string]any{
				"total": map[string]any{"rx_bytes": float64(123), "tx_bytes": float64(456)},
			},
		},
	}, "token-remote-traffic")
	if err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}

	if got := len(trafficSvc.ingestCalls); got != 1 {
		t.Fatalf("traffic ingest calls = %d, want 1", got)
	}
	if trafficSvc.ingestCalls[0].agentID != "remote-traffic" {
		t.Fatalf("traffic ingest agentID = %q", trafficSvc.ingestCalls[0].agentID)
	}
	if _, ok := trafficSvc.ingestCalls[0].stats["traffic"]; !ok {
		t.Fatalf("traffic ingest stats = %+v, want original traffic payload", trafficSvc.ingestCalls[0].stats)
	}
	stats := parseAgentStats(store.savedAgent.LastReportedStatsJSON)
	if _, ok := stats["traffic"]; ok {
		t.Fatalf("LastReportedStatsJSON = %q, want traffic omitted", store.savedAgent.LastReportedStatsJSON)
	}
	if stats["status"] != "运行中" {
		t.Fatalf("LastReportedStatsJSON = %q, want non-traffic stats persisted", store.savedAgent.LastReportedStatsJSON)
	}
	if store.savedHeartbeatCalls != 1 {
		t.Fatalf("SaveAgentHeartbeat calls = %d, want 1", store.savedHeartbeatCalls)
	}
	if store.savedAgentCalls != 0 {
		t.Fatalf("SaveAgent calls = %d, want 0", store.savedAgentCalls)
	}
	if reply.TrafficStatsEnabled == nil || !*reply.TrafficStatsEnabled {
		t.Fatalf("TrafficStatsEnabled = %v, want true", reply.TrafficStatsEnabled)
	}
}

func TestAgentServiceDeleteRejectsReferencedRelayListenerAndCleansUpRemoteAgent(t *testing.T) {
	t.Parallel()
	cfg := config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}
	store := &fakeStore{
		agents: []storage.AgentRow{
			{ID: "edge-a", Name: "edge-a", AgentToken: "token-a"},
			{ID: "edge-b", Name: "edge-b", AgentToken: "token-b"},
		},
		relayByID: map[string][]storage.RelayListenerRow{
			"edge-a": {{
				ID:      7,
				AgentID: "edge-a",
				Name:    "relay-a",
			}},
		},
		rulesByID: map[string][]storage.HTTPRuleRow{
			"edge-a": {{ID: 1, AgentID: "edge-a"}},
			"edge-b": {{
				ID:              9,
				AgentID:         "edge-b",
				FrontendURL:     "https://relay.example.com",
				RelayChainJSON:  `[8]`,
				RelayLayersJSON: `[[7]]`,
			}},
		},
		l4RulesByID: map[string][]storage.L4RuleRow{
			"edge-a": {{ID: 2, AgentID: "edge-a"}},
		},
	}
	svc := NewAgentService(cfg, store)

	_, err := svc.Delete(context.Background(), "edge-a")
	if err == nil || err.Error() != "invalid argument: cannot delete agent edge-a: relay listener 7 is referenced by HTTP rule #9 on agent edge-b" {
		t.Fatalf("Delete() error = %v", err)
	}

	delete(store.rulesByID, "edge-b")
	deleted, err := svc.Delete(context.Background(), "edge-a")
	if err != nil {
		t.Fatalf("Delete() second call error = %v", err)
	}

	if deleted.ID != "edge-a" {
		t.Fatalf("deleted agent = %+v", deleted)
	}
	if store.deletedAgentID != "edge-a" {
		t.Fatalf("DeleteAgent() called with %q", store.deletedAgentID)
	}
	if len(store.rulesByID["edge-a"]) != 0 || len(store.l4RulesByID["edge-a"]) != 0 || len(store.relayByID["edge-a"]) != 0 {
		t.Fatalf("agent resources not cleaned up: rules=%+v l4=%+v relay=%+v", store.rulesByID["edge-a"], store.l4RulesByID["edge-a"], store.relayByID["edge-a"])
	}
}

func TestAgentServiceDeleteCleansUpManagedCertificates(t *testing.T) {
	t.Parallel()
	cfg := config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}
	store := &fakeStore{
		agents: []storage.AgentRow{
			{ID: "edge-a", Name: "edge-a", AgentToken: "token-a"},
			{ID: "edge-b", Name: "edge-b", AgentToken: "token-b"},
		},
		managedCerts: []storage.ManagedCertificateRow{
			{ID: 1, Domain: "shared.example.com", TargetAgentIDs: `["edge-a","edge-b"]`},
			{ID: 2, Domain: "orphan.example.com", TargetAgentIDs: `["edge-a"]`},
		},
	}
	svc := NewAgentService(cfg, store)

	deleted, err := svc.Delete(context.Background(), "edge-a")
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if deleted.ID != "edge-a" {
		t.Fatalf("deleted agent = %+v", deleted)
	}
	if store.deletedAgentID != "edge-a" {
		t.Fatalf("DeleteAgent() called with %q", store.deletedAgentID)
	}

	if len(store.managedCerts) != 1 {
		t.Fatalf("expected 1 remaining cert, got %d", len(store.managedCerts))
	}
	remaining := store.managedCerts[0]
	if remaining.ID != 1 {
		t.Fatalf("expected remaining cert ID 1, got %d", remaining.ID)
	}
	if remaining.TargetAgentIDs != `["edge-b"]` {
		t.Fatalf("expected shared cert to drop edge-a, got %q", remaining.TargetAgentIDs)
	}
}

// fakeDDNSReconciler records ReconcileAfterHeartbeat invocations and can be
// configured to panic, exercising the fire-and-forget contract on the heartbeat
// main path.
type fakeDDNSReconciler struct {
	calledIDs []string
	panicOn   bool
}

func (f *fakeDDNSReconciler) ReconcileAfterHeartbeat(_ context.Context, agentID string) {
	f.calledIDs = append(f.calledIDs, agentID)
	if f.panicOn {
		panic("simulated ddns reconciler failure")
	}
}

// TestAgentSummaryJSONCarriesNoCredential verifies the AgentSummary wire shape
// that redactAgentSummary operates on never exposes a token/secret key — the
// precondition that lets the handler redact only the proxy password (R7). The
// full dispatched ddns_config is exposed so the edit form can round-trip family
// state; it must carry only domain + per-family {enabled,source,interface}.
func TestAgentServiceApplyRetriesCurrentDesiredForLocalAndRemoteWithoutSynchronousTrigger(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name    string
		agentID string
		local   bool
	}{
		{name: "local", agentID: "local", local: true},
		{name: "remote", agentID: "edge-retry"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := t.Context()
			store, err := newServiceTestSQLiteStore(t, t.TempDir(), "local")
			if err != nil {
				t.Fatalf("NewSQLiteStore() error = %v", err)
			}
			t.Cleanup(func() { _ = store.Close() })
			if !testCase.local {
				if err := store.SaveAgent(ctx, storage.AgentRow{
					ID: testCase.agentID, Name: "Edge Retry", AgentToken: "token-retry",
					CapabilitiesJSON: `["http_rules"]`, DesiredRevision: 4, CurrentRevision: 3,
					LastApplyRevision: 3, LastApplyStatus: "error",
				}); err != nil {
					t.Fatalf("SaveAgent() error = %v", err)
				}
			}
			snapshot := storage.Snapshot{
				Revision: 4, Rules: []storage.HTTPRule{}, L4Rules: []storage.L4Rule{},
				RelayListeners: []storage.RelayListener{},
				EgressProfiles: []storage.EgressProfile{}, Certificates: []storage.ManagedCertificateBundle{},
				CertificatePolicies: []storage.ManagedCertificatePolicy{},
			}
			payload, digest, err := revision.CanonicalSnapshotPayload(snapshot)
			if err != nil {
				t.Fatalf("CanonicalSnapshotPayload() error = %v", err)
			}
			now := time.Date(2026, 7, 12, 22, 0, 0, 0, time.UTC)
			operationID := "apply-retry-" + testCase.name
			artifactID := "snapshot-" + digest
			if err := store.CreateRevisionLedger(ctx, storage.RevisionLedgerWrite{
				Operation: storage.OperationRow{ID: operationID, Kind: "test.seed", Status: storage.OperationStatusPending, PrimaryAgentID: testCase.agentID, CreatedAt: now, UpdatedAt: now},
				Revisions: []storage.AgentRevisionRow{{AgentID: testCase.agentID, Revision: 4, OperationID: operationID, State: storage.AgentRevisionStateFailed, SnapshotArtifactID: artifactID, SnapshotDigest: digest, AttemptCount: 5, CreatedAt: now, UpdatedAt: now}},
				Pointers:  []storage.AgentRevisionPointerRow{{AgentID: testCase.agentID, DesiredRevision: 4, AppliedRevision: 3, LastKnownGoodRevision: 3, UpdatedAt: now}},
				Artifacts: []storage.GenerationArtifactRow{{ID: artifactID, Kind: "agent_snapshot", SHA256: digest, Payload: payload, SizeBytes: int64(len(payload)), CreatedAt: now}},
			}); err != nil {
				t.Fatalf("CreateRevisionLedger() error = %v", err)
			}

			svc := NewAgentService(config.Config{EnableLocalAgent: true, LocalAgentID: "local", LocalAgentName: "Local"}, store)
			triggerCalls := 0
			svc.SetLocalApplyTrigger(func(context.Context) error {
				triggerCalls++
				return errors.New("synchronous trigger must not run")
			})
			result, err := svc.Apply(ctx, testCase.agentID)
			if err != nil {
				t.Fatalf("Apply() error = %v", err)
			}
			if !result.Pending || result.DesiredRevision != 4 {
				t.Fatalf("Apply() = %+v, want asynchronous pending retry", result)
			}
			if triggerCalls != 0 {
				t.Fatalf("triggerCalls = %d, want 0", triggerCalls)
			}
			retried, found, err := store.GetCoordinatorRevision(ctx, testCase.agentID, 4)
			if err != nil || !found {
				t.Fatalf("GetCoordinatorRevision() found=%v error=%v", found, err)
			}
			if retried.State != storage.AgentRevisionStatePending || retried.RetryCycle != 1 || retried.AttemptCount != 0 {
				t.Fatalf("retried revision = %+v", retried)
			}
		})
	}
}
