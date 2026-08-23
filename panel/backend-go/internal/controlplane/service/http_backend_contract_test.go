//go:build !integration

package service

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/authz"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/coordinator"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/marketplace"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	revisionpkg "github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/revision"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestNormalizeHTTPBackendsKeepsURLWireAndTagsProviders(t *testing.T) {
	for _, backend := range []HTTPRuleBackend{
		{URL: " http://127.0.0.1:8096 "},
		{Kind: pluginsdk.HTTPBackendKindURL, URL: " http://127.0.0.1:8096 "},
	} {
		normalized, err := normalizeHTTPBackends([]HTTPRuleBackend{backend})
		if err != nil {
			t.Fatal(err)
		}
		wire, err := json.Marshal(normalized)
		if err != nil {
			t.Fatal(err)
		}
		if string(wire) != `[{"url":"http://127.0.0.1:8096"}]` {
			t.Fatalf("URL backend wire changed: %s", wire)
		}
	}
	historical := "HTTP://user:pass@127.0.0.1:8096/path#fragment"
	normalized, err := normalizeHTTPBackends([]HTTPRuleBackend{
		{URL: "ftp://127.0.0.1/file"},
		{URL: historical},
		{URL: historical},
	})
	if err != nil {
		t.Fatalf("historical URL matrix rejected: %v", err)
	}
	wire, err := json.Marshal(normalized)
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"url":"HTTP://user:pass@127.0.0.1:8096/path#fragment"},{"url":"HTTP://user:pass@127.0.0.1:8096/path#fragment"}]`
	if string(wire) != want {
		t.Fatalf("historical URL normalization/wire = %s, want %s", wire, want)
	}

	provider := HTTPRuleBackend{
		Kind: pluginsdk.HTTPBackendKindPluginProvider,
		PluginProvider: &pluginsdk.HTTPPluginProviderRef{
			InstanceID: "provider-1",
			ProviderID: "default",
		},
	}
	if _, err := normalizeHTTPBackends([]HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}, provider}); err != nil {
		t.Fatalf("mixed URL/provider backends rejected: %v", err)
	}

	for name, backend := range map[string]HTTPRuleBackend{
		"untagged provider": {PluginProvider: provider.PluginProvider},
		"unknown kind":      {Kind: "unknown", URL: "http://127.0.0.1:8096"},
		"mixed payload":     {Kind: pluginsdk.HTTPBackendKindPluginProvider, URL: "http://127.0.0.1:8096", PluginProvider: provider.PluginProvider},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := normalizeHTTPBackends([]HTTPRuleBackend{backend}); err == nil {
				t.Fatal("invalid provider backend was accepted")
			}
		})
	}
}

func TestNormalizeHTTPBackendsEnforcesProviderIDByteLimit(t *testing.T) {
	for _, test := range []struct {
		name    string
		id      string
		wantErr bool
	}{
		{name: "64 bytes", id: "a" + strings.Repeat("b", pluginsdk.ProviderIDMaxBytes-1)},
		{name: "65 bytes", id: "a" + strings.Repeat("b", pluginsdk.ProviderIDMaxBytes), wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := normalizeHTTPBackends([]HTTPRuleBackend{{
				Kind:           pluginsdk.HTTPBackendKindPluginProvider,
				PluginProvider: &pluginsdk.HTTPPluginProviderRef{InstanceID: "provider-1", ProviderID: test.id},
			}})
			if (err != nil) != test.wantErr {
				t.Fatalf("normalizeHTTPBackends() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestRuleServiceCreateStillRequiresReadyHTTPBackendProvider(t *testing.T) {
	t.Parallel()
	store := newServiceOwnerStore(t)
	if err := store.SaveAgent(t.Context(), storage.AgentRow{ID: "local", Name: "local", Platform: runtime.GOOS + "-" + runtime.GOARCH}); err != nil {
		t.Fatal(err)
	}
	svc := NewRuleService(config.Config{LocalAgentID: "local", EnableLocalAgent: true}, store)
	frontend := "https://rules.example.com"
	enabled := true
	backends := []HTTPRuleBackend{{
		Kind:           pluginsdk.HTTPBackendKindPluginProvider,
		PluginProvider: &pluginsdk.HTTPPluginProviderRef{InstanceID: "provider-1", ProviderID: "default"},
	}}
	ctx := WithSystemMutationPrincipal(t.Context(), "system:owner")
	_, err := svc.Create(ctx, "local", HTTPRuleInput{FrontendURL: &frontend, Backends: &backends, Enabled: &enabled})
	if err == nil {
		t.Fatal("rule-page Create accepted a plugin provider without a ready generation")
	}
	if leftover, listErr := store.ListHTTPRules(t.Context(), "local"); listErr != nil || len(leftover) != 0 {
		t.Fatalf("unready provider leaked rules=%+v err=%v", leftover, listErr)
	}
}

func TestPluginPublishWritesSingleEnabledHTTPRuleWithoutLiveCatalog(t *testing.T) {
	t.Parallel()
	fixture := newPluginPublishFixture(t, true)
	ctx := WithSystemMutationPrincipal(t.Context(), "system:owner")
	instanceID := "provider-1"
	published, err := callPluginPublish(t, fixture.service, ctx, pluginPublishFields(fixture.pluginID, instanceID, "https://emby.example.com", 0))
	if err != nil {
		t.Fatalf("PublishMutation() error = %v", err)
	}
	assertPluginPublishRuleCount(t, fixture.store, "local", 1)
	stored := mustPluginPublishRule(t, fixture.store, "local", 0)
	if !stored.Enabled || stored.FrontendURL != "https://emby.example.com" {
		t.Fatalf("published rule = %+v", stored)
	}
	storedTags := parseStringArray(stored.TagsJSON)
	if !slicesContains(storedTags, "plugin") || !slicesContains(storedTags, "plugin:"+fixture.pluginID) {
		t.Fatalf("published rule tags = %v, want plugin provenance tags", storedTags)
	}
	assertPluginProviderBackend(t, stored, published.ID, "default")
	if certs, err := fixture.store.ListManagedCertificates(t.Context()); err != nil {
		t.Fatal(err)
	} else if len(certs) == 0 {
		t.Fatal("https frontend_url did not trigger the managed certificate path")
	}
	entries := publishedEntriesFromState(t, fixture, "local")
	if len(entries) != 1 || entries[0].RuleID != stored.ID || entries[0].AgentID != "local" || entries[0].FrontendURL != "https://emby.example.com" || !entries[0].Enabled {
		t.Fatalf("published_entries = %+v", entries)
	}
	instance := mustPluginInstance(t, fixture.store, fixture.pluginID)
	if containsHTTPRuleBinding(instance, stored.ID, "local") {
		t.Fatalf("persisted bindings = %s, http.backend-provider must not store http_rule", instance.BindingsJSON)
	}
	if !containsHTTPRuleBindingDetail(t, published, stored.ID, "local") {
		t.Fatalf("projected bindings = %+v, want http_rule %d on local", published.Bindings, stored.ID)
	}
	if instance.DesiredEnabled != true || len(instanceTargets(t, instance)) != 1 || instanceTargets(t, instance)[0] != "local" {
		t.Fatalf("published instance = %+v", instance)
	}

	frontend := "https://rules-page.example.com"
	enabled := true
	backends := []HTTPRuleBackend{{
		Kind:           pluginsdk.HTTPBackendKindPluginProvider,
		PluginProvider: &pluginsdk.HTTPPluginProviderRef{InstanceID: instance.ID, ProviderID: "default"},
	}}
	rules := NewRuleService(config.Config{LocalAgentID: "local", EnableLocalAgent: true}, fixture.store)
	if _, err := rules.Create(ctx, "local", HTTPRuleInput{FrontendURL: &frontend, Backends: &backends, Enabled: &enabled}); err == nil {
		t.Fatal("rule-page Create accepted a plugin provider without a ready generation after publish")
	}
}

func TestPluginListProjectsInstalledVersionForMarketplaceComparison(t *testing.T) {
	t.Parallel()
	fixture := newPluginPublishFixture(t, true)
	summaries, err := fixture.service.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	detail, err := fixture.service.Detail(t.Context(), fixture.pluginID)
	if err != nil {
		t.Fatal(err)
	}
	for _, summary := range summaries {
		if summary.PluginID == fixture.pluginID {
			if summary.ActiveVersion == "" || summary.ActiveVersion != detail.Package.Version {
				t.Fatalf("active version = %q, package version = %q", summary.ActiveVersion, detail.Package.Version)
			}
			return
		}
	}
	t.Fatalf("plugin %q missing from list", fixture.pluginID)
}

func TestPluginPublishReconcilesSupersededConfigureBeforeNextMutation(t *testing.T) {
	fixture := newPluginPublishFixture(t, true)
	ctx := WithSystemMutationPrincipal(t.Context(), "system:owner")
	instance, err := callPluginPublish(t, fixture.service, ctx, pluginPublishFields(fixture.pluginID, "provider-1", "https://superseded.example.com", 0))
	if err != nil {
		t.Fatalf("first PublishMutation() error = %v", err)
	}
	installed, found, err := fixture.store.GetInstalledPlugin(ctx, fixture.pluginID)
	if err != nil || !found || installed.PendingOperationID == "" || installed.PendingRevision <= 0 {
		t.Fatalf("pending installed plugin = %+v, found=%t err=%v", installed, found, err)
	}
	fixture.service.store = supersededPluginPublishStore{
		GormStore: fixture.store,
		revision: storage.AgentRevisionRow{
			AgentID: "local", Revision: installed.PendingRevision, State: storage.AgentRevisionStateSuperseded,
		},
	}
	if err := fixture.service.reconcileSupersededConfigure(ctx, fixture.pluginID); err != nil {
		t.Fatalf("reconcileSupersededConfigure() error = %v", err)
	}
	installed, found, err = fixture.store.GetInstalledPlugin(ctx, fixture.pluginID)
	if err != nil || !found || installed.PendingOperationID != "" {
		t.Fatalf("reconciled installed plugin = %+v, found=%t err=%v", installed, found, err)
	}
	row := mustPluginInstanceByID(t, fixture.store, instance.ID)
	if row.PendingOperationID != "" || row.CurrentState != "degraded" {
		t.Fatalf("reconciled instance = %+v", row)
	}
	operations, err := fixture.store.ListPluginOperations(ctx, fixture.pluginID)
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range operations {
		if operation.ID == installed.LastOperationID && operation.Status == "failed" && operation.ErrorClass == "agent_apply" {
			return
		}
	}
	t.Fatalf("superseded operation was not failed: %+v", operations)
}

func TestPluginLifecycleReconcilerRecoversSupersededConfigureWithoutNextMutation(t *testing.T) {
	fixture := newPluginPublishFixture(t, true)
	ctx := WithSystemMutationPrincipal(t.Context(), "system:owner")
	instance, err := callPluginPublish(t, fixture.service, ctx, pluginPublishFields(fixture.pluginID, "provider-1", "https://superseded-background.example.com", 0))
	if err != nil {
		t.Fatalf("PublishMutation() error = %v", err)
	}
	installed, found, err := fixture.store.GetInstalledPlugin(ctx, fixture.pluginID)
	if err != nil || !found || installed.PendingOperationID == "" || installed.PendingRevision <= 0 {
		t.Fatalf("pending installed plugin = %+v, found=%t err=%v", installed, found, err)
	}
	store := supersededPluginPublishStore{
		GormStore: fixture.store,
		revision: storage.AgentRevisionRow{
			AgentID: "local", Revision: installed.PendingRevision, State: storage.AgentRevisionStateSuperseded,
		},
	}
	fixture.service.store = store
	reconciler, err := NewPluginLifecycleReconciler(store, fixture.service)
	if err != nil {
		t.Fatalf("NewPluginLifecycleReconciler() error = %v", err)
	}
	if err := reconciler.RecoverSupersededOperations(ctx); err != nil {
		t.Fatalf("RecoverSupersededOperations() error = %v", err)
	}
	installed, found, err = fixture.store.GetInstalledPlugin(ctx, fixture.pluginID)
	if err != nil || !found || installed.PendingOperationID != "" {
		t.Fatalf("recovered installed plugin = %+v, found=%t err=%v", installed, found, err)
	}
	row := mustPluginInstanceByID(t, fixture.store, instance.ID)
	if row.PendingOperationID != "" || row.CurrentState != "degraded" {
		t.Fatalf("recovered instance = %+v", row)
	}
}

func TestHeartbeatRevisionRebasesPendingPluginGeneration(t *testing.T) {
	fixture := newPluginPublishFixture(t, true)
	ctx := WithSystemMutationPrincipal(t.Context(), "system:owner")
	instance, err := callPluginPublish(t, fixture.service, ctx, pluginPublishFields(fixture.pluginID, "provider-1", "https://heartbeat-rebase.example.com", 0))
	if err != nil {
		t.Fatalf("PublishMutation() error = %v", err)
	}
	installed, found, err := fixture.store.GetInstalledPlugin(ctx, fixture.pluginID)
	if err != nil || !found || installed.PendingRevision <= 0 {
		t.Fatalf("pending installed plugin = %+v, found=%t err=%v", installed, found, err)
	}
	originalRevision := installed.PendingRevision
	pendingBefore := mustPluginInstanceByID(t, fixture.store, instance.ID)
	runtimeSnapshot, found, err := fixture.store.LoadCoordinatorRuntimeSnapshot(ctx, "local", originalRevision)
	if err != nil || !found || len(runtimeSnapshot.Snapshot.PluginGenerations) != 1 {
		t.Fatalf("pending runtime snapshot = %+v, found=%t err=%v", runtimeSnapshot, found, err)
	}

	const heartbeatRevision = int64(432)
	heartbeatSnapshot := runtimeSnapshot.Snapshot
	heartbeatSnapshot.Revision = heartbeatRevision
	for index := range heartbeatSnapshot.PluginGenerations {
		heartbeatSnapshot.PluginGenerations[index].Revision = heartbeatRevision
	}
	payload, digest, err := revisionpkg.CanonicalSnapshotPayload(heartbeatSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.EnsureAgentHeartbeatRevision(ctx, "local", heartbeatSnapshot, payload, digest, time.Now().UTC()); err != nil {
		t.Fatalf("EnsureAgentHeartbeatRevision() error = %v", err)
	}
	pendingAfter := mustPluginInstanceByID(t, fixture.store, instance.ID)
	if pendingAfter.PendingOperationID != pendingBefore.PendingOperationID ||
		pendingAfter.PendingConfigJSON != pendingBefore.PendingConfigJSON ||
		pendingAfter.PendingSecretHandlesJSON != pendingBefore.PendingSecretHandlesJSON {
		t.Fatalf("heartbeat rebase changed pending configuration: before=%+v after=%+v", pendingBefore, pendingAfter)
	}

	coord, err := coordinator.New(fixture.store, coordinator.Options{})
	if err != nil {
		t.Fatal(err)
	}
	api := NewRevisionAPI(fixture.store, coord)
	reconciler, err := NewPluginLifecycleReconciler(fixture.store, fixture.service)
	if err != nil {
		t.Fatal(err)
	}
	api.SetPluginLifecycleReconciler(reconciler)
	pull, err := api.PullRemoteRevision(ctx, "local")
	if err != nil || pull.Lease == nil || pull.Snapshot == nil || pull.Lease.Revision != heartbeatRevision {
		t.Fatalf("PullRemoteRevision() = %+v, %v", pull, err)
	}
	lease := pull.Lease
	const runtimeGenerationID = "generation-432-heartbeat-rebase"
	if _, err := api.StartRemoteRevision(ctx, "local", RemoteRevisionStart{
		AgentID: "local", Revision: lease.Revision, RetryCycle: lease.RetryCycle, Attempt: lease.Attempt,
		LeaseID: lease.LeaseID, GenerationID: runtimeGenerationID,
	}); err != nil {
		t.Fatalf("StartRemoteRevision() error = %v", err)
	}
	generation := pull.Snapshot.PluginGenerations[0]
	status := storage.PluginRuntimeStatus{
		InstanceID: generation.InstanceID, PluginID: generation.PluginID, OperationID: generation.OperationID,
		Revision: heartbeatRevision, GenerationID: generation.ID, PackageDigest: generation.PackageDigest,
		ArtifactDigest: generation.Artifact.SHA256, ConfigVersion: generation.ConfigVersion,
		RuntimeKind: generation.Runtime.Kind, State: "active", Sequence: 1,
		Details: json.RawMessage(`{}`), Budget: json.RawMessage(`{}`),
	}
	report := RemoteRevisionReport{
		AgentID: "local", Revision: lease.Revision, RetryCycle: lease.RetryCycle, Attempt: lease.Attempt,
		LeaseID: lease.LeaseID, GenerationID: runtimeGenerationID, Status: storage.AgentRevisionStateApplied,
		PluginStatuses: []storage.PluginRuntimeStatus{status},
	}
	if _, err := api.ReportRemoteRevision(ctx, "local", report); err != nil {
		t.Fatalf("ReportRemoteRevision() error = %v", err)
	}
	if _, err := api.ReportRemoteRevision(ctx, "local", report); err != nil {
		t.Fatalf("ReportRemoteRevision() replay error = %v", err)
	}

	installed, found, err = fixture.store.GetInstalledPlugin(ctx, fixture.pluginID)
	if err != nil || !found || installed.PendingOperationID != "" {
		t.Fatalf("rebased installed plugin = %+v, found=%t err=%v", installed, found, err)
	}
	row := mustPluginInstanceByID(t, fixture.store, instance.ID)
	if row.PendingOperationID != "" || row.ConfigVersion != 1 || row.CurrentState != "active" {
		t.Fatalf("rebased instance = %+v", row)
	}
	operations, err := fixture.store.ListPluginOperations(ctx, fixture.pluginID)
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range operations {
		if operation.ID == installed.LastOperationID && operation.Status == "succeeded" && operation.TargetRevision == heartbeatRevision {
			return
		}
	}
	t.Fatalf("rebased operation did not succeed at revision %d: %+v", heartbeatRevision, operations)
}

func TestHeartbeatRevisionRejectsChangedInheritedPluginGeneration(t *testing.T) {
	fixture := newPluginPublishFixture(t, true)
	ctx := WithSystemMutationPrincipal(t.Context(), "system:owner")
	instance, err := callPluginPublish(t, fixture.service, ctx, pluginPublishFields(fixture.pluginID, "provider-1", "http://heartbeat-mismatch.example.com", 0))
	if err != nil {
		t.Fatalf("PublishMutation() error = %v", err)
	}
	installed, found, err := fixture.store.GetInstalledPlugin(ctx, fixture.pluginID)
	if err != nil || !found || installed.PendingRevision <= 0 {
		t.Fatalf("pending installed plugin = %+v, found=%t err=%v", installed, found, err)
	}
	originalRevision := installed.PendingRevision
	pendingBefore := mustPluginInstanceByID(t, fixture.store, instance.ID)
	runtimeSnapshot, found, err := fixture.store.LoadCoordinatorRuntimeSnapshot(ctx, "local", originalRevision)
	if err != nil || !found || len(runtimeSnapshot.Snapshot.PluginGenerations) != 1 {
		t.Fatalf("pending runtime snapshot = %+v, found=%t err=%v", runtimeSnapshot, found, err)
	}
	const heartbeatRevision = int64(432)
	heartbeatSnapshot := runtimeSnapshot.Snapshot
	heartbeatSnapshot.Revision = heartbeatRevision
	for index := range heartbeatSnapshot.PluginGenerations {
		heartbeatSnapshot.PluginGenerations[index].Revision = heartbeatRevision
		heartbeatSnapshot.PluginGenerations[index].ID = strings.Repeat("f", sha256.Size*2)
	}
	payload, digest, err := revisionpkg.CanonicalSnapshotPayload(heartbeatSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.EnsureAgentHeartbeatRevision(ctx, "local", heartbeatSnapshot, payload, digest, time.Now().UTC()); !errors.Is(err, storage.ErrPluginGenerationConflict) {
		t.Fatalf("EnsureAgentHeartbeatRevision() error = %v, want generation conflict", err)
	}
	installed, found, err = fixture.store.GetInstalledPlugin(ctx, fixture.pluginID)
	if err != nil || !found || installed.PendingRevision != originalRevision {
		t.Fatalf("conflicting heartbeat changed installed plugin = %+v, found=%t err=%v", installed, found, err)
	}
	pendingAfter := mustPluginInstanceByID(t, fixture.store, instance.ID)
	if pendingAfter.PendingOperationID != pendingBefore.PendingOperationID ||
		pendingAfter.PendingConfigJSON != pendingBefore.PendingConfigJSON ||
		pendingAfter.PendingSecretHandlesJSON != pendingBefore.PendingSecretHandlesJSON {
		t.Fatalf("conflicting heartbeat changed pending configuration: before=%+v after=%+v", pendingBefore, pendingAfter)
	}
	pointer, found, err := fixture.store.GetAgentRevisionPointer(ctx, "local")
	if err != nil || !found || pointer.DesiredRevision != originalRevision {
		t.Fatalf("conflicting heartbeat advanced pointer = %+v, found=%t err=%v", pointer, found, err)
	}
}

func TestLaterConfigRevisionRebasesPendingPluginGeneration(t *testing.T) {
	fixture := newPluginPublishFixture(t, true)
	ctx := WithSystemMutationPrincipal(t.Context(), "system:owner")
	instance, err := callPluginPublish(t, fixture.service, ctx, pluginPublishFields(fixture.pluginID, "provider-1", "http://provider-pending.example.com", 0))
	if err != nil {
		t.Fatalf("PublishMutation() error = %v", err)
	}
	installed, found, err := fixture.store.GetInstalledPlugin(ctx, fixture.pluginID)
	if err != nil || !found || installed.PendingRevision <= 0 {
		t.Fatalf("pending installed plugin = %+v, found=%t err=%v", installed, found, err)
	}
	originalRevision := installed.PendingRevision
	frontend := "http://unrelated.example.com"
	backends := []HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}}
	enabled := true
	rules := NewRuleService(config.Config{LocalAgentID: "local", EnableLocalAgent: true}, fixture.store)
	unrelated, err := rules.Create(ctx, "local", HTTPRuleInput{FrontendURL: &frontend, Backends: &backends, Enabled: &enabled})
	if err != nil {
		t.Fatalf("Create() unrelated rule error = %v", err)
	}
	if int64(unrelated.Revision) <= originalRevision {
		t.Fatalf("unrelated rule revision = %d, want > %d", unrelated.Revision, originalRevision)
	}
	installed, found, err = fixture.store.GetInstalledPlugin(ctx, fixture.pluginID)
	if err != nil || !found || installed.PendingRevision != int64(unrelated.Revision) {
		t.Fatalf("rebased installed plugin = %+v, found=%t err=%v", installed, found, err)
	}
	fence, found, err := fixture.store.GetPluginAgentRuntimeStatusFence(ctx, installed.PendingOperationID, "local", instance.ID)
	if err != nil || !found || fence.Revision != int64(unrelated.Revision) || fence.GenerationID == "" {
		t.Fatalf("rebased runtime fence = %+v, found=%t err=%v", fence, found, err)
	}
}

func TestAppliedHeartbeatCompletesPreviouslyRebasedPluginGeneration(t *testing.T) {
	fixture := newPluginPublishFixture(t, true)
	ctx := WithSystemMutationPrincipal(t.Context(), "system:owner")
	const agentID = "edge-heartbeat-rebase"
	const agentToken = "heartbeat-rebase-agent-token"
	if err := fixture.store.SaveAgent(ctx, storage.AgentRow{
		ID: agentID, Name: agentID, AgentToken: agentToken, Version: "1.0.0",
		Platform: runtime.GOOS + "-" + runtime.GOARCH, CapabilitiesJSON: marshalStringArray(defaultLocalCapabilities),
	}); err != nil {
		t.Fatal(err)
	}
	fields := pluginPublishFields(fixture.pluginID, "provider-1", "http://heartbeat-report.example.com", 0)
	fields["Targets"] = []string{agentID}
	instance, err := callPluginPublish(t, fixture.service, ctx, fields)
	if err != nil {
		t.Fatalf("PublishMutation() error = %v", err)
	}
	installed, found, err := fixture.store.GetInstalledPlugin(ctx, fixture.pluginID)
	if err != nil || !found || installed.PendingRevision <= 0 {
		t.Fatalf("pending installed plugin = %+v, found=%t err=%v", installed, found, err)
	}
	runtimeSnapshot, found, err := fixture.store.LoadCoordinatorRuntimeSnapshot(ctx, agentID, installed.PendingRevision)
	if err != nil || !found || len(runtimeSnapshot.Snapshot.PluginGenerations) != 1 {
		t.Fatalf("pending runtime snapshot = %+v, found=%t err=%v", runtimeSnapshot, found, err)
	}
	const heartbeatRevision = int64(432)
	heartbeatSnapshot := runtimeSnapshot.Snapshot
	heartbeatSnapshot.Revision = heartbeatRevision
	for index := range heartbeatSnapshot.PluginGenerations {
		heartbeatSnapshot.PluginGenerations[index].Revision = heartbeatRevision
	}
	payload, digest, err := revisionpkg.CanonicalSnapshotPayload(heartbeatSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.EnsureAgentHeartbeatRevision(ctx, agentID, heartbeatSnapshot, payload, digest, time.Now().UTC()); err != nil {
		t.Fatalf("EnsureAgentHeartbeatRevision() error = %v", err)
	}
	coord, err := coordinator.New(fixture.store, coordinator.Options{})
	if err != nil {
		t.Fatal(err)
	}
	api := NewRevisionAPI(fixture.store, coord)
	reconciler, err := NewPluginLifecycleReconciler(fixture.store, fixture.service)
	if err != nil {
		t.Fatal(err)
	}
	api.SetPluginLifecycleReconciler(reconciler)
	pull, err := api.PullRemoteRevision(ctx, agentID)
	if err != nil || pull.Lease == nil || pull.Snapshot == nil {
		t.Fatalf("PullRemoteRevision() = %+v, %v", pull, err)
	}
	lease := pull.Lease
	const runtimeGenerationID = "generation-432-heartbeat-report"
	if _, err := api.StartRemoteRevision(ctx, agentID, RemoteRevisionStart{
		AgentID: agentID, Revision: lease.Revision, RetryCycle: lease.RetryCycle, Attempt: lease.Attempt,
		LeaseID: lease.LeaseID, GenerationID: runtimeGenerationID,
	}); err != nil {
		t.Fatalf("StartRemoteRevision() error = %v", err)
	}
	if _, err := coord.Applied(ctx, coordinator.AppliedReport{
		Lease: coordinator.Lease{
			AgentID: agentID, Revision: lease.Revision, RetryCycle: lease.RetryCycle, Attempt: lease.Attempt,
			LeaseID: lease.LeaseID, SnapshotArtifactID: "snapshot-" + lease.SnapshotDigest,
			SnapshotDigest: lease.SnapshotDigest, DesiredVersion: lease.DesiredVersion,
			ApplyTimeoutSeconds: lease.ApplyTimeoutSeconds, DrainTimeoutSeconds: lease.DrainTimeoutSeconds,
		},
		GenerationID: runtimeGenerationID,
	}); err != nil {
		t.Fatalf("Coordinator.Applied() error = %v", err)
	}

	agentService := NewAgentService(config.Config{LocalAgentID: "local", EnableLocalAgent: true}, fixture.store)
	agentService.revisionAPI.SetPluginLifecycleReconciler(reconciler)
	generation := pull.Snapshot.PluginGenerations[0]
	status := storage.PluginRuntimeStatus{
		InstanceID: generation.InstanceID, PluginID: generation.PluginID, OperationID: generation.OperationID,
		Revision: heartbeatRevision, GenerationID: generation.ID, PackageDigest: generation.PackageDigest,
		ArtifactDigest: generation.Artifact.SHA256, ConfigVersion: generation.ConfigVersion,
		RuntimeKind: generation.Runtime.Kind, State: "active", Sequence: 1,
		Details: json.RawMessage(`{}`), Budget: json.RawMessage(`{}`),
	}
	if _, err := agentService.Heartbeat(ctx, HeartbeatRequest{
		AgentID: agentID, CurrentRevision: heartbeatRevision, LastApplyRevision: heartbeatRevision,
		LastApplyStatus: "success", PluginStatuses: []storage.PluginRuntimeStatus{status},
	}, agentToken); err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}
	installed, found, err = fixture.store.GetInstalledPlugin(ctx, fixture.pluginID)
	if err != nil || !found || installed.PendingOperationID != "" {
		t.Fatalf("heartbeat-completed plugin = %+v, found=%t err=%v", installed, found, err)
	}
	row := mustPluginInstanceByID(t, fixture.store, instance.ID)
	if row.PendingOperationID != "" || row.ConfigVersion != 1 || row.CurrentState != "active" {
		t.Fatalf("heartbeat-completed instance = %+v", row)
	}
}

type supersededPluginPublishStore struct {
	*storage.GormStore
	revision storage.AgentRevisionRow
}

func (s supersededPluginPublishStore) GetCoordinatorRevision(_ context.Context, agentID string, revision int64) (storage.AgentRevisionRow, bool, error) {
	if s.revision.AgentID == agentID && s.revision.Revision == revision {
		return s.revision, true, nil
	}
	return storage.AgentRevisionRow{}, false, nil
}

func TestPluginPublishRejectsNonHTTPBackendAndLeavesRulesUnchanged(t *testing.T) {
	t.Parallel()
	fixture := newPluginPublishFixture(t, false)
	ctx := WithSystemMutationPrincipal(t.Context(), "system:owner")
	_, err := callPluginPublish(t, fixture.service, ctx, pluginPublishFields(fixture.pluginID, "provider-1", "https://emby.example.com", 0))
	if err == nil {
		t.Fatal("PublishMutation accepted a package without an HTTP backend")
	}
	if !errors.Is(err, ErrInvalidArgument) && !strings.Contains(err.Error(), "HTTP") && !strings.Contains(err.Error(), "http") {
		t.Fatalf("PublishMutation() error = %v, want HTTP backend rejection", err)
	}
	assertPluginPublishRuleCount(t, fixture.store, "local", 0)
	instances, err := fixture.store.ListPluginInstances(t.Context(), fixture.pluginID)
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 0 {
		t.Fatalf("rejected publish wrote instances = %+v", instances)
	}
}

func TestPluginPublishRejectsMultipleTargetsOrMissingDomain(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		fields map[string]any
	}{
		{name: "multiple targets", fields: map[string]any{"Targets": []string{"local", "edge-b"}, "FrontendURL": "https://emby.example.com"}},
		{name: "missing domain", fields: map[string]any{"Targets": []string{"local"}, "FrontendURL": ""}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newPluginPublishFixture(t, true)
			ctx := WithSystemMutationPrincipal(t.Context(), "system:owner")
			fields := pluginPublishFields(fixture.pluginID, "provider-1", "https://emby.example.com", 0)
			for key, value := range test.fields {
				fields[key] = value
			}
			_, err := callPluginPublish(t, fixture.service, ctx, fields)
			if err == nil {
				t.Fatal("PublishMutation accepted an invalid publish request")
			}
			assertPluginPublishRuleCount(t, fixture.store, "local", 0)
			instances, listErr := fixture.store.ListPluginInstances(t.Context(), fixture.pluginID)
			if listErr != nil {
				t.Fatal(listErr)
			}
			if len(instances) != 0 {
				t.Fatalf("invalid publish mutated instances = %+v", instances)
			}
		})
	}
}

func TestPluginPublishUpdatesOriginalRuleAndCreatesIndependentDomain(t *testing.T) {
	t.Parallel()
	fixture := newPluginPublishFixture(t, true)
	ctx := WithSystemMutationPrincipal(t.Context(), "system:owner")
	instanceID := "provider-1"
	fields := pluginPublishFields(fixture.pluginID, instanceID, "https://emby.example.com", 0)
	firstInstance, err := callPluginPublish(t, fixture.service, ctx, fields)
	if err != nil {
		t.Fatalf("first PublishMutation() error = %v", err)
	}
	first := mustPluginPublishRule(t, fixture.store, "local", 0)
	fields["RuleID"] = first.ID
	fields["FrontendURL"] = "https://emby-updated.example.com"
	if _, err := callPluginPublish(t, fixture.service, ctx, fields); err != nil {
		t.Fatalf("update PublishMutation() error = %v", err)
	}
	assertPluginPublishRuleCount(t, fixture.store, "local", 1)
	if updated := mustPluginPublishRule(t, fixture.store, "local", 0); updated.ID != first.ID || updated.FrontendURL != "https://emby-updated.example.com" || !slicesContains(parseStringArray(updated.TagsJSON), "plugin") || !slicesContains(parseStringArray(updated.TagsJSON), "plugin:"+fixture.pluginID) {
		t.Fatalf("updated rule = %+v, want id %d, new frontend_url, and plugin provenance tags", updated, first.ID)
	}

	delete(fields, "RuleID")
	fields["FrontendURL"] = "https://emby-second.example.com"
	if _, err := callPluginPublish(t, fixture.service, ctx, fields); err != nil {
		t.Fatalf("second-domain PublishMutation() error = %v", err)
	}
	assertPluginPublishRuleCount(t, fixture.store, "local", 2)
	second := mustPluginPublishRule(t, fixture.store, "local", 1)
	if second.ID == first.ID || second.FrontendURL != "https://emby-second.example.com" || !second.Enabled {
		t.Fatalf("second published rule = %+v", second)
	}
	assertPluginProviderBackend(t, second, firstInstance.ID, "default")
	entries := publishedEntriesFromState(t, fixture, "local")
	if len(entries) != 2 {
		t.Fatalf("published_entries = %+v", entries)
	}
	if containsHTTPRuleBinding(mustPluginInstance(t, fixture.store, fixture.pluginID), first.ID, "local") {
		t.Fatal("updated publish persisted an http_rule binding that http.backend-provider cannot own")
	}
}

func TestPluginUnpublishDeletesOneEntryAndKeepsIndependentDomain(t *testing.T) {
	t.Parallel()
	fixture := newPluginPublishFixture(t, true)
	ctx := WithSystemMutationPrincipal(t.Context(), "system:owner")
	instanceID := "provider-1"
	fields := pluginPublishFields(fixture.pluginID, instanceID, "https://emby.example.com", 0)
	firstInstance, err := callPluginPublish(t, fixture.service, ctx, fields)
	if err != nil {
		t.Fatalf("first PublishMutation() error = %v", err)
	}
	first := mustPluginPublishRule(t, fixture.store, "local", 0)
	delete(fields, "RuleID")
	fields["FrontendURL"] = "https://emby-second.example.com"
	if _, err := callPluginPublish(t, fixture.service, ctx, fields); err != nil {
		t.Fatalf("second-domain PublishMutation() error = %v", err)
	}
	second := mustPluginPublishRule(t, fixture.store, "local", 1)
	assertPluginPublishRuleCount(t, fixture.store, "local", 2)

	updated, err := callPluginUnpublish(t, fixture.service, ctx, fixture.pluginID, "local", second.ID)
	if err != nil {
		t.Fatalf("UnpublishMutation() error = %v", err)
	}
	if updated.ID != firstInstance.ID {
		t.Fatalf("unpublish instance = %+v, want %s", updated, firstInstance.ID)
	}
	assertPluginPublishRuleCount(t, fixture.store, "local", 1)
	if stored := mustPluginPublishRule(t, fixture.store, "local", 0); stored.ID != first.ID || stored.FrontendURL != "https://emby.example.com" {
		t.Fatalf("remaining rule = %+v, want original entry %d", stored, first.ID)
	}
	entries := publishedEntriesFromState(t, fixture, "local")
	if len(entries) != 1 || !containsPublishedEntry(entries, first.ID, "local", "https://emby.example.com") {
		t.Fatalf("published_entries after first unpublish = %+v", entries)
	}
	if containsPublishedEntry(entries, second.ID, "local", "https://emby-second.example.com") {
		t.Fatalf("unpublished entry still projected = %+v", entries)
	}

	if _, err := callPluginUnpublish(t, fixture.service, ctx, fixture.pluginID, "local", first.ID); err != nil {
		t.Fatalf("second UnpublishMutation() error = %v", err)
	}
	assertPluginPublishRuleCount(t, fixture.store, "local", 0)
	if remaining := publishedEntriesFromState(t, fixture, "local"); len(remaining) != 0 {
		t.Fatalf("published_entries after last unpublish = %+v, want empty", remaining)
	}
	if _, found, err := fixture.store.GetPluginInstance(ctx, firstInstance.ID); err != nil || !found {
		t.Fatalf("GetPluginInstance() after unpublish = (_, %v, %v), want instance retained", found, err)
	}
}

func TestPluginUnpublishRejectsForeignRuleAndNonHTTPBackend(t *testing.T) {
	t.Parallel()
	httpFixture := newPluginPublishFixture(t, true)
	ctx := WithSystemMutationPrincipal(t.Context(), "system:owner")
	if _, err := callPluginPublish(t, httpFixture.service, ctx, pluginPublishFields(httpFixture.pluginID, "provider-1", "https://emby.example.com", 0)); err != nil {
		t.Fatalf("PublishMutation() error = %v", err)
	}
	owned := mustPluginPublishRule(t, httpFixture.store, "local", 0)
	if _, err := callPluginUnpublish(t, httpFixture.service, ctx, httpFixture.pluginID, "local", 0); err == nil {
		t.Fatal("UnpublishMutation() accepted a missing rule id")
	}
	if _, err := callPluginUnpublish(t, httpFixture.service, ctx, httpFixture.pluginID, "local", owned.ID+99); err == nil {
		t.Fatal("UnpublishMutation() accepted a missing rule")
	}

	frontend := "https://plain.example.com"
	enabled := true
	backends := []HTTPRuleBackend{{Kind: pluginsdk.HTTPBackendKindURL, URL: "http://127.0.0.1:8096"}}
	plain, err := NewRuleService(config.Config{LocalAgentID: "local", EnableLocalAgent: true}, httpFixture.store).Create(ctx, "local", HTTPRuleInput{
		FrontendURL: &frontend, Backends: &backends, Enabled: &enabled,
	})
	if err != nil {
		t.Fatalf("Create() plain HTTP rule error = %v", err)
	}
	if _, err := callPluginUnpublish(t, httpFixture.service, ctx, httpFixture.pluginID, "local", plain.ID); err == nil {
		t.Fatal("UnpublishMutation() accepted a rule that is not a published plugin entry")
	}
	assertPluginPublishRuleCount(t, httpFixture.store, "local", 2)
	if stored := mustPluginPublishRule(t, httpFixture.store, "local", 0); stored.ID != owned.ID {
		t.Fatalf("rejected unpublish mutated owned rule = %+v", stored)
	}

	nonHTTP := newPluginPublishFixture(t, false)
	if _, err := callPluginUnpublish(t, nonHTTP.service, ctx, nonHTTP.pluginID, "local", 1); err == nil {
		t.Fatal("UnpublishMutation() accepted a plugin without an HTTP backend")
	}
	assertPluginPublishRuleCount(t, nonHTTP.store, "local", 0)
}

func TestPluginPublishSecondNodeKeepsOriginalInstanceAndEntry(t *testing.T) {
	t.Parallel()
	fixture := newPluginPublishFixture(t, true)
	addPluginPublishAgent(t, fixture.store, "edge-b")
	ctx := WithSystemMutationPrincipal(t.Context(), "system:owner")
	instanceID := "provider-1"
	firstInstance, err := callPluginPublish(t, fixture.service, ctx, pluginPublishFields(fixture.pluginID, instanceID, "https://emby.example.com", 0))
	if err != nil {
		t.Fatalf("first PublishMutation() error = %v", err)
	}
	completePublishedConfigure(t, fixture)
	first := mustPluginPublishRule(t, fixture.store, "local", 0)
	if firstInstance.ID == "" {
		t.Fatal("first publish returned an empty instance id")
	}

	fields := pluginPublishFields(fixture.pluginID, firstInstance.ID, "https://emby-edge.example.com", 0)
	fields["Targets"] = []string{"edge-b"}
	secondInstance, err := callPluginPublish(t, fixture.service, ctx, fields)
	if err != nil {
		t.Fatalf("second-node PublishMutation() error = %v", err)
	}
	if secondInstance.ID == "" || secondInstance.ID == firstInstance.ID {
		t.Fatalf("second-node instance = %+v, want a new instance", secondInstance)
	}
	if len(secondInstance.Targets) != 1 || secondInstance.Targets[0] != "edge-b" {
		t.Fatalf("second-node instance targets = %v, want [edge-b]", secondInstance.Targets)
	}

	original := mustPluginInstanceByID(t, fixture.store, firstInstance.ID)
	if targets := instanceTargets(t, original); len(targets) != 1 || targets[0] != "local" {
		t.Fatalf("original instance targets = %v, want [local]", targets)
	}
	assertPluginPublishRuleCount(t, fixture.store, "local", 1)
	assertPluginPublishRuleCount(t, fixture.store, "edge-b", 1)
	if stored := mustPluginPublishRule(t, fixture.store, "local", 0); stored.ID != first.ID || stored.FrontendURL != "https://emby.example.com" {
		t.Fatalf("original rule = %+v, want id %d on local", stored, first.ID)
	}
	assertPluginProviderBackend(t, mustPluginPublishRule(t, fixture.store, "local", 0), firstInstance.ID, "default")
	second := mustPluginPublishRule(t, fixture.store, "edge-b", 0)
	if second.FrontendURL != "https://emby-edge.example.com" || !second.Enabled {
		t.Fatalf("second-node rule = %+v", second)
	}
	assertPluginProviderBackend(t, second, secondInstance.ID, "default")

	entries := publishedEntriesFromState(t, fixture, "local")
	if len(entries) != 2 {
		t.Fatalf("published_entries = %+v, want both original and second-node entries", entries)
	}
	if !containsPublishedEntry(entries, first.ID, "local", "https://emby.example.com") {
		t.Fatalf("published_entries = %+v, missing original local entry", entries)
	}
	if !containsPublishedEntry(entries, second.ID, "edge-b", "https://emby-edge.example.com") {
		t.Fatalf("published_entries = %+v, missing second-node entry", entries)
	}
	if !containsHTTPRuleBindingDetail(t, firstInstanceFromDetail(t, fixture, firstInstance.ID), first.ID, "local") {
		t.Fatalf("original instance projection missing http_rule %d on local", first.ID)
	}
	if !containsHTTPRuleBindingDetail(t, secondInstance, second.ID, "edge-b") {
		t.Fatalf("second instance projection missing http_rule %d on edge-b", second.ID)
	}

	instances, err := fixture.store.ListPluginInstances(t.Context(), fixture.pluginID)
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 2 {
		t.Fatalf("instances = %+v, want original plus one new node instance", instances)
	}

	fields["RuleID"] = first.ID
	fields["Targets"] = []string{"edge-b"}
	fields["FrontendURL"] = "https://emby-moved.example.com"
	if _, err := callPluginPublish(t, fixture.service, ctx, fields); err == nil {
		t.Fatal("updating the original rule via a different node was accepted")
	}
	if stored := mustPluginPublishRule(t, fixture.store, "local", 0); stored.ID != first.ID || stored.FrontendURL != "https://emby.example.com" {
		t.Fatalf("rejected retarget update mutated original rule = %+v", stored)
	}
	if targets := instanceTargets(t, mustPluginInstanceByID(t, fixture.store, firstInstance.ID)); len(targets) != 1 || targets[0] != "local" {
		t.Fatalf("rejected retarget update mutated original instance targets = %v", targets)
	}
}

func TestPluginPublishUpdatesSecondNodeEntryFromOriginalInstanceID(t *testing.T) {
	t.Parallel()
	fixture := newPluginPublishFixture(t, true)
	addPluginPublishAgent(t, fixture.store, "edge-b")
	ctx := WithSystemMutationPrincipal(t.Context(), "system:owner")
	firstInstance, err := callPluginPublish(t, fixture.service, ctx, pluginPublishFields(fixture.pluginID, "provider-1", "https://emby.example.com", 0))
	if err != nil {
		t.Fatalf("first PublishMutation() error = %v", err)
	}
	completePublishedConfigure(t, fixture)
	first := mustPluginPublishRule(t, fixture.store, "local", 0)

	fields := pluginPublishFields(fixture.pluginID, firstInstance.ID, "https://emby-edge.example.com", 0)
	fields["Targets"] = []string{"edge-b"}
	secondInstance, err := callPluginPublish(t, fixture.service, ctx, fields)
	if err != nil {
		t.Fatalf("second-node PublishMutation() error = %v", err)
	}
	second := mustPluginPublishRule(t, fixture.store, "edge-b", 0)

	fields = pluginPublishFields(fixture.pluginID, firstInstance.ID, "https://emby-edge-v2.example.com", second.ID)
	fields["Targets"] = []string{"edge-b"}
	updated, err := callPluginPublish(t, fixture.service, ctx, fields)
	if err != nil {
		t.Fatalf("update second-node entry via original instance_id error = %v", err)
	}
	if updated.ID != secondInstance.ID {
		t.Fatalf("updated instance = %+v, want backing instance %s", updated, secondInstance.ID)
	}
	if stored := mustPluginPublishRule(t, fixture.store, "edge-b", 0); stored.ID != second.ID || stored.FrontendURL != "https://emby-edge-v2.example.com" {
		t.Fatalf("updated second-node rule = %+v, want id %d and new frontend_url", stored, second.ID)
	}
	assertPluginPublishRuleCount(t, fixture.store, "edge-b", 1)
	assertPluginPublishRuleCount(t, fixture.store, "local", 1)
	if stored := mustPluginPublishRule(t, fixture.store, "local", 0); stored.ID != first.ID || stored.FrontendURL != "https://emby.example.com" {
		t.Fatalf("original rule = %+v, want unchanged local entry", stored)
	}
	if targets := instanceTargets(t, mustPluginInstanceByID(t, fixture.store, firstInstance.ID)); len(targets) != 1 || targets[0] != "local" {
		t.Fatalf("original instance targets = %v, want [local]", targets)
	}
	if targets := instanceTargets(t, mustPluginInstanceByID(t, fixture.store, secondInstance.ID)); len(targets) != 1 || targets[0] != "edge-b" {
		t.Fatalf("second instance targets = %v, want [edge-b]", targets)
	}
	entries := publishedEntriesFromState(t, fixture, "local")
	if !containsPublishedEntry(entries, first.ID, "local", "https://emby.example.com") {
		t.Fatalf("published_entries = %+v, missing original local entry", entries)
	}
	if !containsPublishedEntry(entries, second.ID, "edge-b", "https://emby-edge-v2.example.com") {
		t.Fatalf("published_entries = %+v, missing updated second-node entry", entries)
	}
}

func TestPluginPublishSurvivesCompleteConfigureAndDeleteInstanceCascadesPublishedRules(t *testing.T) {
	t.Parallel()
	fixture := newPluginPublishFixture(t, true)
	ctx := WithSystemMutationPrincipal(t.Context(), "system:owner")
	instanceID := "provider-1"
	fields := pluginPublishFields(fixture.pluginID, instanceID, "https://emby.example.com", 0)
	if _, err := callPluginPublish(t, fixture.service, ctx, fields); err != nil {
		t.Fatalf("first PublishMutation() error = %v", err)
	}
	completePublishedConfigure(t, fixture)
	first := mustPluginPublishRule(t, fixture.store, "local", 0)
	fields["RuleID"] = first.ID
	fields["FrontendURL"] = "https://emby-updated.example.com"
	updated, err := callPluginPublish(t, fixture.service, ctx, fields)
	if err != nil {
		t.Fatalf("update after CompleteConfigure() error = %v", err)
	}
	assertPluginPublishRuleCount(t, fixture.store, "local", 1)
	if stored := mustPluginPublishRule(t, fixture.store, "local", 0); stored.ID != first.ID || stored.FrontendURL != "https://emby-updated.example.com" {
		t.Fatalf("updated rule = %+v, want id %d and new frontend_url", stored, first.ID)
	}
	if !containsHTTPRuleBindingDetail(t, updated, first.ID, "local") {
		t.Fatalf("projected bindings after update = %+v, want http_rule %d", updated.Bindings, first.ID)
	}

	delete(fields, "RuleID")
	fields["FrontendURL"] = "https://emby-second.example.com"
	if _, err := callPluginPublish(t, fixture.service, ctx, fields); err != nil {
		t.Fatalf("second-domain after CompleteConfigure() error = %v", err)
	}
	assertPluginPublishRuleCount(t, fixture.store, "local", 2)
	second := mustPluginPublishRule(t, fixture.store, "local", 1)
	instance := mustPluginInstance(t, fixture.store, fixture.pluginID)
	if containsHTTPRuleBinding(instance, first.ID, "local") || containsHTTPRuleBinding(instance, second.ID, "local") {
		t.Fatalf("persisted bindings = %s after applied republish", instance.BindingsJSON)
	}
	if err := fixture.service.DeleteInstanceMutation(ctx, PluginDeleteInstanceRequest{
		PluginID: fixture.pluginID, InstanceID: instance.ID, ActorID: "admin", Actor: pluginPublishAdmin(),
	}); err != nil {
		t.Fatalf("DeleteInstance() error = %v", err)
	}
	assertPluginPublishRuleCount(t, fixture.store, "local", 0)
	if _, found, err := fixture.store.GetPluginInstance(ctx, instance.ID); err != nil || found {
		t.Fatalf("GetPluginInstance() = (_, %v, %v), want deleted", found, err)
	}
}

type pluginPublishHarness struct {
	pluginID string
	store    *storage.GormStore
	service  *PluginService
}

func newPluginPublishFixture(t *testing.T, httpBackend bool) pluginPublishHarness {
	t.Helper()
	store := newServiceOwnerStore(t)
	if err := store.SaveAgent(t.Context(), storage.AgentRow{
		ID: "local", Name: "local", Version: "1.0.0", Platform: runtime.GOOS + "-" + runtime.GOARCH,
		CapabilitiesJSON: `["package_manifest_v1"]`,
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 17, 6, 0, 0, 0, time.UTC)
	if err := store.UpsertBuiltinResourceGroup(t.Context(), storage.ResourceGroupRow{
		ID: "default", Name: "default", Description: "default", Builtin: true, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	cacheRoot := filepath.Join(t.TempDir(), "plugins", "packages")
	if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = marketplace.DiscardVerifiedCacheRoot(cacheRoot) })
	pluginID := "official.publish"
	if !httpBackend {
		pluginID = "official.waf"
	}
	seed := seedInstalledPublishPackage(t, store, cacheRoot, pluginID, httpBackend)
	svc := NewPluginServiceWithValidator(store, seed.validator, cacheRoot)
	svc.ConfigureRevisionMutations(config.Config{LocalAgentID: "local", EnableLocalAgent: true}, store)
	return pluginPublishHarness{pluginID: pluginID, store: store, service: svc}
}

type seededPublishPackage struct {
	validator *plugins.Validator
}

func seedInstalledPublishPackage(t *testing.T, store *storage.GormStore, cacheRoot, pluginID string, httpBackend bool) seededPublishPackage {
	t.Helper()
	key := publishFixtureSigningKey()
	publicKey := base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey))
	fingerprint, err := marketplace.SourceSignerFingerprint(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	validator := plugins.NewValidator(plugins.ValidatorOptions{
		HostVersion:         "0.0.0-dev",
		TrustedSigners:      map[string]ed25519.PublicKey{"test-fixture": key.Public().(ed25519.PublicKey)},
		TrustedSignerPolicy: plugins.TrustedSignerPolicyExact,
		TargetGOOS:          runtime.GOOS,
		TargetGOARCH:        runtime.GOARCH,
	})
	root := writePublishPackage(t, pluginID, httpBackend, key)
	validated, err := validator.ValidatePackage(root, plugins.PackageExpectation{
		ID: pluginID, Version: "1.0.0", SignatureKeyID: "test-fixture",
	})
	if err != nil {
		t.Fatalf("ValidatePackage() error = %v", err)
	}
	trust := marketplace.SignatureTrust{
		SourceID: "publish-fixture", SourceKind: marketplace.SourceKindCustom,
		KeyID: "test-fixture", PublicKey: publicKey, Fingerprint: fingerprint,
	}
	cachePath, err := marketplace.ImportVerifiedPackage(cacheRoot, validated, validator, trust)
	if err != nil {
		t.Fatalf("ImportVerifiedPackage() error = %v", err)
	}
	now := time.Date(2026, 8, 17, 6, 0, 0, 0, time.UTC)
	manifestJSON, err := json.Marshal(validated.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	schemaJSON, err := json.Marshal(validated.ConfigSchema)
	if err != nil {
		t.Fatal(err)
	}
	cleanupJSON, err := json.Marshal(validated.Manifest.Cleanup)
	if err != nil {
		t.Fatal(err)
	}
	row := storage.PluginPackageRow{
		Digest: validated.Digest, PluginID: pluginID, Version: "1.0.0",
		SourceID: trust.SourceID, SourceKind: trust.SourceKind, SignatureKeyID: trust.KeyID,
		SignaturePublicKey: trust.PublicKey, SignatureFingerprint: trust.Fingerprint,
		CachePath: cachePath, ManifestJSON: string(manifestJSON), ConfigSchemaJSON: string(schemaJSON),
		VerifiedAt: now,
	}
	row.Identity = storage.PluginPackageIdentity(validated.Digest, trust.SourceID, trust.Fingerprint)
	packageRow, artifacts, err := storage.ProjectPluginPackage(row, validated.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	operation := storage.PluginOperationRow{
		ID: "op-install-" + pluginID, PluginID: pluginID, Kind: "install", Status: "succeeded",
		TargetPackageDigest: validated.Digest, TargetPackageIdentity: packageRow.Identity,
		TargetSignatureKeyID: trust.KeyID, TargetSignaturePublicKey: trust.PublicKey,
		TargetSignatureFingerprint: trust.Fingerprint, ActorID: "admin", AgentResultsJSON: `[]`,
		SourceID: trust.SourceID, SourceKind: trust.SourceKind, CreatedAt: now, CompletedAt: &now,
	}
	if err := storage.BindPluginOperationPackage(&operation, packageRow); err != nil {
		t.Fatal(err)
	}
	installed := storage.InstalledPluginRow{
		PluginID: pluginID, ActivePackageDigest: validated.Digest, ActivePackageIdentity: packageRow.Identity,
		RuntimeKind: validated.Manifest.Runtime.Kind, RuntimeABI: validated.Manifest.Runtime.ABI,
		HostScope: validated.Manifest.Runtime.HostScope, ActiveSourceID: trust.SourceID,
		ActiveSourceKind: trust.SourceKind, ActiveSignatureKeyID: trust.KeyID,
		ActiveSignaturePublicKey: trust.PublicKey, ActiveSignatureFingerprint: trust.Fingerprint,
		DesiredLifecycle: "disabled", CurrentLifecycle: "disabled", CleanupPolicyJSON: string(cleanupJSON),
		LastOperationID: operation.ID, StateVersion: 1, InstalledAt: now, UpdatedAt: now,
	}
	if err := store.InstallPlugin(t.Context(), storage.PluginInstallTransaction{
		Package: packageRow, Artifacts: artifacts, Installed: installed, Operation: operation,
		Audit: storage.AuditEventRow{
			ID: "audit-install-" + pluginID, ActorID: "admin", Action: "plugin.install",
			TargetKind: "plugin", TargetID: pluginID, Result: "success", MetadataJSON: `{}`, CreatedAt: now,
		},
	}); err != nil {
		t.Fatalf("InstallPlugin() error = %v", err)
	}
	return seededPublishPackage{validator: validator}
}

func writePublishPackage(t *testing.T, pluginID string, httpBackend bool, key ed25519.PrivateKey) string {
	t.Helper()
	root := t.TempDir()
	schema := `{"type":"object","additionalProperties":false}`
	writePublishFile(t, root, plugins.ConfigSchemaFile, schema)
	if httpBackend {
		artifact, dest := publishRPCArtifact(t, root)
		sum := sha256.Sum256(artifact)
		writePublishFile(t, root, plugins.PackageManifestFile, fmtPublishRPCManifest(pluginID, dest, hex.EncodeToString(sum[:]), int64(len(artifact))))
	} else {
		artifact := publishWASMArtifact(t)
		sum := sha256.Sum256(artifact)
		writePublishBytes(t, root, "artifacts/policy.wasm", artifact)
		writePublishFile(t, root, plugins.PackageManifestFile, fmtPublishWASMManifest(pluginID, hex.EncodeToString(sum[:]), int64(len(artifact))))
	}
	digest, err := plugins.ComputePackageDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	writePublishFile(t, root, plugins.PackageDigestFile, digest+"\n")
	writePublishFile(t, root, plugins.PackageSignatureFile, base64.StdEncoding.EncodeToString(ed25519.Sign(key, []byte(digest)))+"\n")
	return root
}

func publishRPCArtifact(t *testing.T, root string) ([]byte, string) {
	t.Helper()
	source := publishRPCArtifactSource(t)
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	name := "plugin"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	dest := "artifacts/" + runtime.GOOS + "-" + runtime.GOARCH + "/" + name
	writePublishBytes(t, root, dest, data)
	return data, dest
}

func publishRPCArtifactSource(t *testing.T) string {
	t.Helper()
	candidates := []string{"true", "sleep"}
	if runtime.GOOS == "windows" {
		candidates = []string{filepath.Join(os.Getenv("SystemRoot"), "System32", "where.exe"), filepath.Join(os.Getenv("SystemRoot"), "System32", "hostname.exe")}
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() && info.Size() > 0 {
			return candidate
		}
	}
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func publishWASMArtifact(t *testing.T) []byte {
	t.Helper()
	name := filepath.Join("..", "..", "..", "..", "..", "plugin-sdk", "policy", "v1", "testdata", "compatible_guest.wasm.hex")
	encoded, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := hex.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	return artifact
}

func fmtPublishRPCManifest(pluginID, artifactPath, digest string, size int64) string {
	return fmt.Sprintf(`schema_version: 1
