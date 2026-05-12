package service

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

const (
	testWireGuardPrivateKey    = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	testWireGuardPublicKey     = "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB="
	testWireGuardPresharedKey  = "CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC="
	testWireGuardPublicKeyB    = "DDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD="
	testWireGuardPresharedKeyB = "EEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEE="
)

func TestWireGuardProfileCreateRedactsSecretsOnRead(t *testing.T) {
	ctx := context.Background()
	store, svc := newTestWireGuardProfileService(t)

	profile, err := svc.Create(ctx, "local", testWireGuardProfileInput())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if profile.PrivateKey != redactedProxyPassword {
		t.Fatalf("Create() private_key = %q, want redacted", profile.PrivateKey)
	}
	if len(profile.Peers) != 1 || profile.Peers[0].PresharedKey != redactedProxyPassword {
		t.Fatalf("Create() peer preshared_key = %+v, want redacted", profile.Peers)
	}

	profiles, err := svc.List(ctx, "local")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("List() length = %d, want 1", len(profiles))
	}
	if profiles[0].PrivateKey != redactedProxyPassword {
		t.Fatalf("List() private_key = %q, want redacted", profiles[0].PrivateKey)
	}
	if len(profiles[0].Peers) != 1 || profiles[0].Peers[0].PresharedKey != redactedProxyPassword {
		t.Fatalf("List() peer preshared_key = %+v, want redacted", profiles[0].Peers)
	}

	rawRows, err := store.ListWireGuardProfiles(ctx, "local")
	if err != nil {
		t.Fatalf("ListWireGuardProfiles() error = %v", err)
	}
	if len(rawRows) != 1 || rawRows[0].PrivateKey != testWireGuardPrivateKey {
		t.Fatalf("raw private_key = %+v, want original secret", rawRows)
	}
}

func TestWireGuardProfileRejectsInvalidCIDR(t *testing.T) {
	ctx := context.Background()
	_, svc := newTestWireGuardProfileService(t)

	input := testWireGuardProfileInput()
	input.Addresses = []string{"10.0.0.1"}
	_, err := svc.Create(ctx, "local", input)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() error = %v, want ErrInvalidArgument", err)
	}
	if err == nil || !strings.Contains(err.Error(), "addresses must be CIDR") {
		t.Fatalf("Create() error = %v, want addresses CIDR message", err)
	}
}

func TestWireGuardProfileCreateRequiresAddresses(t *testing.T) {
	ctx := context.Background()
	_, svc := newTestWireGuardProfileService(t)

	input := testWireGuardProfileInput()
	input.Addresses = nil
	_, err := svc.Create(ctx, "local", input)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() error = %v, want ErrInvalidArgument", err)
	}
	if err == nil || !strings.Contains(err.Error(), "addresses is required") {
		t.Fatalf("Create() error = %v, want addresses required message", err)
	}
}

func TestWireGuardProfileCreateRequiresPeers(t *testing.T) {
	ctx := context.Background()
	_, svc := newTestWireGuardProfileService(t)

	input := testWireGuardProfileInput()
	input.Peers = nil
	_, err := svc.Create(ctx, "local", input)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() error = %v, want ErrInvalidArgument", err)
	}
	if err == nil || !strings.Contains(err.Error(), "peers is required") {
		t.Fatalf("Create() error = %v, want peers required message", err)
	}
}

func TestWireGuardProfileRejectsDuplicatePeerPublicKey(t *testing.T) {
	ctx := context.Background()
	_, svc := newTestWireGuardProfileService(t)

	input := testWireGuardProfileInput()
	input.Peers = append(input.Peers, WireGuardPeer{
		Name:         "peer-b",
		PublicKey:    testWireGuardPublicKey,
		PresharedKey: testWireGuardPresharedKeyB,
		Endpoint:     "example.net:51820",
		AllowedIPs:   []string{"10.0.0.3/32"},
	})
	_, err := svc.Create(ctx, "local", input)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() error = %v, want ErrInvalidArgument", err)
	}
	if err == nil || !strings.Contains(err.Error(), "duplicate peers public_key") {
		t.Fatalf("Create() error = %v, want duplicate public_key message", err)
	}
}

func TestWireGuardProfileCreateDefaultsEnabledToTrueWhenOmitted(t *testing.T) {
	ctx := context.Background()
	_, svc := newTestWireGuardProfileService(t)

	created, err := svc.Create(ctx, "local", testWireGuardProfileInputWithoutEnabled())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !created.Enabled {
		t.Fatalf("Create() enabled = false, want true")
	}
}

