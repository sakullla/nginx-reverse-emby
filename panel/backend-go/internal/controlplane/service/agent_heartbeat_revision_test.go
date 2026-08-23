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
