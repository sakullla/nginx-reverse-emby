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

func TestWireGuardProfileCreateAllocatesIDAcrossAgents(t *testing.T) {
	ctx := context.Background()
	store, svc := newTestWireGuardProfileService(t)

	if err := store.SaveAgent(ctx, storage.AgentRow{
		ID:         "remote",
		Name:       "remote",
		AgentToken: "token-remote",
	}); err != nil {
		t.Fatalf("SaveAgent(remote) error = %v", err)
	}
	if err := store.SaveWireGuardProfiles(ctx, "remote", []storage.WireGuardProfileRow{{
		ID:            1,
		AgentID:       "remote",
		Name:          "remote-wg",
		Mode:          "generic_wireguard",
		PrivateKey:    testWireGuardPrivateKey,
		ListenPort:    51820,
		AddressesJSON: `["10.0.1.1/24"]`,
		PeersJSON:     `[]`,
		DNSJSON:       `[]`,
		Enabled:       true,
		Revision:      1,
	}}); err != nil {
		t.Fatalf("SaveWireGuardProfiles(remote) error = %v", err)
	}

	profile, err := svc.Create(ctx, "local", testWireGuardProfileInput())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if profile.ID == 1 {
		t.Fatalf("Create() ID = %d, want globally unique ID", profile.ID)
	}
	if profile.ID != 2 {
		t.Fatalf("Create() ID = %d, want next global ID 2", profile.ID)
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

func TestWireGuardProfileUpdateCanClearDNSAndTags(t *testing.T) {
	ctx := context.Background()
	store, svc := newTestWireGuardProfileService(t)

	created, err := svc.Create(ctx, "local", testWireGuardProfileInput())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	update := testWireGuardProfileInput()
	update.PrivateKey = redactedProxyPassword
	update.Peers[0].PresharedKey = redactedProxyPassword
	update.DNS = []string{}
	update.Tags = []string{}
	updated, err := svc.Update(ctx, "local", created.ID, update)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.DNS == nil || len(updated.DNS) != 0 {
		t.Fatalf("Update() DNS = %+v, want explicit empty slice", updated.DNS)
	}
	if updated.Tags == nil || len(updated.Tags) != 0 {
		t.Fatalf("Update() Tags = %+v, want explicit empty slice", updated.Tags)
	}

	rawRows, err := store.ListWireGuardProfiles(ctx, "local")
	if err != nil {
		t.Fatalf("ListWireGuardProfiles() error = %v", err)
	}
	if len(rawRows) != 1 {
		t.Fatalf("raw rows len = %d, want 1", len(rawRows))
	}
	if rawRows[0].DNSJSON != "[]" {
		t.Fatalf("raw DNSJSON = %q, want []", rawRows[0].DNSJSON)
	}
	if rawRows[0].TagsJSON != "[]" {
		t.Fatalf("raw TagsJSON = %q, want []", rawRows[0].TagsJSON)
	}
}

func TestWireGuardProfileRejectsDisableOrDeleteWhenReferenced(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name string
		seed func(*testing.T, *storage.SQLiteStore, int)
	}{
		{
			name: "relay listener",
			seed: func(t *testing.T, store *storage.SQLiteStore, profileID int) {
				t.Helper()
				if err := store.SaveRelayListeners(ctx, "local", []storage.RelayListenerRow{{
					ID:                 100,
					AgentID:            "local",
					Name:               "wg relay",
					ListenHost:         "0.0.0.0",
					BindHostsJSON:      `["0.0.0.0"]`,
					ListenPort:         7443,
					PublicHost:         "relay.example.com",
					PublicPort:         7443,
					Enabled:            true,
					TLSMode:            "none",
					TransportMode:      "wireguard",
					WireGuardProfileID: &profileID,
					ObfsMode:           "off",
					PinSetJSON:         `[]`,
					TagsJSON:           `[]`,
					Revision:           1,
				}}); err != nil {
					t.Fatalf("SaveRelayListeners() error = %v", err)
				}
			},
		},
		{
			name: "l4 listen",
			seed: func(t *testing.T, store *storage.SQLiteStore, profileID int) {
				t.Helper()
				if err := store.SaveL4Rules(ctx, "local", []storage.L4RuleRow{{
					ID:                 101,
					AgentID:            "local",
					Name:               "wg listen",
					Protocol:           "tcp",
					ListenHost:         "0.0.0.0",
					ListenPort:         9443,
					BackendsJSON:       `[]`,
					LoadBalancingJSON:  `{"strategy":"adaptive"}`,
					TuningJSON:         `{"proxy_protocol":{"decode":false,"send":false}}`,
					RelayLayersJSON:    `[]`,
					ListenMode:         "wireguard",
					WireGuardProfileID: &profileID,
					Enabled:            true,
					TagsJSON:           `[]`,
					Revision:           1,
				}}); err != nil {
					t.Fatalf("SaveL4Rules() error = %v", err)
				}
			},
		},
		{
			name: "l4 egress",
			seed: func(t *testing.T, store *storage.SQLiteStore, profileID int) {
				t.Helper()
				if err := store.SaveL4Rules(ctx, "local", []storage.L4RuleRow{{
					ID:                 102,
					AgentID:            "local",
					Name:               "wg egress",
					Protocol:           "tcp",
					ListenHost:         "0.0.0.0",
					ListenPort:         1080,
					BackendsJSON:       `[]`,
					LoadBalancingJSON:  `{"strategy":"adaptive"}`,
					TuningJSON:         `{"proxy_protocol":{"decode":false,"send":false}}`,
					RelayLayersJSON:    `[]`,
					ListenMode:         "proxy",
					WireGuardProfileID: &profileID,
					ProxyEntryAuthJSON: `{}`,
					ProxyEgressMode:    "wireguard",
					Enabled:            true,
					TagsJSON:           `[]`,
					Revision:           1,
				}}); err != nil {
					t.Fatalf("SaveL4Rules() error = %v", err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name+"/disable", func(t *testing.T) {
			store, svc := newTestWireGuardProfileService(t)
			created, err := svc.Create(ctx, "local", testWireGuardProfileInput())
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			tt.seed(t, store, created.ID)

			update := testWireGuardProfileInput()
			update.PrivateKey = redactedProxyPassword
			update.Peers[0].PresharedKey = redactedProxyPassword
			update.Enabled = boolPtr(false)
			_, err = svc.Update(ctx, "local", created.ID, update)
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("Update() error = %v, want ErrInvalidArgument", err)
			}
			if err == nil || !strings.Contains(err.Error(), "wireguard profile is referenced") {
				t.Fatalf("Update() error = %v, want referenced message", err)
			}
		})

		t.Run(tt.name+"/delete", func(t *testing.T) {
			store, svc := newTestWireGuardProfileService(t)
			created, err := svc.Create(ctx, "local", testWireGuardProfileInput())
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			tt.seed(t, store, created.ID)

			_, err = svc.Delete(ctx, "local", created.ID)
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("Delete() error = %v, want ErrInvalidArgument", err)
			}
			if err == nil || !strings.Contains(err.Error(), "wireguard profile is referenced") {
				t.Fatalf("Delete() error = %v, want referenced message", err)
			}
		})
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

	for _, endpoint := range []string{"example.com:", "example.com:http", ":51820", "example.com:70000", "bad host:51820"} {
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

func TestWireGuardProfileAcceptsValidPeerEndpoints(t *testing.T) {
	ctx := context.Background()

	for _, endpoint := range []string{"example.com:51820", "192.0.2.10:51820", "[2001:db8::1]:51820"} {
		t.Run(endpoint, func(t *testing.T) {
			_, svc := newTestWireGuardProfileService(t)

			input := testWireGuardProfileInput()
			input.Peers[0].Endpoint = endpoint
			created, err := svc.Create(ctx, "local", input)
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			if got := created.Peers[0].Endpoint; got != endpoint {
				t.Fatalf("Create() endpoint = %q, want %q", got, endpoint)
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

func TestWireGuardProfileLocalApplyTriggerFiresForLocalMutations(t *testing.T) {
	ctx := context.Background()
	_, svc := newTestWireGuardProfileService(t)
	triggered := 0
	svc.SetLocalApplyTrigger(func(context.Context) error {
		triggered++
		return nil
	})

	created, err := svc.Create(ctx, "local", testWireGuardProfileInput())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if triggered != 1 {
		t.Fatalf("trigger count after Create() = %d, want 1", triggered)
	}

	update := testWireGuardProfileInput()
	update.PrivateKey = redactedProxyPassword
	update.Peers[0].PresharedKey = redactedProxyPassword
	if _, err := svc.Update(ctx, "local", created.ID, update); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if triggered != 2 {
		t.Fatalf("trigger count after Update() = %d, want 2", triggered)
	}

	if _, err := svc.Delete(ctx, "local", created.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if triggered != 3 {
		t.Fatalf("trigger count after Delete() = %d, want 3", triggered)
	}
}

func TestWireGuardProfileLocalApplyTriggerSkipsRemoteAgents(t *testing.T) {
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
		ID:   "edge-1",
		Name: "edge-1",
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	svc := NewWireGuardProfileService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)
	triggered := 0
	svc.SetLocalApplyTrigger(func(context.Context) error {
		triggered++
		return nil
	})

	created, err := svc.Create(ctx, "edge-1", testWireGuardProfileInput())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	update := testWireGuardProfileInput()
	update.PrivateKey = redactedProxyPassword
	update.Peers[0].PresharedKey = redactedProxyPassword
	if _, err := svc.Update(ctx, "edge-1", created.ID, update); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if _, err := svc.Delete(ctx, "edge-1", created.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if triggered != 0 {
		t.Fatalf("trigger count = %d, want 0", triggered)
	}
}

func TestWireGuardProfileUpdateUsesPathIDWhenBodyIDDiffers(t *testing.T) {
	ctx := context.Background()
	store, svc := newTestWireGuardProfileService(t)

	created, err := svc.Create(ctx, "local", testWireGuardProfileInput())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	update := testWireGuardProfileInput()
	update.ID = created.ID + 1000
	update.Name = "path id wins"
	update.PrivateKey = redactedProxyPassword
	update.Peers[0].PresharedKey = redactedProxyPassword
	updated, err := svc.Update(ctx, "local", created.ID, update)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.ID != created.ID {
		t.Fatalf("Update() id = %d, want path id %d", updated.ID, created.ID)
	}

	rawRows, err := store.ListWireGuardProfiles(ctx, "local")
	if err != nil {
		t.Fatalf("ListWireGuardProfiles() error = %v", err)
	}
	if len(rawRows) != 1 {
		t.Fatalf("raw row length = %d, want 1", len(rawRows))
	}
	if rawRows[0].ID != created.ID {
		t.Fatalf("raw row id = %d, want path id %d", rawRows[0].ID, created.ID)
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

func TestWireGuardProfileUpdateAndDeleteMissingReturnWireGuardNotFound(t *testing.T) {
	ctx := context.Background()
	_, svc := newTestWireGuardProfileService(t)

	if _, err := svc.Update(ctx, "local", 99, testWireGuardProfileInput()); !errors.Is(err, ErrWireGuardProfileNotFound) {
		t.Fatalf("Update() error = %v, want ErrWireGuardProfileNotFound", err)
	}
	if _, err := svc.Delete(ctx, "local", 99); !errors.Is(err, ErrWireGuardProfileNotFound) {
		t.Fatalf("Delete() error = %v, want ErrWireGuardProfileNotFound", err)
	}
}

func TestWireGuardProfileDeleteRemovesProfile(t *testing.T) {
	store, svc := newTestWireGuardProfileService(t)

	created, err := svc.Create(t.Context(), "local", testWireGuardProfileInput())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	deleted, err := svc.Delete(t.Context(), "local", created.ID)
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if deleted.ID != created.ID {
		t.Fatalf("Delete() ID = %d, want %d", deleted.ID, created.ID)
	}
	if deleted.PrivateKey != redactedProxyPassword {
		t.Fatalf("Delete() private_key = %q, want redacted", deleted.PrivateKey)
	}

	rows, err := store.ListWireGuardProfiles(t.Context(), "local")
	if err != nil {
		t.Fatalf("ListWireGuardProfiles() error = %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("rows after Delete() = %+v, want none", rows)
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
