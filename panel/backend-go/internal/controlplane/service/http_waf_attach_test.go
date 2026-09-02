//go:build !integration

package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
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
