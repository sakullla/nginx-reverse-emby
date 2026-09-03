//go:build !integration

package service

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/marketplace"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestOfficialWAFPolicyRefFromCatalogDefaultsObserveAndPreservesExisting(t *testing.T) {
	t.Parallel()
	policies := []storage.PluginPolicy{
		{ID: "ip-chain", Stages: []storage.PolicyStage{{Kind: "ip", ExtensionPoints: []string{policyExtensionHTTP, policyExtensionL4}}}},
		{ID: "official.waf-default", Stages: []storage.PolicyStage{{Kind: "waf", ExtensionPoints: []string{policyExtensionHTTP}}}},
	}
	got := defaultOfficialWAFPolicyRef(policies, nil)
	if got == nil || got.ID != "official.waf-default" || string(got.Overlay) != `{"mode":"observe"}` {
		t.Fatalf("default ref = %+v", got)
	}
	existing := &storage.PolicyRef{ID: "custom-chain", Overlay: json.RawMessage(`{"mode":"deny"}`)}
	kept := defaultOfficialWAFPolicyRef(policies, existing)
	if kept == nil || kept.ID != "custom-chain" || string(kept.Overlay) != `{"mode":"deny"}` {
		t.Fatalf("existing ref overwritten: %+v", kept)
	}
}

func TestAttachOfficialWAFPolicyRefJSONDoesNotOverwriteNonEmpty(t *testing.T) {
	t.Parallel()
	existing := `{"id":"custom-chain"}`
	got, changed := attachOfficialWAFPolicyRefJSON(existing, "official.waf-default")
	if changed || got != existing {
		t.Fatalf("non-empty ref changed: %q", got)
	}
	attached, changed := attachOfficialWAFPolicyRefJSON("", "official.waf-default")
	if !changed {
		t.Fatal("empty ref was not attached")
	}
	ref := parseRulePolicyRef(attached)
	if ref == nil || ref.ID != "official.waf-default" || string(ref.Overlay) != `{"mode":"observe"}` {
		t.Fatalf("attached ref = %s", attached)
	}
}

func TestDetachOfficialWAFPolicyRefJSONClearsMatchingIDs(t *testing.T) {
	t.Parallel()
	ids := map[string]struct{}{"official.waf-default": {}}
	cleared, changed := detachOfficialWAFPolicyRefJSON(`{"id":"official.waf-default","overlay":{"mode":"deny"}}`, ids)
	if !changed || cleared != "" {
		t.Fatalf("matching ref not detached: %q changed=%v", cleared, changed)
	}
	kept, changed := detachOfficialWAFPolicyRefJSON(`{"id":"custom-chain"}`, ids)
	if changed || kept != `{"id":"custom-chain"}` {
		t.Fatalf("foreign ref detached: %q", kept)
	}
}

func TestValidateWAFPolicyOverlay(t *testing.T) {
	t.Parallel()
	for _, overlay := range []json.RawMessage{nil, json.RawMessage(""), json.RawMessage("null"), json.RawMessage(`{"mode":"observe"}`), json.RawMessage(`{"mode":"deny"}`)} {
		if err := validateWAFPolicyOverlay(overlay); err != nil {
			t.Fatalf("valid overlay %s error = %v", overlay, err)
		}
	}
	for _, overlay := range []json.RawMessage{
		json.RawMessage(`{}`),
		json.RawMessage(`{"mode":"block"}`),
		json.RawMessage(`{"mode":"deny","extra":true}`),
		json.RawMessage(`[]`),
		json.RawMessage(`not-json`),
	} {
		if err := validateWAFPolicyOverlay(overlay); err == nil || !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("invalid overlay %s error = %v", overlay, err)
		}
	}
}

