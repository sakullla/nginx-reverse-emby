package service

import (
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestManagedEndpointGrantProjectionPreservesIPv6AndOtherResourceKinds(t *testing.T) {
	for _, permission := range []string{sdk.PermissionManagedNetworkListen, sdk.PermissionManagedNetworkDial} {
		for _, selector := range []string{"tcp://[::1]:443", "udp://[2001:db8::1]:53", "network-endpoint:tcp://[::1]:443"} {
			kind, id := storage.SplitPluginGenerationGrantResource(permission, selector)
			if kind != "network-endpoint" || (id != "tcp://[::1]:443" && id != "udp://[2001:db8::1]:53") {
				t.Fatal("endpoint was split as a generic identity", kind, id)
			}
			projected := pluginGenerationGrants([]plugins.Permission{{Name: permission, Resource: selector}})
			if len(projected) != 1 || projected[0].ResourceKind != kind || projected[0].ResourceID != id {
				t.Fatal("pending and durable grant projection disagree")
			}
		}
	}
	if kind, id := storage.SplitPluginGenerationGrantResource(sdk.PermissionScopedSecretRead, "secret-scope:outbound-auth"); kind != "secret-scope" || id != "outbound-auth" {
		t.Fatal("unrelated resource identity changed")
	}
}
