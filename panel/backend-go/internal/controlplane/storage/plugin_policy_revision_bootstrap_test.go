//go:build !fast && !integration

package storage

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/marketplace"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestAutomaticPolicyCatalogInitializesRemoteRevision(t *testing.T) {
	store, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	cache := filepath.Join(store.dataRoot, "plugins", "packages")
	seed := seedPolicyCatalogPackage(t, store, cache, policyCatalogSeed{
		pluginID: "official.waf", instanceID: "official.waf-default", digestSeed: "a",
		runtime: plugins.Runtime{Kind: pluginsdk.RuntimeRPCService, ABI: pluginsdk.RPCABIV1, HostScope: pluginsdk.HostScopeControlPlane, Entry: "plugin", PolicyKind: "waf",
			Policy: &pluginsdk.RuntimePolicy{Kind: pluginsdk.RuntimeWASMPolicy, ABI: pluginsdk.PolicyABIV1, HostScope: pluginsdk.HostScopeAgent, Entry: "artifacts/policy.wasm",
				ResourceBudget: plugins.ResourceBudget{TimeoutMS: 2, MemoryBytes: 1048576, Concurrency: 8, InputBytes: 65536, OutputBytes: 4096},
				FailurePolicy:  plugins.FailurePolicy{OnError: "fail-closed", OnBudget: "fail-closed", Restart: "never", CoreFallback: "preserve"}}},
		extensions: []string{pluginsdk.ExtensionUIRoute, pluginsdk.ExtensionHTTPRequest}, targets: `[]`, policyChains: `[]`, desired: "enabled", current: "active",
	}, time.Now().UTC())
	if _, err := marketplace.NewVerifiedCache(cache, plugins.NewValidator(plugins.ValidatorOptions{}), nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = marketplace.DiscardVerifiedCacheRoot(cache) })
	for _, id := range []string{"edge-a", "edge-b"} {
		if err := store.db.Create(&AgentRow{ID: id}).Error; err != nil {
			t.Fatal(err)
		}
	}
	// A pre-existing zero fence and a new Agent both receive the globally
	// projected policy; neither may publish a zero policy revision.
	if err := store.db.Create(&PluginPolicyAgentRevisionRow{AgentID: "edge-a", Revision: 0}).Error; err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"edge-a", "edge-b"} {
		if err := store.EnsureAgentPluginPolicyCatalog(t.Context(), id); err != nil {
			t.Fatal(err)
		}
		policies, err := store.LoadAgentPluginPolicies(t.Context(), id)
		if err != nil || len(policies) != 1 || policies[0].Revision <= 0 {
			t.Fatalf("%s catalog = %+v, %v", id, policies, err)
		}
		revision := policies[0].Revision
		if err := store.EnsureAgentPluginPolicyCatalog(t.Context(), id); err != nil {
			t.Fatal(err)
		}
		policies, err = store.LoadAgentPluginPolicies(t.Context(), id)
		if err != nil || policies[0].Revision != revision {
			t.Fatalf("unchanged catalog advanced: %+v, %v", policies, err)
		}
	}
	agents, err := store.pluginMutationPolicyAgents(t.Context(), PluginMutation{PluginID: seed.pluginID})
	if err != nil || !slices.Contains(agents, "edge-a") || !slices.Contains(agents, "edge-b") || !slices.Contains(agents, "local") {
		t.Fatalf("automatic policy mutation fences = %v, %v", agents, err)
	}
}