func TestApplyDefaultOfficialWAFPolicyRefUsesCatalogObserve(t *testing.T) {
	t.Parallel()
	store := staticWAFPolicyCatalogStore{policies: []storage.PluginPolicy{{
		ID:     "official.waf-default",
		Stages: []storage.PolicyStage{{Kind: "waf", ExtensionPoints: []string{policyExtensionHTTP}}},
	}}}
	got, err := applyDefaultOfficialWAFPolicyRef(t.Context(), store, "local", nil)
	if err != nil || got == nil || got.ID != "official.waf-default" || string(got.Overlay) != `{"mode":"observe"}` {
		t.Fatalf("default ref = %+v err=%v", got, err)
	}
	existing := &storage.PolicyRef{ID: "custom-chain"}
	kept, err := applyDefaultOfficialWAFPolicyRef(t.Context(), store, "local", existing)
	if err != nil || kept == nil || kept.ID != "custom-chain" {
		t.Fatalf("existing ref overwritten: %+v err=%v", kept, err)
	}
}

func TestValidateRulePolicyReferenceRejectsInvalidWAFOverlay(t *testing.T) {
	t.Parallel()
	store := staticWAFPolicyCatalogStore{policies: []storage.PluginPolicy{{
		ID: "official.waf-default",
		Stages: []storage.PolicyStage{{
			Kind: "waf", ExtensionPoints: []string{policyExtensionHTTP},
			ResourceBudget: storage.PolicyResourceBudget{InputBytes: 65536},
		}},
	}}}
	err := validateRulePolicyReference(t.Context(), store, "local", &storage.PolicyRef{
		ID: "official.waf-default", Overlay: json.RawMessage(`{"mode":"block"}`),
	}, policyExtensionHTTP)
	if err == nil || !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid waf overlay error = %v", err)
	}
	if err := validateRulePolicyReference(t.Context(), store, "local", &storage.PolicyRef{
		ID: "official.waf-default", Overlay: json.RawMessage(`{"mode":"observe"}`),
	}, policyExtensionHTTP); err != nil {
		t.Fatalf("observe overlay error = %v", err)
	}
}

func TestOfficialDualFaceWAFRequiresControlPlaneUIAndAgentPolicy(t *testing.T) {
	t.Parallel()
	dual := plugins.Manifest{Runtime: pluginsdk.Runtime{
		Kind: pluginsdk.RuntimeRPCService, ABI: pluginsdk.RPCABIV1,
		HostScope: pluginsdk.HostScopeControlPlane, PolicyKind: "waf",
		Policy: &pluginsdk.RuntimePolicy{
			Kind: pluginsdk.RuntimeWASMPolicy, ABI: pluginsdk.PolicyABIV1, HostScope: pluginsdk.HostScopeAgent,
			Entry: "artifacts/policy.wasm",
		},
	}}
	if !officialDualFaceWAF(dual) {
		t.Fatal("dual-face waf was not recognized")
	}
	wasmOnly := dual
	wasmOnly.Runtime.Kind = pluginsdk.RuntimeWASMPolicy
	wasmOnly.Runtime.HostScope = pluginsdk.HostScopeAgent
	wasmOnly.Runtime.Policy = nil
	if officialDualFaceWAF(wasmOnly) {
		t.Fatal("wasm-only waf was treated as dual-face")
	}
}

