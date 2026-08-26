//go:build !integration

package service

import (
	"slices"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/revision"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestNormalizeCapabilitiesPreservesPluginGenerationCapability(t *testing.T) {
	capabilities := normalizeCapabilities([]string{storage.PluginGenerationCapability})
	if !slices.Contains(capabilities, storage.PluginGenerationCapability) {
		t.Fatalf("normalizeCapabilities() = %v, missing %q", capabilities, storage.PluginGenerationCapability)
	}
}

func TestHeartbeatRevisionSnapshotIgnoresRuntimePackageOverlay(t *testing.T) {
	base := storage.Snapshot{Revision: 7, DesiredVersion: "", Rules: []storage.HTTPRule{}, PluginGenerations: []storage.PluginGeneration{}}
	withPackage := base
	withPackage.VersionPackage = &storage.VersionPackage{
		URL:      "/agent-assets/nre-agent-linux-amd64",
		SHA256:   strings.Repeat("a", 64),
		Platform: "linux-amd64",
	}

	_, baseDigest, err := revision.CanonicalSnapshotPayload(heartbeatRevisionSnapshot(base))
	if err != nil {
		t.Fatal(err)
	}
	_, packageDigest, err := revision.CanonicalSnapshotPayload(heartbeatRevisionSnapshot(withPackage))
	if err != nil {
		t.Fatal(err)
	}
	if packageDigest != baseDigest {
		t.Fatalf("heartbeat revision digest changed with runtime package overlay: %s != %s", packageDigest, baseDigest)
	}
}