func TestWireGuardProfileUpdatePreservesEnabledWhenOmitted(t *testing.T) {
	ctx := context.Background()
	_, svc := newTestWireGuardProfileService(t)

	created, err := svc.Create(ctx, "local", testWireGuardProfileInput())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	update := testWireGuardProfileInputWithoutEnabled()
	update.Name = "renamed wg"
	update.PrivateKey = redactedProxyPassword
	update.Peers[0].PresharedKey = redactedProxyPassword
	updated, err := svc.Update(ctx, "local", created.ID, update)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if !updated.Enabled {
		t.Fatalf("Update() enabled = false, want preserved true")
	}
}

func TestWireGuardProfileUpdateCanDisableProfile(t *testing.T) {
	ctx := context.Background()
	store, svc := newTestWireGuardProfileService(t)

	created, err := svc.Create(ctx, "local", testWireGuardProfileInput())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	update := testWireGuardProfileInput()
	update.PrivateKey = redactedProxyPassword
	update.Peers[0].PresharedKey = redactedProxyPassword
	update.Enabled = boolPtr(false)
	if _, err := svc.Update(ctx, "local", created.ID, update); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	profiles, err := svc.List(ctx, "local")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("List() length = %d, want 1", len(profiles))
	}
	if profiles[0].Enabled {
		t.Fatalf("List() enabled = true, want false")
	}
	rawRows, err := store.ListWireGuardProfiles(ctx, "local")
	if err != nil {
		t.Fatalf("ListWireGuardProfiles() error = %v", err)
	}
	if len(rawRows) != 1 || rawRows[0].Enabled {
		t.Fatalf("raw enabled rows = %+v, want disabled row", rawRows)
	}
}

func TestWireGuardProfileDefaultsModeToGenericWireGuard(t *testing.T) {
	ctx := context.Background()
	_, svc := newTestWireGuardProfileService(t)

	input := testWireGuardProfileInput()
	input.Mode = ""
	created, err := svc.Create(ctx, "local", input)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Mode != "generic_wireguard" {
		t.Fatalf("Create() mode = %q, want generic_wireguard", created.Mode)
	}
}

func TestWireGuardProfileRejectsUnsupportedMode(t *testing.T) {
	ctx := context.Background()
	_, svc := newTestWireGuardProfileService(t)

	input := testWireGuardProfileInput()
	input.Mode = "relay"
	_, err := svc.Create(ctx, "local", input)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() error = %v, want ErrInvalidArgument", err)
	}
	if err == nil || !strings.Contains(err.Error(), "mode") {
		t.Fatalf("Create() error = %v, want mode message", err)
	}
}

func TestWireGuardProfileRejectsInvalidPeerEndpoints(t *testing.T) {
	ctx := context.Background()

	for _, endpoint := range []string{"example.com:", "example.com:http", ":51820", "example.com:70000"} {
		t.Run(endpoint, func(t *testing.T) {
			_, svc := newTestWireGuardProfileService(t)

			input := testWireGuardProfileInput()
			input.Peers[0].Endpoint = endpoint
			_, err := svc.Create(ctx, "local", input)
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("Create() error = %v, want ErrInvalidArgument", err)
			}
			if err == nil || !strings.Contains(err.Error(), "endpoint") {
				t.Fatalf("Create() error = %v, want endpoint message", err)
			}
		})
	}
}

func TestWireGuardProfileRevisionUsesRemoteAgentFloor(t *testing.T) {
	ctx := context.Background()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "data"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})
	if err := store.SaveAgent(ctx, storage.AgentRow{
		ID:              "edge-1",
		Name:            "edge-1",
		DesiredRevision: 8,
		CurrentRevision: 11,
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}

	svc := NewWireGuardProfileService(config.Config{LocalAgentID: "local"}, store)
	created, err := svc.Create(ctx, "edge-1", testWireGuardProfileInput())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Revision != 12 {
		t.Fatalf("Create() revision = %d, want 12", created.Revision)
	}
}

func TestWireGuardProfileUpdateKeepsRedactedSecrets(t *testing.T) {
	ctx := context.Background()
	store, svc := newTestWireGuardProfileService(t)

	created, err := svc.Create(ctx, "local", testWireGuardProfileInput())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	update := testWireGuardProfileInput()
	update.Name = "renamed wg"
	update.PrivateKey = redactedProxyPassword
	update.Peers[0].PresharedKey = redactedProxyPassword
	if _, err := svc.Update(ctx, "local", created.ID, update); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	rawRows, err := store.ListWireGuardProfiles(ctx, "local")
	if err != nil {
		t.Fatalf("ListWireGuardProfiles() error = %v", err)
	}
	if len(rawRows) != 1 {
		t.Fatalf("raw row length = %d, want 1", len(rawRows))
	}
	if rawRows[0].PrivateKey != testWireGuardPrivateKey {
		t.Fatalf("raw private_key = %q, want original", rawRows[0].PrivateKey)
	}
	rawProfile := wireGuardProfileFromRow(rawRows[0])
	if len(rawProfile.Peers) != 1 || rawProfile.Peers[0].PresharedKey != testWireGuardPresharedKey {
		t.Fatalf("raw peer preshared_key = %+v, want original", rawProfile.Peers)
	}
}

