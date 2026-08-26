//go:build !integration

package service

import (
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/revision"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestHeartbeatComparableSnapshotIgnoresRelayPKIRuntimeOverlay(t *testing.T) {
	base := storage.Snapshot{Revision: 7, RelayListeners: []storage.RelayListener{{ID: 1, AgentID: "relay-agent"}}}
	decorated := base
	decorated.RelayListeners = append([]storage.RelayListener(nil), base.RelayListeners...)
	decorated.RelayListeners[0].PKIIdentityID = "identity-1"
	decorated.RelayListeners[0].PKIIdentityState = "active"
	decorated.RelayListeners[0].PKICertificateID = "certificate-1"

	_, baseDigest, err := revision.CanonicalSnapshotPayload(base)
	if err != nil {
		t.Fatal(err)
	}
	_, decoratedDigest, err := revision.CanonicalSnapshotPayload(decorated)
	if err != nil {
		t.Fatal(err)
	}
	if baseDigest == decoratedDigest {
		t.Fatal("relay PKI overlay unexpectedly left the immutable payload unchanged")
	}

	_, baseComparable, err := revision.CanonicalSnapshotPayload(heartbeatComparableSnapshot(base))
	if err != nil {
		t.Fatal(err)
	}
	_, decoratedComparable, err := revision.CanonicalSnapshotPayload(heartbeatComparableSnapshot(decorated))
	if err != nil {
		t.Fatal(err)
	}
	if baseComparable != decoratedComparable {
		t.Fatalf("heartbeat comparable digests differ: base=%s decorated=%s", baseComparable, decoratedComparable)
	}
}

func TestBindHeartbeatRevisionKeepsLiveWhenComparableMatches(t *testing.T) {
	durable := storage.Snapshot{
		Revision: 7,
		Certificates: []storage.ManagedCertificateBundle{{
			ID: 1, Domain: "a.example",
		}},
		RelayListeners: []storage.RelayListener{{ID: 1, AgentID: "zouter"}},
	}
	live := durable
	live.VersionPackage = &storage.VersionPackage{
		URL:    "/panel-api/public/agent-assets/nre-agent-linux-amd64",
		SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	live.RelayListeners = append([]storage.RelayListener(nil), durable.RelayListeners...)
	live.RelayListeners[0].PKIIdentityID = "identity-1"
	live.RelayListeners[0].PKIIdentityState = "active"

	digest, bound, drifted, err := bindHeartbeatRevision(live, durable, "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB")
	if err != nil {
		t.Fatal(err)
	}
	if drifted {
		t.Fatal("comparable live overlay was treated as a durable drift")
	}
	if digest != "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" {
		t.Fatalf("digest = %q", digest)
	}
	if bound.VersionPackage != nil {
		t.Fatal("bound snapshot retained the version package overlay")
	}
	if len(bound.Certificates) != 1 || bound.Certificates[0].Domain != "a.example" {
		t.Fatalf("matched bind dropped live certificates: %+v", bound.Certificates)
	}
}

func TestBindHeartbeatRevisionPrefersDurableWhenLiveRematerializationDrifts(t *testing.T) {
	durable := storage.Snapshot{
		Revision:       7,
		DesiredVersion: "v1",
		Rules:          []storage.HTTPRule{{ID: 1, AgentID: "zouter", FrontendURL: "https://a.example"}},
	}
	live := durable
	live.PluginGenerations = []storage.PluginGeneration{{
		ID: "gen-live", InstanceID: "docker-app", PluginID: "docker-app", PackageDigest: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
	}}
	live.VersionPackage = &storage.VersionPackage{
		URL:    "/panel-api/public/agent-assets/nre-agent-linux-amd64",
		SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}

	digest, bound, drifted, err := bindHeartbeatRevision(live, durable, "DDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD")
	if err != nil {
		t.Fatal(err)
	}
	if !drifted {
		t.Fatal("live rematerialization was not treated as durable drift")
	}
	if digest != "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd" {
		t.Fatalf("digest = %q", digest)
	}
	if bound.VersionPackage != nil {
		t.Fatal("durable bind retained the version package overlay")
	}
	if len(bound.PluginGenerations) != 0 {
		t.Fatalf("drifted bind kept live plugin generations: %+v", bound.PluginGenerations)
	}
	if len(bound.Rules) != 1 || bound.Rules[0].FrontendURL != "https://a.example" {
		t.Fatalf("drifted bind lost durable rules: %+v", bound.Rules)
	}
}
