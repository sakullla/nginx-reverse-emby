package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/authz"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

func TestPluginRuntimeHTTPDTOExposesStableRuntimeAndArtifactFields(t *testing.T) {
	packageDetail := service.PluginPackageDetail{
		Digest: strings.Repeat("a", 64), Version: "1.0.0",
		Runtime:        plugins.Runtime{Kind: "wasm-policy", ABI: "nre:policy/v1", HostScope: "agent", Entry: "artifacts/policy.wasm", PolicyKind: "waf"},
		Artifacts:      []service.PluginArtifactDetail{{Path: "artifacts/policy.wasm", SHA256: strings.Repeat("b", 64), Size: 64, Mode: "readonly"}},
		ResourceBudget: plugins.ResourceBudget{TimeoutMS: 10, MemoryBytes: 65536, Concurrency: 2, InputBytes: 1024, OutputBytes: 1024},
		FailurePolicy:  plugins.FailurePolicy{OnError: "fail-closed", OnBudget: "fail-closed", Restart: "never", CoreFallback: "preserve-old"},
		Signature:      plugins.Signature{Algorithm: "ed25519", KeyID: "release-key", File: "package.sig"},
		Manifest:       plugins.Manifest{ID: "runtime.http"}, ConfigSchema: map[string]any{"type": "object"}, Permissions: []string{}, PermissionDiff: service.PluginPermissionDiff{Added: []string{}, Removed: []string{}},
	}
	summary := service.PluginSummary{PluginID: "runtime.http", ActivePackageDigest: packageDetail.Digest, RuntimeKind: "wasm-policy", RuntimeABI: "nre:policy/v1", HostScope: "agent"}
	api := &pluginReadAPIFake{installed: []service.PluginSummary{summary}, detail: service.PluginDetail{Plugin: summary, Package: packageDetail, Instances: []service.PluginInstanceDetail{}, Grants: []service.PluginGrantDetail{}, AgentStatuses: []service.PluginAgentStatus{}}}

	request := httptest.NewRequest(http.MethodGet, "/panel-api/plugins/runtime.http", nil)
	request = request.WithContext(context.WithValue(request.Context(), actorContextKey{}, authz.Actor{ID: "admin", Permissions: []string{authz.PermissionSystemAdmin}}))
	request.SetPathValue("id", "runtime.http")
	response := httptest.NewRecorder()
	Dependencies{PluginService: api}.handlePlugin(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	plugin := payload["plugin"].(map[string]any)
	pkg := payload["package"].(map[string]any)
	runtime := pkg["runtime"].(map[string]any)
	artifacts := pkg["artifacts"].([]any)
	if plugin["runtime_kind"] != "wasm-policy" || plugin["runtime_abi"] != "nre:policy/v1" || runtime["entry"] != "artifacts/policy.wasm" || len(artifacts) != 1 {
		t.Fatalf("runtime HTTP projection = %s", response.Body.String())
	}
	for _, forbidden := range []string{"CachePath", "ManifestJSON", "SignatureVerdict", `C:\panel\data`} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Fatalf("runtime HTTP projection leaked %q: %s", forbidden, response.Body.String())
		}
	}
}