func TestRewriteOfficialWAFHTTPPolicyRefsAttachesAndDetaches(t *testing.T) {
	store := newHTTPWAFAttachStore(t)
	ctx := t.Context()
	if err := store.SaveAgent(ctx, storage.AgentRow{ID: "local", Name: "local"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAgent(ctx, storage.AgentRow{ID: "edge-a", Name: "edge-a"}); err != nil {
		t.Fatal(err)
	}
	empty := testHTTPWAFRuleRow(1, "local", "")
	kept := testHTTPWAFRuleRow(2, "local", `{"id":"custom-chain"}`)
	if err := store.SaveHTTPRules(ctx, "local", []storage.HTTPRuleRow{empty, kept}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveHTTPRules(ctx, "edge-a", []storage.HTTPRuleRow{testHTTPWAFRuleRow(3, "edge-a", "")}); err != nil {
		t.Fatal(err)
	}

	service := NewPluginService(store, t.TempDir())
	service.cfg.LocalAgentID = "local"
	if err := service.rewriteOfficialWAFHTTPPolicyRefs(ctx, "official.waf-default", []string{"official.waf-default"}, true); err != nil {
		t.Fatalf("attach error = %v", err)
	}
	local, err := store.ListHTTPRules(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	attached := httpWAFRuleByID(t, local, 1)
	ref := parseRulePolicyRef(attached.PolicyRefJSON)
	if ref == nil || ref.ID != "official.waf-default" || string(ref.Overlay) != `{"mode":"observe"}` {
		t.Fatalf("attached rule = %+v ref=%s", attached, attached.PolicyRefJSON)
	}
	keptRow := httpWAFRuleByID(t, local, 2)
	if keptRow.PolicyRefJSON != `{"id":"custom-chain"}` {
		t.Fatalf("existing ref overwritten: %+v", keptRow)
	}
	remote, err := store.ListHTTPRules(ctx, "edge-a")
	if err != nil {
		t.Fatal(err)
	}
	remoteRef := parseRulePolicyRef(httpWAFRuleByID(t, remote, 3).PolicyRefJSON)
	if remoteRef == nil || remoteRef.ID != "official.waf-default" {
		t.Fatalf("remote attach = %+v", remote)
	}

	if err := service.rewriteOfficialWAFHTTPPolicyRefs(ctx, "", []string{"official.waf-default"}, false); err != nil {
		t.Fatalf("detach error = %v", err)
	}
	local, err = store.ListHTTPRules(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if parseRulePolicyRef(httpWAFRuleByID(t, local, 1).PolicyRefJSON) != nil {
		t.Fatalf("dangling ref after detach: %+v", local)
	}
	if httpWAFRuleByID(t, local, 2).PolicyRefJSON != `{"id":"custom-chain"}` {
		t.Fatalf("foreign ref detached: %+v", local)
	}
}

func TestRewriteOfficialWAFHTTPPolicyRefsPublishesCoordinatorSnapshots(t *testing.T) {
	store := newHTTPWAFAttachStore(t)
	ctx := t.Context()
	if err := store.SaveAgent(ctx, storage.AgentRow{ID: "local", Name: "local"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAgent(ctx, storage.AgentRow{ID: "edge-a", Name: "edge-a"}); err != nil {
		t.Fatal(err)
	}
	localEmpty := testHTTPWAFRuleRow(1, "local", "")
	localEmpty.FrontendURL = "http://app-a.example"
	localKept := testHTTPWAFRuleRow(2, "local", `{"id":"custom-chain"}`)
	localKept.FrontendURL = "http://app-b.example"
	remoteEmpty := testHTTPWAFRuleRow(3, "edge-a", "")
	remoteEmpty.FrontendURL = "http://app-c.example"
	if err := store.SaveHTTPRules(ctx, "local", []storage.HTTPRuleRow{localEmpty, localKept}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveHTTPRules(ctx, "edge-a", []storage.HTTPRuleRow{remoteEmpty}); err != nil {
		t.Fatal(err)
	}

	service := NewPluginService(store, t.TempDir())
	service.ConfigureRevisionMutations(config.Config{LocalAgentID: "local", EnableLocalAgent: true}, store)
	if err := service.rewriteOfficialWAFHTTPPolicyRefs(ctx, "official.waf-default", []string{"official.waf-default"}, true); err != nil {
		t.Fatalf("attach error = %v", err)
	}

	localSnapshot := latestWAFCoordinatorSnapshot(t, store, "local")
	if localSnapshot.Revision <= 1 {
		t.Fatalf("local attach snapshot revision = %d, want above pre-mutation HTTP revision", localSnapshot.Revision)
	}
	localRule := snapshotHTTPRuleByID(t, localSnapshot, 1)
	if localRule.PolicyRef == nil || localRule.PolicyRef.ID != "official.waf-default" || string(localRule.PolicyRef.Overlay) != `{"mode":"observe"}` {
		t.Fatalf("local snapshot missing observe policy_ref: %+v", localRule.PolicyRef)
	}
	if localRule.Revision != localSnapshot.Revision {
		t.Fatalf("local HTTP revision = %d snapshot = %d", localRule.Revision, localSnapshot.Revision)
	}
	kept := snapshotHTTPRuleByID(t, localSnapshot, 2)
	if kept.PolicyRef == nil || kept.PolicyRef.ID != "custom-chain" {
		t.Fatalf("existing ref overwritten in snapshot: %+v", kept.PolicyRef)
	}

	remoteSnapshot := latestWAFCoordinatorSnapshot(t, store, "edge-a")
	if remoteSnapshot.Revision <= 1 {
		t.Fatalf("remote attach snapshot revision = %d, want above pre-mutation HTTP revision", remoteSnapshot.Revision)
	}
	remoteRule := snapshotHTTPRuleByID(t, remoteSnapshot, 3)
	if remoteRule.PolicyRef == nil || remoteRule.PolicyRef.ID != "official.waf-default" {
		t.Fatalf("remote snapshot missing policy_ref: %+v", remoteRule.PolicyRef)
	}

	attachRevision := localSnapshot.Revision
	if err := service.rewriteOfficialWAFHTTPPolicyRefs(ctx, "", []string{"official.waf-default"}, false); err != nil {
		t.Fatalf("detach error = %v", err)
	}
	detached := latestWAFCoordinatorSnapshot(t, store, "local")
	if detached.Revision <= attachRevision {
		t.Fatalf("detach snapshot revision = %d, want above attach %d", detached.Revision, attachRevision)
	}
	if snapshotHTTPRuleByID(t, detached, 1).PolicyRef != nil {
		t.Fatalf("dangling ref after detach snapshot: %+v", detached.Rules)
	}
	kept = snapshotHTTPRuleByID(t, detached, 2)
	if kept.PolicyRef == nil || kept.PolicyRef.ID != "custom-chain" {
		t.Fatalf("foreign ref detached from snapshot: %+v", kept.PolicyRef)
	}
	remoteDetached := latestWAFCoordinatorSnapshot(t, store, "edge-a")
	if snapshotHTTPRuleByID(t, remoteDetached, 3).PolicyRef != nil {
		t.Fatalf("remote dangling ref after detach snapshot: %+v", remoteDetached.Rules)
	}
}

func TestCompleteLifecycleApplyDetachesOfficialWAFHTTPPolicyRefsBeforeDisableMutation(t *testing.T) {
	fixture := newOfficialWAFDisableLifecycleFixture(t)
	ctx := t.Context()
	if _, err := fixture.service.Disable(ctx, fixture.pluginID, "admin"); err != nil {
		t.Fatalf("Disable() error = %v", err)
	}
	fixture.attachHTTPPolicyRefs(t)
	installed := mustInstalledPlugin(t, fixture.store, fixture.pluginID)
	if installed.CurrentLifecycle != "applying" || installed.PendingKind != "disable" || installed.PendingOperationID == "" {
		t.Fatalf("disable did not enter applying: %+v", installed)
	}
	policies, err := fixture.store.LoadAgentPluginPolicies(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if officialWAFPolicyID(policies) != fixture.instanceID {
		t.Fatalf("catalog before disable complete = %+v", policies)
	}

	completed, err := fixture.service.CompleteLifecycleApply(ctx, PluginApplyResult{
		PluginID: fixture.pluginID, OperationID: installed.PendingOperationID,
		TargetRevision: installed.PendingRevision, TargetDigest: installed.PendingTargetDigest,
		ActorID: "admin", Applied: true, AgentResults: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CompleteLifecycleApply() error = %v", err)
	}
	if completed.CurrentLifecycle != "disabled" {
		t.Fatalf("completed lifecycle = %+v", completed)
	}
	policies, err = fixture.store.LoadAgentPluginPolicies(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if officialWAFPolicyID(policies) != "" {
		t.Fatalf("catalog after disable still has waf: %+v", policies)
	}
	local, err := fixture.store.ListHTTPRules(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if parseRulePolicyRef(httpWAFRuleByID(t, local, 1).PolicyRefJSON) != nil {
		t.Fatalf("dangling ref after disable complete: %+v", local)
	}
	if httpWAFRuleByID(t, local, 2).PolicyRefJSON != `{"id":"custom-chain"}` {
		t.Fatalf("foreign ref detached: %+v", local)
	}
	detached := latestWAFCoordinatorSnapshot(t, fixture.store, "local")
	if snapshotHTTPRuleByID(t, detached, 1).PolicyRef != nil {
		t.Fatalf("dangling ref after disable snapshot: %+v", detached.Rules)
	}
}

func TestCompleteLifecycleApplyFailedDisableKeepsOfficialWAFHTTPPolicyRefs(t *testing.T) {
	fixture := newOfficialWAFDisableLifecycleFixture(t)
	ctx := t.Context()
	if _, err := fixture.service.Disable(ctx, fixture.pluginID, "admin"); err != nil {
		t.Fatalf("Disable() error = %v", err)
	}
	fixture.attachHTTPPolicyRefs(t)
	installed := mustInstalledPlugin(t, fixture.store, fixture.pluginID)
	completed, err := fixture.service.CompleteLifecycleApply(ctx, PluginApplyResult{
		PluginID: fixture.pluginID, OperationID: installed.PendingOperationID,
		TargetRevision: installed.PendingRevision, TargetDigest: installed.PendingTargetDigest,
		ActorID: "admin", Applied: false, AgentResults: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CompleteLifecycleApply() error = %v", err)
	}
	if completed.CurrentLifecycle != "degraded" {
		t.Fatalf("failed disable lifecycle = %+v", completed)
	}
	local, err := fixture.store.ListHTTPRules(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	ref := parseRulePolicyRef(httpWAFRuleByID(t, local, 1).PolicyRefJSON)
	if ref == nil || ref.ID != fixture.instanceID {
		t.Fatalf("matching ref dropped on failed disable: %+v", local)
	}
}

func TestDisableMutationDetachesOfficialWAFHTTPPolicyRefsBeforeLifecycleMutation(t *testing.T) {
	fixture := newOfficialWAFDisableLifecycleFixture(t)
	ctx := t.Context()
	fixture.attachHTTPPolicyRefs(t)
	summary, err := fixture.service.DisableMutation(ctx, fixture.pluginID, "admin")
	if err != nil {
		t.Fatalf("DisableMutation() error = %v", err)
	}
	if summary.CurrentLifecycle != "applying" && summary.CurrentLifecycle != "disabled" {
		t.Fatalf("DisableMutation lifecycle = %+v", summary)
	}
	local, err := fixture.store.ListHTTPRules(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if parseRulePolicyRef(httpWAFRuleByID(t, local, 1).PolicyRefJSON) != nil {
		t.Fatalf("dangling ref after DisableMutation: %+v", local)
	}
	if httpWAFRuleByID(t, local, 2).PolicyRefJSON != `{"id":"custom-chain"}` {
		t.Fatalf("foreign ref detached: %+v", local)
	}
}

func latestWAFCoordinatorSnapshot(t *testing.T, store *storage.GormStore, agentID string) storage.Snapshot {
	t.Helper()
	pointer, found, err := store.GetAgentRevisionPointer(t.Context(), agentID)
	if err != nil || !found || pointer.DesiredRevision <= 0 {
		t.Fatalf("GetAgentRevisionPointer(%s) = %+v found=%v err=%v", agentID, pointer, found, err)
	}
	runtime, found, err := store.LoadCoordinatorRuntimeSnapshot(t.Context(), agentID, pointer.DesiredRevision)
	if err != nil || !found {
		t.Fatalf("LoadCoordinatorRuntimeSnapshot(%s/%d) found=%v err=%v", agentID, pointer.DesiredRevision, found, err)
	}
	return runtime.Snapshot
}

func snapshotHTTPRuleByID(t *testing.T, snapshot storage.Snapshot, id int) storage.HTTPRule {
	t.Helper()
	for _, rule := range snapshot.Rules {
		if rule.ID == id {
			return rule
		}
	}
	t.Fatalf("snapshot rule %d not found in %+v", id, snapshot.Rules)
	return storage.HTTPRule{}
}

type staticWAFPolicyCatalogStore struct {
	policies []storage.PluginPolicy
}

func (s staticWAFPolicyCatalogStore) LoadAgentPluginPolicies(context.Context, string) ([]storage.PluginPolicy, error) {
	return s.policies, nil
}

func httpWAFRuleByID(t *testing.T, rows []storage.HTTPRuleRow, id int) storage.HTTPRuleRow {
	t.Helper()
	for _, row := range rows {
		if row.ID == id {
			return row
		}
	}
	t.Fatalf("rule %d not found in %+v", id, rows)
	return storage.HTTPRuleRow{}
}

func testHTTPWAFRuleRow(id int, agentID, policyRefJSON string) storage.HTTPRuleRow {
	return storage.HTTPRuleRow{
		ID: id, AgentID: agentID, FrontendURL: "http://app.example",
		BackendsJSON: `[{"url":"http://127.0.0.1:8080"}]`, LoadBalancingJSON: `{"strategy":"adaptive"}`,
		Enabled: true, TagsJSON: "[]", RelayChainJSON: "[]", RelayLayersJSON: "[]",
		CustomHeadersJSON: "[]", TrustedProxyRangesJSON: "[]", PolicyRefJSON: policyRefJSON, Revision: 1,
	}
}

func newHTTPWAFAttachStore(t *testing.T) *storage.GormStore {
	t.Helper()
	if testing.Short() {
		t.Skip("SQLite-backed WAF attach scenarios run in the full test tier")
	}
	root := t.TempDir()
	store, err := storage.NewStore(storage.StoreConfig{Driver: "sqlite", DataRoot: root, LocalAgentID: "local", TrafficStatsEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

type officialWAFDisableLifecycleFixture struct {
	store      *storage.GormStore
	service    *PluginService
	pluginID   string
	instanceID string
}

func newOfficialWAFDisableLifecycleFixture(t *testing.T) officialWAFDisableLifecycleFixture {
	t.Helper()
	if testing.Short() {
		t.Skip("SQLite-backed WAF attach scenarios run in the full test tier")
	}
	root := t.TempDir()
	store, err := storage.NewStore(storage.StoreConfig{Driver: "sqlite", DataRoot: root, LocalAgentID: "local", TrafficStatsEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := t.Context()
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	if err := store.SaveAgent(ctx, storage.AgentRow{ID: "local", Name: "local"}); err != nil {
		t.Fatal(err)
	}

	pluginID := "official.waf"
	instanceID := "official.waf-default"
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encodedKey := base64.StdEncoding.EncodeToString(publicKey)
	fingerprintSum := sha256.Sum256(publicKey)
	fingerprint := hex.EncodeToString(fingerprintSum[:])
	digest := strings.Repeat("a", 64)
	sourceID := "waf-attach-fixture"
	identity := storage.PluginPackageIdentity(digest, sourceID, fingerprint)
	cacheRoot := filepath.Join(root, "plugins", "packages")
	wasm := []byte("waf-policy-wasm-" + pluginID)
	wasmDigest := sha256.Sum256(wasm)
	rpcDigest := sha256.Sum256([]byte("rpc-" + pluginID))
	artifacts := []plugins.Artifact{
		{Path: "artifacts/linux-amd64/plugin", SHA256: hex.EncodeToString(rpcDigest[:]), Size: 12, Mode: "executable", GOOS: "linux", GOARCH: "amd64"},
		{Path: "artifacts/policy.wasm", SHA256: hex.EncodeToString(wasmDigest[:]), Size: int64(len(wasm)), Mode: "wasm"},
	}
	manifest := plugins.Manifest{
		SchemaVersion: 1, ID: pluginID, Version: "1.0.0", Name: pluginID,
		Runtime: plugins.Runtime{
			Kind: pluginsdk.RuntimeRPCService, ABI: pluginsdk.RPCABIV1,
			HostScope: pluginsdk.HostScopeControlPlane, Entry: "plugin", PolicyKind: "waf",
			Policy: &pluginsdk.RuntimePolicy{
				Kind: pluginsdk.RuntimeWASMPolicy, ABI: pluginsdk.PolicyABIV1, HostScope: pluginsdk.HostScopeAgent,
				Entry:          "artifacts/policy.wasm",
				ResourceBudget: plugins.ResourceBudget{TimeoutMS: 2, MemoryBytes: 1048576, Concurrency: 8, InputBytes: 65536, OutputBytes: 4096},
				FailurePolicy:  plugins.FailurePolicy{OnError: "fail-closed", OnBudget: "fail-closed", Restart: "never", CoreFallback: "preserve"},
			},
		},
		Artifacts:       artifacts,
		ExtensionPoints: []string{pluginsdk.ExtensionUIRoute, pluginsdk.ExtensionHTTPRequest},
		Permissions:     []plugins.Permission{{Name: "http.inspect"}},
		ResourceBudget:  plugins.ResourceBudget{TimeoutMS: 2000, MemoryBytes: 1048576, Concurrency: 8, InputBytes: 65536, OutputBytes: 4096, CPUMillis: 100, Restarts: 1},
		FailurePolicy:   plugins.FailurePolicy{OnError: "fail-closed", OnBudget: "fail-closed", Restart: "on-failure", CoreFallback: "preserve"},
		Signature:       plugins.Signature{Algorithm: "ed25519", KeyID: "test-key"},
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	row := storage.PluginPackageRow{
		Identity: identity, Digest: digest, PluginID: pluginID, Version: manifest.Version,
		SignatureKeyID: "test-key", SignaturePublicKey: encodedKey, SignatureFingerprint: fingerprint,
		SourceID: sourceID, SourceKind: marketplace.SourceKindCustom, ManifestJSON: string(manifestJSON),
		ConfigSchemaJSON: `{}`, VerifiedAt: now,
	}
	projected, projectedArtifacts, err := storage.ProjectPluginPackage(row, manifest)
	if err != nil {
		t.Fatal(err)
	}
	cachePath, err := marketplace.SignerCachePath(cacheRoot, digest, fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cachePath, "artifacts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cachePath, "artifacts", "policy.wasm"), wasm, 0o600); err != nil {
		t.Fatal(err)
	}
	projected.CachePath = cachePath
	if _, err := marketplace.NewVerifiedCache(cacheRoot, plugins.NewValidator(plugins.ValidatorOptions{}), nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = marketplace.DiscardVerifiedCacheRoot(cacheRoot) })

	installOp := storage.PluginOperationRow{
		ID: "op-install-" + pluginID, PluginID: pluginID, Kind: "install", Status: "succeeded",
		ActorID: "admin", AgentResultsJSON: `{}`, CreatedAt: now, CompletedAt: &now,
	}
	if err := storage.BindPluginOperationPackage(&installOp, projected); err != nil {
		t.Fatal(err)
	}
	installed := storage.InstalledPluginRow{
		PluginID: pluginID, ActivePackageDigest: digest, ActivePackageIdentity: identity,
		RuntimeKind: projected.RuntimeKind, RuntimeABI: projected.RuntimeABI, HostScope: projected.HostScope,
		ActiveSourceID: projected.SourceID, ActiveSourceKind: projected.SourceKind,
		ActiveSignatureKeyID: projected.SignatureKeyID, ActiveSignaturePublicKey: projected.SignaturePublicKey,
		ActiveSignatureFingerprint: projected.SignatureFingerprint, DesiredLifecycle: "enabled", CurrentLifecycle: "active",
		CleanupPolicyJSON: `{}`, LastOperationID: installOp.ID, StateVersion: 1, InstalledAt: now, UpdatedAt: now,
	}
	if err := store.InstallPlugin(ctx, storage.PluginInstallTransaction{
		Package: projected, Artifacts: projectedArtifacts, Installed: installed, Operation: installOp,
		Audit: storage.AuditEventRow{
			ID: "audit-install-" + pluginID, ActorID: "admin", Action: "plugin.install",
			TargetKind: "plugin", TargetID: pluginID, Result: "success", MetadataJSON: `{}`, CreatedAt: now,
		},
	}); err != nil {
		t.Fatalf("InstallPlugin() error = %v", err)
	}

	installed = mustInstalledPlugin(t, store, pluginID)
	instance := storage.PluginInstanceRow{
		ID: instanceID, PluginID: pluginID, ResourceGroupID: "default", TargetJSON: `[]`,
		PolicyChainsJSON: `[]`, SecretHandlesJSON: `[]`, BindingsJSON: `[]`, ConfigJSON: `{}`,
		ConfigVersion: 1, PendingConfigJSON: "", PendingTargetJSON: "", PendingPolicyChainsJSON: `[]`,
		PendingBindingsJSON: `[]`, PendingSecretHandlesJSON: `[]`, RollbackConfigJSON: "",
		RollbackPolicyChainsJSON: `[]`, RollbackBindingsJSON: `[]`, RollbackSecretHandlesJSON: `[]`,
		DesiredEnabled: true, CurrentState: "active", StatusSummaryJSON: `{}`, UpdatedAt: now,
	}
	configureOp := storage.PluginOperationRow{
		ID: "op-configure-" + pluginID, PluginID: pluginID, Kind: "configure", Status: "succeeded",
		ActorID: "admin", AgentResultsJSON: `{}`, CreatedAt: now, CompletedAt: &now,
	}
	if err := storage.BindPluginOperationPackage(&configureOp, projected); err != nil {
		t.Fatal(err)
	}
	nextInstalled := installed
	nextInstalled.LastOperationID = configureOp.ID
	nextInstalled.UpdatedAt = now
	if err := store.ApplyPluginMutation(ctx, storage.PluginMutation{
		PluginID: pluginID, ExpectedActive: installed.ActivePackageDigest, ExpectedStateVersion: installed.StateVersion,
		Installed: &nextInstalled, ReplaceInstance: &instance,
		Operation: configureOp,
		Audit: storage.AuditEventRow{
			ID: "audit-configure-" + pluginID, ActorID: "admin", Action: "plugin.configure",
			TargetKind: "plugin", TargetID: pluginID, Result: "success", MetadataJSON: `{}`, CreatedAt: now,
		},
	}); err != nil {
		t.Fatalf("seed instance: %v", err)
	}

	service := NewPluginService(store, cacheRoot)
	service.ConfigureRevisionMutations(config.Config{LocalAgentID: "local", EnableLocalAgent: true}, store)
	return officialWAFDisableLifecycleFixture{store: store, service: service, pluginID: pluginID, instanceID: instanceID}
}

func (f officialWAFDisableLifecycleFixture) attachHTTPPolicyRefs(t *testing.T) {
	t.Helper()
	attached := testHTTPWAFRuleRow(1, "local", marshalJSON(officialWAFObservePolicyRef(f.instanceID), ""))
	attached.FrontendURL = "http://app-a.example"
	kept := testHTTPWAFRuleRow(2, "local", `{"id":"custom-chain"}`)
	kept.FrontendURL = "http://app-b.example"
	if err := f.store.SaveHTTPRules(t.Context(), "local", []storage.HTTPRuleRow{attached, kept}); err != nil {
		t.Fatal(err)
	}
}

func mustInstalledPlugin(t *testing.T, store *storage.GormStore, pluginID string) storage.InstalledPluginRow {
	t.Helper()
	installed, found, err := store.GetInstalledPlugin(t.Context(), pluginID)
	if err != nil || !found {
		t.Fatalf("GetInstalledPlugin(%s) found=%v err=%v", pluginID, found, err)
	}
	return installed
}
