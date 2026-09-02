package model

import (
	"testing"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestKnownPluginExtensionPointAllowsResourceGroup(t *testing.T) {
	t.Parallel()
	if !knownPluginExtensionPoint(pluginsdk.ExtensionResourceGroup) {
		t.Fatal("resource.group extension point is not supported")
	}
}

func TestPluginGenerationGrantsAllowDeclaredFullNetwork(t *testing.T) {
	t.Parallel()
	if err := validatePluginGrants([]PluginGrantProjection{{Name: pluginsdk.PermissionNetworkFull}}); err != nil {
		t.Fatalf("network.full generation grant = %v", err)
	}
}
