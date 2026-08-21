package rpcplugin

import (
	"context"
	"testing"
	"time"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestLifecycleBindsHostAttestedDigestsAtHandshake(t *testing.T) {
	lifecycle, err := New(Config{
		PluginID:       "example",
		PluginVersion:  "1.0.0",
		RequiredGrants: []string{"resource.use"},
		Timeouts:       Timeouts{Prepare: time.Second, Activate: time.Second, Stop: time.Second, Drain: time.Second},
	}, HookFuncs{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	response, err := lifecycle.Handshake(context.Background(), pluginsdk.RPCHandshakeRequest{
		ABI:            pluginsdk.RPCABIV1,
		PluginID:       "example",
		PluginVersion:  "1.0.0",
		PackageDigest:  "package-digest",
		ArtifactDigest: "artifact-digest",
		GrantedScopes:  []string{"resource.use"},
		Generation:     "generation-1",
	})
	if err != nil {
		t.Fatalf("Handshake() error = %v", err)
	}
	if response.ABI != pluginsdk.RPCABIV1 {
		t.Fatalf("Handshake() ABI = %q", response.ABI)
	}
	if lifecycle.config.PackageDigest != "package-digest" || lifecycle.config.ArtifactDigest != "artifact-digest" {
		t.Fatalf("handshake did not bind Host-attested digests: %#v", lifecycle.config)
	}
}

func TestLifecycleRejectsPartialOrMissingDigestBinding(t *testing.T) {
	base := Config{
		PluginID:      "example",
		PluginVersion: "1.0.0",
		Timeouts:      Timeouts{Prepare: time.Second, Activate: time.Second, Stop: time.Second, Drain: time.Second},
	}
	partial := base
	partial.PackageDigest = "package-digest"
	if _, err := New(partial, HookFuncs{}); err == nil {
		t.Fatal("New() accepted a partial digest binding")
	}

	lifecycle, err := New(base, HookFuncs{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = lifecycle.Handshake(context.Background(), pluginsdk.RPCHandshakeRequest{
		ABI:           pluginsdk.RPCABIV1,
		PluginID:      "example",
		PluginVersion: "1.0.0",
		Generation:    "generation-1",
	})
	if err == nil {
		t.Fatal("Handshake() accepted missing Host-attested digests")
	}
}
