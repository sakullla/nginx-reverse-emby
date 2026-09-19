package pluginsdk

import "testing"

func TestResolveRPCHandshakeProbe(t *testing.T) {
	declaration := RPCPluginDeclaration{PluginID: "example", PluginVersion: "1.2.3", RequiredCapabilities: []string{"resource.read"}}

	resolved, probe, err := ResolveRPCHandshakeProbe(nil, declaration)
	if err != nil || probe || resolved.PluginID != declaration.PluginID || resolved.PluginVersion != declaration.PluginVersion {
		t.Fatalf("non-probe result = %#v, %t, %v", resolved, probe, err)
	}
	resolved, probe, err = ResolveRPCHandshakeProbe([]string{RPCHandshakeProbeFlag}, declaration)
	if err != nil || !probe || resolved.PluginID != declaration.PluginID || resolved.PluginVersion != declaration.PluginVersion {
		t.Fatalf("self probe result = %#v, %t, %v", resolved, probe, err)
	}
	resolved, probe, err = ResolveRPCHandshakeProbe([]string{RPCHandshakeProbeFlag, "manifest-plugin", "2.0.0"}, declaration)
	if err != nil || !probe || resolved.PluginID != "manifest-plugin" || resolved.PluginVersion != "2.0.0" || len(resolved.RequiredCapabilities) != 1 {
		t.Fatalf("manifest probe result = %#v, %t, %v", resolved, probe, err)
	}
}

func TestResolveRPCHandshakeProbeRejectsInvalidArgumentsAndIdentity(t *testing.T) {
	declaration := RPCPluginDeclaration{PluginID: "example", PluginVersion: "1.2.3"}
	tests := [][]string{
		{RPCHandshakeProbeFlag, "example"},
		{RPCHandshakeProbeFlag, "example", "1.2.3", "extra"},
		{RPCHandshakeProbeFlag, "", "1.2.3"},
		{RPCHandshakeProbeFlag, " example", "1.2.3"},
		{RPCHandshakeProbeFlag, "example", ""},
		{RPCHandshakeProbeFlag, "example", "1.2.3 "},
	}
	for _, args := range tests {
		if _, probe, err := ResolveRPCHandshakeProbe(args, declaration); err == nil || !probe {
			t.Fatalf("ResolveRPCHandshakeProbe(%q) = probe %t, error %v", args, probe, err)
		}
	}
}
