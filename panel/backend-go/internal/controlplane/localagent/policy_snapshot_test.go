package localagent

import (
	"encoding/json"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestEmbeddedSnapshotCarriesPolicyDefinitionsAttachmentsAndTrust(t *testing.T) {
	snapshot := storage.Snapshot{
		PluginPolicies: []storage.PluginPolicy{{
			ID: "shared-policy", Revision: 7,
			Stages: []storage.PolicyStage{{
				Kind: "waf", PolicyID: "shared-policy-waf", PluginID: "official.waf", PluginVersion: "1.0.0",
				InstanceID: "waf-1", PackageDigest: "package-digest", ArtifactPath: "packages/signer/digest/policy.wasm",
				ArtifactDigest: "artifact-digest", SignatureVerified: true, SignerKeyID: "official", SignerFingerprint: "fingerprint",
				ABI: "nre:policy/v1", ExtensionPoints: []string{"http.request"}, GrantedScopes: []string{"http.inspect"},
				Config: json.RawMessage(`{"mode":"block"}`), ResourceBudget: storage.PolicyResourceBudget{TimeoutMS: 2, MemoryBytes: 1 << 20, Concurrency: 1, InputBytes: 4096, OutputBytes: 4096},
				FailurePolicy: storage.PolicyFailurePolicy{OnError: "fail-closed", OnBudget: "fail-closed", Restart: "never", CoreFallback: "preserve"},
			}},
		}},
		Rules: []storage.HTTPRule{{
			ID: 1, FrontendURL: "https://media.example.test", TrustedProxyRanges: []string{"10.0.0.0/8"},
			PolicyRef: &storage.PolicyRef{ID: "shared-policy", Overlay: json.RawMessage(`{"site":"media"}`)},
		}},
		L4Rules: []storage.L4Rule{{
			ID: 2, Protocol: "udp", ListenHost: "127.0.0.1", ListenPort: 9000,
			Tuning:    storage.L4Tuning{ProxyProtocol: storage.L4ProxyProtocolTuning{Decode: true, TrustedPeers: []string{"192.0.2.0/24"}}},
			PolicyRef: &storage.PolicyRef{ID: "shared-policy"},
		}},
	}
	embedded := toEmbeddedSnapshot(snapshot)
	if len(embedded.PluginPolicies) != 1 || embedded.PluginPolicies[0].ID != "shared-policy" || len(embedded.PluginPolicies[0].Stages) != 1 {
		t.Fatalf("embedded policies = %+v", embedded.PluginPolicies)
	}
	if len(embedded.Rules) != 1 || embedded.Rules[0].PolicyRef == nil || string(embedded.Rules[0].PolicyRef.Overlay) != `{"site":"media"}` {
		t.Fatalf("embedded HTTP policy ref = %+v", embedded.Rules)
	}
	if ranges := embedded.Rules[0].TrustedProxyRanges; len(ranges) != 1 || ranges[0] != "10.0.0.0/8" {
		t.Fatalf("embedded HTTP trusted ranges = %+v", ranges)
	}
	if peers := embedded.L4Rules[0].Tuning.ProxyProtocol.TrustedPeers; len(peers) != 1 || peers[0] != "192.0.2.0/24" {
		t.Fatalf("embedded L4 trusted peers = %+v", peers)
	}
	payload, err := json.Marshal(embedded)
	if err != nil {
		t.Fatalf("json.Marshal(embedded) error = %v", err)
	}
	if string(payload) == "" || embedded.PluginPolicies == nil {
		t.Fatalf("embedded snapshot lost explicit policy presence: %s", payload)
	}
}
