//go:build !integration

package service

import (
	"testing"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestPluginHostInstanceTargetsRequireExplicitAllowlist(t *testing.T) {
	t.Parallel()
	dual := pluginsdk.Runtime{
		Kind: pluginsdk.RuntimeRPCService, HostScope: pluginsdk.HostScopeControlPlane, HostScopes: []string{pluginsdk.HostScopeAgent},
	}
	if pluginHostInstanceTargetsAgent(dual, `[]`, "edge-a", "local") {
		t.Fatal("empty explicit targets must not allow plugin.call to arbitrary remotes")
	}
	if pluginHostInstanceTargetsAgent(dual, ``, "edge-a", "local") {
		t.Fatal("missing explicit targets must not allow plugin.call to arbitrary remotes")
	}
	if !pluginHostInstanceTargetsAgent(dual, `["edge-a"]`, "edge-a", "local") {
		t.Fatal("explicit target must allow plugin.call")
	}
	if pluginHostInstanceTargetsAgent(dual, `["edge-b"]`, "edge-a", "local") {
		t.Fatal("plugin.call must not address Agents outside explicit targets")
	}
}