func TestWireGuardProfileUpdateKeepsReorderedRedactedPeerSecretsByPublicKey(t *testing.T) {
	ctx := context.Background()
	store, svc := newTestWireGuardProfileService(t)

	input := testWireGuardProfileInput()
	input.Peers = append(input.Peers, WireGuardPeer{
		Name:                       "peer-b",
		PublicKey:                  testWireGuardPublicKeyB,
		PresharedKey:               testWireGuardPresharedKeyB,
		Endpoint:                   "example.net:51820",
		AllowedIPs:                 []string{"10.0.0.3/32"},
		PersistentKeepaliveSeconds: 30,
	})
	created, err := svc.Create(ctx, "local", input)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	update := testWireGuardProfileInput()
	update.PrivateKey = redactedProxyPassword
	update.Peers = []WireGuardPeer{
		{
			Name:                       "peer-b renamed",
			PublicKey:                  testWireGuardPublicKeyB,
			PresharedKey:               redactedProxyPassword,
			Endpoint:                   "example.net:51820",
			AllowedIPs:                 []string{"10.0.0.3/32"},
			PersistentKeepaliveSeconds: 30,
		},
		{
			Name:                       "peer-a renamed",
			PublicKey:                  testWireGuardPublicKey,
			PresharedKey:               redactedProxyPassword,
			Endpoint:                   "example.com:51820",
			AllowedIPs:                 []string{"10.0.0.2/32"},
			PersistentKeepaliveSeconds: 25,
		},
	}
	if _, err := svc.Update(ctx, "local", created.ID, update); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	rawRows, err := store.ListWireGuardProfiles(ctx, "local")
	if err != nil {
		t.Fatalf("ListWireGuardProfiles() error = %v", err)
	}
	rawProfile := wireGuardProfileFromRow(rawRows[0])
	if len(rawProfile.Peers) != 2 {
		t.Fatalf("raw peer length = %d, want 2", len(rawProfile.Peers))
	}
	if rawProfile.Peers[0].PresharedKey != testWireGuardPresharedKeyB {
		t.Fatalf("peer-b preshared_key = %q, want original peer-b secret", rawProfile.Peers[0].PresharedKey)
	}
	if rawProfile.Peers[1].PresharedKey != testWireGuardPresharedKey {
		t.Fatalf("peer-a preshared_key = %q, want original peer-a secret", rawProfile.Peers[1].PresharedKey)
	}
}

func TestWireGuardProfileUpdateRejectsUnknownRedactedPeerSecret(t *testing.T) {
	ctx := context.Background()
	_, svc := newTestWireGuardProfileService(t)

	created, err := svc.Create(ctx, "local", testWireGuardProfileInput())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	update := testWireGuardProfileInput()
	update.PrivateKey = redactedProxyPassword
	update.Peers = append(update.Peers, WireGuardPeer{
		Name:         "unknown-peer",
		PublicKey:    testWireGuardPublicKeyB,
		PresharedKey: redactedProxyPassword,
		Endpoint:     "example.net:51820",
		AllowedIPs:   []string{"10.0.0.3/32"},
	})
	_, err = svc.Update(ctx, "local", created.ID, update)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Update() error = %v, want ErrInvalidArgument", err)
	}
}

func newTestWireGuardProfileService(t *testing.T) (*storage.SQLiteStore, *wireGuardProfileService) {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "data"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	cfg := config.Config{EnableLocalAgent: true, LocalAgentID: "local"}
	return store, NewWireGuardProfileService(cfg, store)
}

func testWireGuardProfileInput() WireGuardProfileInput {
	input := testWireGuardProfileInputWithoutEnabled()
	input.Enabled = boolPtr(true)
	return input
}

func testWireGuardProfileInputWithoutEnabled() WireGuardProfileInput {
	return WireGuardProfileInput{
		Name:       "wg relay",
		Mode:       "generic_wireguard",
		PrivateKey: testWireGuardPrivateKey,
		ListenPort: 51820,
		Addresses:  []string{"10.0.0.1/24"},
		Peers: []WireGuardPeer{{
			Name:                       "peer-a",
			PublicKey:                  testWireGuardPublicKey,
			PresharedKey:               testWireGuardPresharedKey,
			Endpoint:                   "example.com:51820",
			AllowedIPs:                 []string{"10.0.0.2/32"},
			PersistentKeepaliveSeconds: 25,
		}},
		DNS:  []string{"1.1.1.1"},
		MTU:  1420,
		Tags: []string{"relay"},
	}
}