id: %s
version: 1.0.0
name: Publish HTTP
compatibility:
  host: "*"
  agent: "*"
runtime:
  kind: rpc-service
  abi: nre:rpc/v1
  host_scope: agent
  entry: plugin
artifacts:
  - path: %s
    sha256: %s
    size: %d
    mode: executable
    goos: %s
    goarch: %s
extension_points: [http.backend-provider]
http_backend_providers:
  - {id: default, display_name: Default}
permissions: [http.outbound]
config_schema: config.schema.json
resource_budget:
  timeout_ms: 2000
  memory_bytes: 1048576
  concurrency: 8
  input_bytes: 65536
  output_bytes: 4096
  cpu_millis: 100
  restarts: 1
failure_policy:
  on_error: fail-open
  on_budget: fail-open
  restart: on-failure
  core_fallback: preserve
signature:
  algorithm: ed25519
  key_id: test-fixture
  file: package.sig
cleanup:
  instances: delete
  config: delete
  owned_data: delete
  grants: delete
  shared_refs: retain
  audit_events: retain
`, pluginID, artifactPath, digest, size, runtime.GOOS, runtime.GOARCH)
}

func fmtPublishWASMManifest(pluginID, digest string, size int64) string {
	return fmt.Sprintf(`schema_version: 1
