package service

import (
	"reflect"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestControlPlaneRuntimeGrantsNegotiateMultipleOutboundResources(t *testing.T) {
	names, selectors := controlPlaneRuntimeGrantProjection([]storage.PluginGenerationGrant{
		{Name: "http.outbound", ResourceID: "dnsapi.cn"},
		{Name: "http.outbound", ResourceID: "api.cloudflare.com"},
		{Name: "secret.use"},
		{Name: "service.revocable-resource-handle", ResourceKind: "resource-group", ResourceID: "dns"},
	})
	request := pluginsdk.RPCHandshakeRequest{ABI: pluginsdk.RPCABIV1, PluginID: "cloudflare-dns", PluginVersion: "0.1.19", PackageDigest: "package", ArtifactDigest: "artifact", Generation: "generation", GrantedScopes: names}
	_, err := pluginsdk.NegotiateRPCHandshake(pluginsdk.RPCPluginDeclaration{PluginID: request.PluginID, PluginVersion: request.PluginVersion, RequiredCapabilities: []string{"http.outbound", "secret.use"}}, request)
	if err != nil {
		t.Fatalf("multi-resource handshake rejected: %v", err)
	}
	want := map[string][]string{"http.outbound": {"api.cloudflare.com", "dnsapi.cn"}, "service.revocable-resource-handle": {"resource-group:dns"}}
	if !reflect.DeepEqual(selectors, want) {
		t.Fatalf("authorization selectors changed: got %v, want %v", selectors, want)
	}
}
