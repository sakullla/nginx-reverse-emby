//go:build !integration

package storage

import (
	"encoding/json"
	"testing"
)

func TestPluginGenerationIdentityIgnoresEchoedConfigGeneration(t *testing.T) {
	t.Parallel()
	base := PluginGeneration{
		OperationID:   "op-1",
		InstanceID:    "instance-1",
		PluginID:      "plugin",
		PluginVersion: "0.1.0",
		PackageDigest: "digest",
		Runtime:       PluginGenerationRuntime{Kind: "rpc-service", ABI: "nre:rpc/v1", HostScope: "control-plane", Entry: "plugin"},
		Config:        json.RawMessage(`{"mode":"strict","resource_group_ref":"resource-group/injected"}`),
		Target:        PluginGenerationTarget{Kind: "control-plane", ID: "control-plane", ResourceGroupID: "group-1", Version: 1},
	}
	withoutGeneration, err := PluginGenerationIdentity(base)
	if err != nil {
		t.Fatal(err)
	}
	if withoutGeneration == "" {
		t.Fatal("empty generation identity")
	}
	base.Config = json.RawMessage(`{"generation":"` + withoutGeneration + `","mode":"strict","resource_group_ref":"resource-group/injected"}`)
	withGeneration, err := PluginGenerationIdentity(base)
	if err != nil {
		t.Fatal(err)
	}
	if withGeneration != withoutGeneration {
		t.Fatalf("identity changed after echoing generation: %s vs %s", withoutGeneration, withGeneration)
	}
	nested := PluginGeneration{
		OperationID:   base.OperationID,
		InstanceID:    base.InstanceID,
		PluginID:      base.PluginID,
		PluginVersion: base.PluginVersion,
		PackageDigest: base.PackageDigest,
		Runtime:       base.Runtime,
		Config:        json.RawMessage(`{"apps":[{"id":"app","image":"img","generation":"` + withoutGeneration + `"}]}`),
		Target:        base.Target,
	}
	withoutNested, err := PluginGenerationIdentity(PluginGeneration{
		OperationID: nested.OperationID, InstanceID: nested.InstanceID, PluginID: nested.PluginID,
		PluginVersion: nested.PluginVersion, PackageDigest: nested.PackageDigest, Runtime: nested.Runtime,
		Config: json.RawMessage(`{"apps":[{"id":"app","image":"img"}]}`), Target: nested.Target,
	})
	if err != nil {
		t.Fatal(err)
	}
	withNested, err := PluginGenerationIdentity(nested)
	if err != nil {
		t.Fatal(err)
	}
	if withNested != withoutNested {
		t.Fatalf("identity changed after echoing nested generation: %s vs %s", withoutNested, withNested)
	}
}