id: %s
version: 1.0.0
name: WAF
compatibility:
  host: "*"
  agent: "*"
runtime:
  kind: wasm-policy
  abi: nre:policy/v1
  host_scope: agent
  entry: artifacts/policy.wasm
  policy_kind: waf
artifacts:
  - path: artifacts/policy.wasm
    sha256: %s
    size: %d
    mode: wasm
extension_points: [http.request]
permissions: [http.inspect]
config_schema: config.schema.json
resource_budget:
  timeout_ms: 2
  memory_bytes: 1048576
  concurrency: 8
  input_bytes: 65536
  output_bytes: 4096
failure_policy:
  on_error: fail-open
  on_budget: fail-open
  restart: never
  core_fallback: preserve
signature:
  algorithm: ed25519
  key_id: test-fixture
  file: package.sig
cleanup:
  instances: delete
  config: delete
  owned_data: delete
  grants: delete
  shared_refs: retain
  audit_events: retain
`, pluginID, digest, size)
}

func writePublishFile(t *testing.T, root, name, value string) {
	t.Helper()
	writePublishBytes(t, root, name, []byte(value))
}

func writePublishBytes(t *testing.T, root, name string, value []byte) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, value, 0o644); err != nil {
		t.Fatal(err)
	}
}

func publishFixtureSigningKey() ed25519.PrivateKey {
	seed := sha256.Sum256([]byte("nre-plugin-publish-contract-fixture"))
	return ed25519.NewKeyFromSeed(seed[:])
}

func pluginPublishAdmin() authz.Actor {
	return authz.Actor{ID: "admin", Username: "admin", Permissions: []string{authz.PermissionSystemAdmin, authz.PermissionAll}}
}

func pluginPublishFields(pluginID, instanceID, frontendURL string, ruleID int) map[string]any {
	fields := map[string]any{
		"PluginID":        pluginID,
		"InstanceID":      instanceID,
		"ResourceGroupID": "default",
		"Targets":         []string{"local"},
		"PolicyChains":    []string{},
		"Config":          json.RawMessage(`{}`),
		"FrontendURL":     frontendURL,
		"ActorID":         "admin",
		"Actor":           pluginPublishAdmin(),
	}
	if ruleID > 0 {
		fields["RuleID"] = ruleID
	}
	return fields
}

func callPluginUnpublish(t *testing.T, svc *PluginService, ctx context.Context, pluginID, agentID string, ruleID int) (PluginInstanceDetail, error) {
	t.Helper()
	instance, _, err := svc.UnpublishMutation(ctx, PluginUnpublishRequest{
		PluginID: pluginID,
		Targets:  []string{agentID},
		RuleID:   ruleID,
		ActorID:  "admin",
		Actor:    pluginPublishAdmin(),
	})
	return instance, err
}

func callPluginPublish(t *testing.T, svc *PluginService, ctx context.Context, fields map[string]any) (PluginInstanceDetail, error) {
	t.Helper()
	chains := []string{}
	if raw, ok := fields["PolicyChains"].([]string); ok {
		chains = raw
	}
	pluginID, _ := fields["PluginID"].(string)
	instanceID, _ := fields["InstanceID"].(string)
	resourceGroupID, _ := fields["ResourceGroupID"].(string)
	frontendURL, _ := fields["FrontendURL"].(string)
	actorID, _ := fields["ActorID"].(string)
	actor, _ := fields["Actor"].(authz.Actor)
	configPayload, _ := fields["Config"].(json.RawMessage)
	if len(configPayload) == 0 {
		configPayload = json.RawMessage(`{}`)
	}
	ruleID, _ := fields["RuleID"].(int)
	instance, _, err := svc.PublishMutation(ctx, PluginConfigureRequest{
		PluginID:              pluginID,
		InstanceID:            instanceID,
		ResourceGroupID:       resourceGroupID,
		Targets:               fields["Targets"],
		PolicyChains:          &chains,
		Config:                configPayload,
		FrontendURL:           frontendURL,
		RuleID:                ruleID,
		ActorID:               actorID,
		Actor:                 actor,
		PublishDesiredEnabled: true,
	}, frontendURL, ruleID)
	return instance, err
}

func publishedEntriesFromState(t *testing.T, fixture pluginPublishHarness, agentID string) []pluginPublishedEntryView {
	t.Helper()
	if detail, err := fixture.service.Detail(t.Context(), fixture.pluginID); err == nil {
		if entries := publishedEntriesFromDetail(t, detail); len(entries) > 0 {
			return entries
		}
	}
	instance := mustPluginInstance(t, fixture.store, fixture.pluginID)
	rules, err := fixture.store.ListHTTPRules(t.Context(), agentID)
	if err != nil {
		t.Fatal(err)
	}
	result := make([]pluginPublishedEntryView, 0, len(rules))
	for _, rule := range rules {
		if !pluginRuleBacksInstance(httpRuleFromRow(rule), instance.ID) {
			continue
		}
		result = append(result, pluginPublishedEntryView{
			RuleID: rule.ID, AgentID: agentID, FrontendURL: rule.FrontendURL, Enabled: rule.Enabled,
		})
	}
	return result
}

type pluginPublishedEntryView struct {
	RuleID      int
	AgentID     string
	FrontendURL string
	Enabled     bool
	Accessible  bool
}

func publishedEntriesFromDetail(t *testing.T, detail PluginDetail) []pluginPublishedEntryView {
	t.Helper()
	wire, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		PublishedEntries []struct {
			RuleID      int    `json:"rule_id"`
			AgentID     string `json:"agent_id"`
			FrontendURL string `json:"frontend_url"`
			Enabled     bool   `json:"enabled"`
			Accessible  bool   `json:"accessible"`
		} `json:"published_entries"`
	}
	if err := json.Unmarshal(wire, &payload); err != nil {
		t.Fatal(err)
	}
	result := make([]pluginPublishedEntryView, 0, len(payload.PublishedEntries))
	for _, entry := range payload.PublishedEntries {
		result = append(result, pluginPublishedEntryView{
			RuleID: entry.RuleID, AgentID: entry.AgentID, FrontendURL: entry.FrontendURL,
			Enabled: entry.Enabled, Accessible: entry.Accessible,
		})
	}
	return result
}

func mustPluginInstance(t *testing.T, store *storage.GormStore, pluginID string) storage.PluginInstanceRow {
	t.Helper()
	instances, err := store.ListPluginInstances(t.Context(), pluginID)
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 {
		t.Fatalf("instances = %+v, want exactly one", instances)
	}
	return instances[0]
}

func mustPluginInstanceByID(t *testing.T, store *storage.GormStore, instanceID string) storage.PluginInstanceRow {
	t.Helper()
	instance, found, err := store.GetPluginInstance(t.Context(), instanceID)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatalf("plugin instance %s is not found", instanceID)
	}
	return instance
}

func addPluginPublishAgent(t *testing.T, store *storage.GormStore, agentID string) {
	t.Helper()
	if err := store.SaveAgent(t.Context(), storage.AgentRow{
		ID: agentID, Name: agentID, Version: "1.0.0", Platform: runtime.GOOS + "-" + runtime.GOARCH,
		CapabilitiesJSON: marshalStringArray(defaultLocalCapabilities),
	}); err != nil {
		t.Fatal(err)
	}
}

func containsPublishedEntry(entries []pluginPublishedEntryView, ruleID int, agentID, frontendURL string) bool {
	for _, entry := range entries {
		if entry.RuleID == ruleID && entry.AgentID == agentID && entry.FrontendURL == frontendURL && entry.Enabled {
			return true
		}
	}
	return false
}

func firstInstanceFromDetail(t *testing.T, fixture pluginPublishHarness, instanceID string) PluginInstanceDetail {
	t.Helper()
	detail, err := fixture.service.Detail(t.Context(), fixture.pluginID)
	if err != nil {
		t.Fatal(err)
	}
	for _, instance := range detail.Instances {
		if instance.ID == instanceID {
			return instance
		}
	}
	t.Fatalf("detail instances = %+v, missing %s", detail.Instances, instanceID)
	return PluginInstanceDetail{}
}

func instanceTargets(t *testing.T, instance storage.PluginInstanceRow) []string {
	t.Helper()
	var targets []string
	if err := json.Unmarshal([]byte(instance.TargetJSON), &targets); err != nil {
		t.Fatalf("instance targets %s: %v", instance.TargetJSON, err)
	}
	return targets
}

func completePublishedConfigure(t *testing.T, fixture pluginPublishHarness) {
	t.Helper()
	instance := mustPluginInstance(t, fixture.store, fixture.pluginID)
	if instance.PendingOperationID == "" || instance.PendingVersion == 0 {
		return
	}
	installed, ok, err := fixture.store.GetInstalledPlugin(t.Context(), fixture.pluginID)
	if err != nil || !ok {
		t.Fatalf("GetInstalledPlugin() ok=%v err=%v", ok, err)
	}
	if _, err := fixture.service.CompleteConfigure(t.Context(), PluginApplyResult{
		PluginID: fixture.pluginID, InstanceID: instance.ID, OperationID: instance.PendingOperationID,
		TargetRevision: installed.PendingRevision, TargetDigest: installed.PendingTargetDigest,
		ConfigVersion: instance.PendingVersion, ActorID: "admin", Applied: true, AgentResults: map[string]any{},
	}); err != nil {
		t.Fatalf("CompleteConfigure() error = %v", err)
	}
}

func containsHTTPRuleBindingDetail(t *testing.T, instance PluginInstanceDetail, ruleID int, agentID string) bool {
	t.Helper()
	wantID := strconv.Itoa(ruleID)
	for _, binding := range instance.Bindings {
		if binding.Consumer.Kind == storage.PluginDependencyConsumerHTTPRule && binding.Consumer.ID == wantID && binding.TargetAgentID == agentID {
			return true
		}
	}
	return false
}

func containsHTTPRuleBinding(instance storage.PluginInstanceRow, ruleID int, agentID string) bool {
	bindings, err := storage.CanonicalPluginInstanceBindings(instance.BindingsJSON)
	if err != nil {
		return false
	}
	wantID := strconv.Itoa(ruleID)
	for _, binding := range bindings {
		if binding.Consumer.Kind == storage.PluginDependencyConsumerHTTPRule && binding.Consumer.ID == wantID && binding.TargetAgentID == agentID {
			return true
		}
	}
	return false
}

func assertPluginPublishRuleCount(t *testing.T, store *storage.GormStore, agentID string, want int) {
	t.Helper()
	rules, err := store.ListHTTPRules(t.Context(), agentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != want {
		t.Fatalf("HTTP rules = %+v, want %d", rules, want)
	}
}

func mustPluginPublishRule(t *testing.T, store *storage.GormStore, agentID string, index int) storage.HTTPRuleRow {
	t.Helper()
	rules, err := store.ListHTTPRules(t.Context(), agentID)
	if err != nil {
		t.Fatal(err)
	}
	if index < 0 || index >= len(rules) {
		t.Fatalf("HTTP rules = %+v, missing index %d", rules, index)
	}
	return rules[index]
}

func assertPluginProviderBackend(t *testing.T, rule storage.HTTPRuleRow, instanceID, providerID string) {
	t.Helper()
	var backends []HTTPRuleBackend
	if err := json.Unmarshal([]byte(rule.BackendsJSON), &backends); err != nil {
		t.Fatalf("backends %s: %v", rule.BackendsJSON, err)
	}
	if len(backends) != 1 || backends[0].Kind != pluginsdk.HTTPBackendKindPluginProvider || backends[0].PluginProvider == nil {
		t.Fatalf("backends = %s, want one plugin_provider", rule.BackendsJSON)
	}
	if backends[0].PluginProvider.InstanceID != instanceID || backends[0].PluginProvider.ProviderID != providerID {
		t.Fatalf("plugin_provider = %+v, want instance %s provider %s", backends[0].PluginProvider, instanceID, providerID)
	}
}
