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

func TestWireGuardClientCreateAllocatesAddressAndGeneratesConfig(t *testing.T) {
	ctx := context.Background()
	store, profileSvc, clientSvc := newTestWireGuardClientService(t)

	input := testWireGuardProfileInput()
	input.Addresses = []string{"10.8.0.1/24"}
	input.PublicEndpoint = "wg.example.com:51820"
	input.Peers[0].Endpoint = ""
	input.DNS = []string{"1.1.1.1"}
	profile, err := profileSvc.Create(ctx, "local", input)
	if err != nil {
		t.Fatalf("Create(profile) error = %v", err)
	}

	client, err := clientSvc.CreateClient(ctx, "local", profile.ID, WireGuardClientInput{Name: "phone"})
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	if client.Address != "10.8.0.2/32" {
		t.Fatalf("CreateClient() address = %q, want 10.8.0.2/32", client.Address)
	}

	configText, err := clientSvc.ClientConfig(ctx, "local", profile.ID, client.ID)
	if err != nil {
		t.Fatalf("ClientConfig() error = %v", err)
	}
	if !strings.Contains(configText, "Endpoint = wg.example.com:51820") {
		t.Fatalf("ClientConfig() missing endpoint:\n%s", configText)
	}
	if !strings.Contains(configText, "Address = 10.8.0.2/32") {
		t.Fatalf("ClientConfig() missing address:\n%s", configText)
	}
	if !strings.Contains(configText, "AllowedIPs = 10.8.0.1/24") {
		t.Fatalf("ClientConfig() missing profile allowed IPs:\n%s", configText)
	}
	if strings.Contains(configText, "AllowedIPs = 10.8.0.2/32") {
		t.Fatalf("ClientConfig() used client address for allowed IPs:\n%s", configText)
	}

	rows, err := store.ListWireGuardClients(ctx, "local", profile.ID)
	if err != nil {
		t.Fatalf("ListWireGuardClients() error = %v", err)
	}
	if len(rows) != 1 || rows[0].PrivateKey == "" || rows[0].PresharedKey == "" {
		t.Fatalf("client row secrets were not persisted: %+v", rows)
	}
}

func TestWireGuardClientCreateHonorsExplicitAllowedIPs(t *testing.T) {
	ctx := context.Background()
	_, profileSvc, clientSvc := newTestWireGuardClientService(t)

	input := testWireGuardProfileInput()
	input.Addresses = []string{"10.8.0.1/24"}
	input.PublicEndpoint = "wg.example.com:51820"
	input.Peers[0].Endpoint = ""
	profile, err := profileSvc.Create(ctx, "local", input)
	if err != nil {
		t.Fatalf("Create(profile) error = %v", err)
	}

	client, err := clientSvc.CreateClient(ctx, "local", profile.ID, WireGuardClientInput{
		Name:       "phone",
		AllowedIPs: []string{"0.0.0.0/0", "::/0"},
	})
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}

	configText, err := clientSvc.ClientConfig(ctx, "local", profile.ID, client.ID)
	if err != nil {
		t.Fatalf("ClientConfig() error = %v", err)
	}
	if !strings.Contains(configText, "AllowedIPs = 0.0.0.0/0, ::/0") {
		t.Fatalf("ClientConfig() missing explicit allowed IPs:\n%s", configText)
	}
	if strings.Contains(configText, "AllowedIPs = 10.8.0.1/24") {
		t.Fatalf("ClientConfig() replaced explicit allowed IPs with profile addresses:\n%s", configText)
	}
}

func TestWireGuardClientCreateDefaultsBlankAllowedIPsToProfileAddresses(t *testing.T) {
	ctx := context.Background()
	_, profileSvc, clientSvc := newTestWireGuardClientService(t)

	input := testWireGuardProfileInput()
	input.Addresses = []string{"10.8.0.1/24"}
	input.PublicEndpoint = "wg.example.com:51820"
	input.Peers[0].Endpoint = ""
	profile, err := profileSvc.Create(ctx, "local", input)
	if err != nil {
		t.Fatalf("Create(profile) error = %v", err)
	}

	client, err := clientSvc.CreateClient(ctx, "local", profile.ID, WireGuardClientInput{
		Name:       "phone",
		AllowedIPs: []string{"", " \t "},
	})
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}

	configText, err := clientSvc.ClientConfig(ctx, "local", profile.ID, client.ID)
	if err != nil {
		t.Fatalf("ClientConfig() error = %v", err)
	}
	if !strings.Contains(configText, "AllowedIPs = 10.8.0.1/24") {
		t.Fatalf("ClientConfig() missing profile allowed IPs:\n%s", configText)
	}
}

func TestWireGuardClientCreateRejectsInvalidAllowedIPs(t *testing.T) {
	ctx := context.Background()
	_, profileSvc, clientSvc := newTestWireGuardClientService(t)

	input := testWireGuardProfileInput()
	input.Addresses = []string{"10.8.0.1/24"}
	input.PublicEndpoint = "wg.example.com:51820"
	input.Peers[0].Endpoint = ""
	profile, err := profileSvc.Create(ctx, "local", input)
	if err != nil {
		t.Fatalf("Create(profile) error = %v", err)
	}

	_, err = clientSvc.CreateClient(ctx, "local", profile.ID, WireGuardClientInput{
		Name:       "phone",
		AllowedIPs: []string{"not-a-cidr"},
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("CreateClient() error = %v, want ErrInvalidArgument", err)
	}
	if err == nil || !strings.Contains(err.Error(), "allowed_ips must be CIDR") {
		t.Fatalf("CreateClient() error = %v, want allowed_ips CIDR message", err)
	}
}

func TestWireGuardClientCreateAndToggleRejectRemoteAgentWithoutWireGuardCapability(t *testing.T) {
	ctx := context.Background()
	store, _, clientSvc := newTestWireGuardClientService(t)
	if err := store.SaveAgent(ctx, storage.AgentRow{
		ID:               "edge-no-wg",
		Name:             "edge-no-wg",
		AgentToken:       "token-edge-no-wg",
		CapabilitiesJSON: `["http_rules","l4"]`,
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	profile := wireGuardProfileToRow(WireGuardProfile{
		ID:         41,
		AgentID:    "edge-no-wg",
		Name:       "seed",
		Mode:       "generic_wireguard",
		PrivateKey: testWireGuardPrivateKey,
		Addresses:  []string{"10.44.0.1/24"},
		Peers:      []WireGuardPeer{},
		Enabled:    true,
		Revision:   1,
	})
	if err := store.SaveWireGuardProfiles(ctx, "edge-no-wg", []storage.WireGuardProfileRow{profile}); err != nil {
		t.Fatalf("SaveWireGuardProfiles() error = %v", err)
	}
	client := storage.WireGuardClientRow{
		ID:             7,
		AgentID:        "edge-no-wg",
		ProfileID:      41,
		Name:           "phone",
		PrivateKey:     testWireGuardPrivateKey,
		PublicKey:      testWireGuardPublicKey,
		PresharedKey:   testWireGuardPresharedKey,
		Address:        "10.44.0.2/32",
		AllowedIPsJSON: `["10.44.0.1/24"]`,
		DNSJSON:        `[]`,
		Enabled:        false,
	}
	if err := store.SaveWireGuardClients(ctx, "edge-no-wg", 41, []storage.WireGuardClientRow{client}); err != nil {
		t.Fatalf("SaveWireGuardClients() error = %v", err)
	}

	if _, err := clientSvc.CreateClient(ctx, "edge-no-wg", 41, WireGuardClientInput{Name: "tablet"}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("CreateClient() error = %v, want ErrInvalidArgument", err)
	} else if !strings.Contains(err.Error(), "agent does not support WireGuard") {
		t.Fatalf("CreateClient() error = %v, want wireguard capability message", err)
	}

	enabled := true
	if _, err := clientSvc.UpdateClient(ctx, "edge-no-wg", 41, 7, WireGuardClientInput{Enabled: &enabled}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("UpdateClient() error = %v, want ErrInvalidArgument", err)
	} else if !strings.Contains(err.Error(), "agent does not support WireGuard") {
		t.Fatalf("UpdateClient() error = %v, want wireguard capability message", err)
	}

	rows, err := store.ListWireGuardClients(ctx, "edge-no-wg", 41)
	if err != nil {
		t.Fatalf("ListWireGuardClients() error = %v", err)
	}
	if len(rows) != 1 || rows[0] != client {
		t.Fatalf("stored wireguard clients = %+v, want unchanged client %+v", rows, client)
	}
}

func TestWireGuardClientConfigRejectsRemoteAgentWithoutWireGuardCapability(t *testing.T) {
	ctx := context.Background()
	store, _, clientSvc := newTestWireGuardClientService(t)
	if err := store.SaveAgent(ctx, storage.AgentRow{
		ID:               "edge-no-wg",
		Name:             "edge-no-wg",
		AgentToken:       "token-edge-no-wg",
		CapabilitiesJSON: `["http_rules","l4"]`,
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	profile := wireGuardProfileToRow(WireGuardProfile{
		ID:             41,
		AgentID:        "edge-no-wg",
		Name:           "seed",
		Mode:           "generic_wireguard",
		PrivateKey:     testWireGuardPrivateKey,
		PublicEndpoint: "wg.example.com:51820",
		Addresses:      []string{"10.44.0.1/24"},
		Peers:          []WireGuardPeer{},
		Enabled:        true,
		Revision:       1,
	})
	if err := store.SaveWireGuardProfiles(ctx, "edge-no-wg", []storage.WireGuardProfileRow{profile}); err != nil {
		t.Fatalf("SaveWireGuardProfiles() error = %v", err)
	}
	client := storage.WireGuardClientRow{
		ID:             7,
		AgentID:        "edge-no-wg",
		ProfileID:      41,
		Name:           "phone",
		PrivateKey:     testWireGuardPrivateKey,
		PublicKey:      testWireGuardPublicKey,
		PresharedKey:   testWireGuardPresharedKey,
		Address:        "10.44.0.2/32",
		AllowedIPsJSON: `["10.44.0.1/24"]`,
		DNSJSON:        `[]`,
		Enabled:        true,
	}
	if err := store.SaveWireGuardClients(ctx, "edge-no-wg", 41, []storage.WireGuardClientRow{client}); err != nil {
		t.Fatalf("SaveWireGuardClients() error = %v", err)
	}

	configText, err := clientSvc.ClientConfig(ctx, "edge-no-wg", 41, 7)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("ClientConfig() error = %v, want ErrInvalidArgument", err)
	}
	if err == nil || !strings.Contains(err.Error(), "agent does not support WireGuard") {
		t.Fatalf("ClientConfig() error = %v, want wireguard capability message", err)
	}
	if configText != "" {
		t.Fatalf("ClientConfig() returned private material for unsupported agent:\n%s", configText)
	}
}

func TestWireGuardClientListReturnsRedactedClients(t *testing.T) {
	ctx := context.Background()
	_, profileSvc, clientSvc := newTestWireGuardClientService(t)

	input := testWireGuardProfileInput()
	input.Addresses = []string{"10.8.0.1/24"}
	input.PublicEndpoint = "wg.example.com:51820"
	input.Peers[0].Endpoint = ""
	profile, err := profileSvc.Create(ctx, "local", input)
	if err != nil {
		t.Fatalf("Create(profile) error = %v", err)
	}
	created, err := clientSvc.CreateClient(ctx, "local", profile.ID, WireGuardClientInput{Name: "phone"})
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}

	clients, err := clientSvc.ListClients(ctx, "local", profile.ID)
	if err != nil {
		t.Fatalf("ListClients() error = %v", err)
	}
	if len(clients) != 1 {
		t.Fatalf("ListClients() len = %d, want 1", len(clients))
	}
	if clients[0].ID != created.ID || clients[0].ProfileID != profile.ID || clients[0].Name != "phone" || clients[0].Address != "10.8.0.2/32" {
		t.Fatalf("ListClients()[0] = %+v, want %+v", clients[0], created)
	}
}

func TestWireGuardClientUpdateDisablesEnabledClientAndKeepsSecretsStable(t *testing.T) {
	ctx := context.Background()
	store, profileSvc, clientSvc := newTestWireGuardClientService(t)

	input := testWireGuardProfileInput()
	input.Addresses = []string{"10.8.0.1/24"}
	input.PublicEndpoint = "wg.example.com:51820"
	input.Peers[0].Endpoint = ""
	profile, err := profileSvc.Create(ctx, "local", input)
	if err != nil {
		t.Fatalf("Create(profile) error = %v", err)
	}
	client, err := clientSvc.CreateClient(ctx, "local", profile.ID, WireGuardClientInput{
		Name:       "phone",
		AllowedIPs: []string{"0.0.0.0/0"},
		DNS:        []string{"9.9.9.9"},
	})
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	beforeRows, err := store.ListWireGuardClients(ctx, "local", profile.ID)
	if err != nil {
		t.Fatalf("ListWireGuardClients(before) error = %v", err)
	}
	if len(beforeRows) != 1 {
		t.Fatalf("before rows len = %d, want 1", len(beforeRows))
	}

	disabled := false
	updated, err := clientSvc.UpdateClient(ctx, "local", profile.ID, client.ID, WireGuardClientInput{Enabled: &disabled})
	if err != nil {
		t.Fatalf("UpdateClient(disable) error = %v", err)
	}
	if updated.Enabled {
		t.Fatalf("UpdateClient(disable) Enabled = true, want false")
	}
	if updated.Name != client.Name || updated.Address != client.Address || updated.PublicKey != client.PublicKey {
		t.Fatalf("UpdateClient(disable) changed stable public fields: before=%+v after=%+v", client, updated)
	}
	if strings.Join(updated.AllowedIPs, ",") != "0.0.0.0/0" || strings.Join(updated.DNS, ",") != "9.9.9.9" {
		t.Fatalf("UpdateClient(disable) changed allowed_ips/dns: %+v", updated)
	}

	afterRows, err := store.ListWireGuardClients(ctx, "local", profile.ID)
	if err != nil {
		t.Fatalf("ListWireGuardClients(after) error = %v", err)
	}
	if len(afterRows) != 1 {
		t.Fatalf("after rows len = %d, want 1", len(afterRows))
	}
	if afterRows[0].PrivateKey != beforeRows[0].PrivateKey || afterRows[0].PresharedKey != beforeRows[0].PresharedKey || afterRows[0].Address != beforeRows[0].Address {
		t.Fatalf("UpdateClient(disable) changed stable row secrets/address: before=%+v after=%+v", beforeRows[0], afterRows[0])
	}

	profiles, err := store.ListWireGuardProfiles(ctx, "local")
	if err != nil {
		t.Fatalf("ListWireGuardProfiles() error = %v", err)
	}
	storedProfile := wireGuardProfileFromRow(profiles[0])
	for _, peer := range storedProfile.Peers {
		if peer.PublicKey == client.PublicKey {
			t.Fatalf("disabled client peer still present: %+v", storedProfile.Peers)
		}
	}
}

func TestWireGuardClientUpdateEnablesDisabledClient(t *testing.T) {
	ctx := context.Background()
	store, profileSvc, clientSvc := newTestWireGuardClientService(t)

	input := testWireGuardProfileInput()
	input.Addresses = []string{"10.8.0.1/24"}
	input.PublicEndpoint = "wg.example.com:51820"
	input.Peers[0].Endpoint = ""
	profile, err := profileSvc.Create(ctx, "local", input)
	if err != nil {
		t.Fatalf("Create(profile) error = %v", err)
	}
	enabled := false
	client, err := clientSvc.CreateClient(ctx, "local", profile.ID, WireGuardClientInput{Name: "phone", Enabled: &enabled})
	if err != nil {
		t.Fatalf("CreateClient(disabled) error = %v", err)
	}

	enabled = true
	updated, err := clientSvc.UpdateClient(ctx, "local", profile.ID, client.ID, WireGuardClientInput{Enabled: &enabled})
	if err != nil {
		t.Fatalf("UpdateClient(enable) error = %v", err)
	}
	if !updated.Enabled {
		t.Fatalf("UpdateClient(enable) Enabled = false, want true")
	}

	profiles, err := store.ListWireGuardProfiles(ctx, "local")
	if err != nil {
		t.Fatalf("ListWireGuardProfiles() error = %v", err)
	}
	storedProfile := wireGuardProfileFromRow(profiles[0])
	foundPeer := false
	for _, peer := range storedProfile.Peers {
		if peer.PublicKey == client.PublicKey {
			foundPeer = true
			if peer.Name != client.Name || len(peer.AllowedIPs) != 1 || peer.AllowedIPs[0] != client.Address {
				t.Fatalf("enabled client peer = %+v, want name/address from client %+v", peer, client)
			}
		}
	}
	if !foundPeer {
		t.Fatalf("enabled client peer not present: %+v", storedProfile.Peers)
	}
}

func TestWireGuardProfileUpdatePreservesEnabledGeneratedClientPeer(t *testing.T) {
	ctx := context.Background()
	store, profileSvc, clientSvc := newTestWireGuardClientService(t)

	input := testWireGuardProfileInput()
	input.Addresses = []string{"10.8.0.1/24"}
	input.PublicEndpoint = "wg.example.com:51820"
	input.Peers = []WireGuardPeer{}
	profile, err := profileSvc.Create(ctx, "local", input)
	if err != nil {
		t.Fatalf("Create(profile) error = %v", err)
	}
	client, err := clientSvc.CreateClient(ctx, "local", profile.ID, WireGuardClientInput{Name: "phone"})
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}

	update := input
	update.PrivateKey = redactedProxyPassword
	update.Peers = []WireGuardPeer{}
	updated, err := profileSvc.Update(ctx, "local", profile.ID, update)
	if err != nil {
		t.Fatalf("Update(profile) error = %v", err)
	}
	if len(updated.Peers) != 1 || updated.Peers[0].PublicKey != client.PublicKey {
		t.Fatalf("Update(profile) peers = %+v, want enabled generated client peer", updated.Peers)
	}

	profiles, err := store.ListWireGuardProfiles(ctx, "local")
	if err != nil {
		t.Fatalf("ListWireGuardProfiles() error = %v", err)
	}
	storedProfile := wireGuardProfileFromRow(profiles[0])
	if len(storedProfile.Peers) != 1 {
		t.Fatalf("stored profile peers = %+v, want enabled generated client peer", storedProfile.Peers)
	}
	peer := storedProfile.Peers[0]
	if peer.PublicKey != client.PublicKey || peer.PresharedKey == "" || len(peer.AllowedIPs) != 1 || peer.AllowedIPs[0] != client.Address {
		t.Fatalf("stored generated peer = %+v, want generated client %v", peer, client)
	}

	snapshot, err := store.LoadAgentSnapshot(ctx, "local", storage.AgentSnapshotInput{})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot() error = %v", err)
	}
	if len(snapshot.WireGuardProfiles) != 1 || len(snapshot.WireGuardProfiles[0].Peers) != 1 || snapshot.WireGuardProfiles[0].Peers[0].PublicKey != client.PublicKey {
		t.Fatalf("snapshot WireGuardProfiles = %+v, want enabled generated client peer", snapshot.WireGuardProfiles)
	}
}

func TestWireGuardProfileUpdateUsesTransactionalClientStateForGeneratedPeers(t *testing.T) {
	ctx := context.Background()
	store, profileSvc, clientSvc := newTestWireGuardClientService(t)

	input := testWireGuardProfileInput()
	input.Addresses = []string{"10.8.0.1/24"}
	input.PublicEndpoint = "wg.example.com:51820"
	input.Peers = []WireGuardPeer{}
	profile, err := profileSvc.Create(ctx, "local", input)
	if err != nil {
		t.Fatalf("Create(profile) error = %v", err)
	}
	client, err := clientSvc.CreateClient(ctx, "local", profile.ID, WireGuardClientInput{Name: "phone"})
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	staleEnabledRows, err := store.ListWireGuardClients(ctx, "local", profile.ID)
	if err != nil {
		t.Fatalf("ListWireGuardClients(stale) error = %v", err)
	}
	if len(staleEnabledRows) != 1 || !staleEnabledRows[0].Enabled {
		t.Fatalf("stale enabled rows = %+v, want one enabled client", staleEnabledRows)
	}

	enabled := false
	if _, err := clientSvc.UpdateClient(ctx, "local", profile.ID, client.ID, WireGuardClientInput{Enabled: &enabled}); err != nil {
		t.Fatalf("UpdateClient(disable) error = %v", err)
	}
	currentDisabledRows, err := store.ListWireGuardClients(ctx, "local", profile.ID)
	if err != nil {
		t.Fatalf("ListWireGuardClients(current) error = %v", err)
	}
	if len(currentDisabledRows) != 1 || currentDisabledRows[0].Enabled {
		t.Fatalf("current rows = %+v, want one disabled client", currentDisabledRows)
	}

	staleStore := &staleWireGuardClientListStore{
		SQLiteStore:  store,
		staleClients: append([]storage.WireGuardClientRow(nil), staleEnabledRows...),
	}
	staleProfileSvc := NewWireGuardProfileService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, staleStore)
	update := input
	update.PrivateKey = redactedProxyPassword
	update.Peers = []WireGuardPeer{}
	updated, err := staleProfileSvc.Update(ctx, "local", profile.ID, update)
	if err != nil {
		t.Fatalf("Update(profile) error = %v", err)
	}
	for _, peer := range updated.Peers {
		if peer.PublicKey == client.PublicKey {
			t.Fatalf("Update(profile) used stale enabled client snapshot and re-added peer: %+v", updated.Peers)
		}
	}

	profiles, err := store.ListWireGuardProfiles(ctx, "local")
	if err != nil {
		t.Fatalf("ListWireGuardProfiles() error = %v", err)
	}
	storedProfile := wireGuardProfileFromRow(profiles[0])
	for _, peer := range storedProfile.Peers {
		if peer.PublicKey == client.PublicKey {
			t.Fatalf("stored profile used stale enabled client snapshot and re-added peer: %+v", storedProfile.Peers)
		}
	}
	afterRows, err := store.ListWireGuardClients(ctx, "local", profile.ID)
	if err != nil {
		t.Fatalf("ListWireGuardClients(after) error = %v", err)
	}
	if len(afterRows) != len(currentDisabledRows) || afterRows[0] != currentDisabledRows[0] {
		t.Fatalf("Update(profile) mutated client rows: before=%+v after=%+v", currentDisabledRows, afterRows)
	}
}

func TestWireGuardProfileUpdateDoesNotReaddDisabledGeneratedClientPeer(t *testing.T) {
	ctx := context.Background()
	store, profileSvc, clientSvc := newTestWireGuardClientService(t)

	input := testWireGuardProfileInput()
	input.Addresses = []string{"10.8.0.1/24"}
	input.PublicEndpoint = "wg.example.com:51820"
	input.Peers = []WireGuardPeer{}
	profile, err := profileSvc.Create(ctx, "local", input)
	if err != nil {
		t.Fatalf("Create(profile) error = %v", err)
	}
	enabled := false
	client, err := clientSvc.CreateClient(ctx, "local", profile.ID, WireGuardClientInput{Name: "phone", Enabled: &enabled})
	if err != nil {
		t.Fatalf("CreateClient(disabled) error = %v", err)
	}

	update := input
	update.PrivateKey = redactedProxyPassword
	update.Peers = []WireGuardPeer{}
	updated, err := profileSvc.Update(ctx, "local", profile.ID, update)
	if err != nil {
		t.Fatalf("Update(profile) error = %v", err)
	}
	for _, peer := range updated.Peers {
		if peer.PublicKey == client.PublicKey {
			t.Fatalf("Update(profile) re-added disabled generated client peer: %+v", updated.Peers)
		}
	}

	profiles, err := store.ListWireGuardProfiles(ctx, "local")
	if err != nil {
		t.Fatalf("ListWireGuardProfiles() error = %v", err)
	}
	storedProfile := wireGuardProfileFromRow(profiles[0])
	for _, peer := range storedProfile.Peers {
		if peer.PublicKey == client.PublicKey {
			t.Fatalf("stored profile re-added disabled generated client peer: %+v", storedProfile.Peers)
		}
	}
}

func TestWireGuardProfileUpdateHonorsExplicitEmptyManualPeersWhenGeneratedClientExists(t *testing.T) {
	ctx := context.Background()
	store, profileSvc, clientSvc := newTestWireGuardClientService(t)

	input := testWireGuardProfileInput()
	input.Addresses = []string{"10.8.0.1/24"}
	input.PublicEndpoint = "wg.example.com:51820"
	input.Peers[0].Endpoint = ""
	input.Peers[0].AllowedIPs = []string{"10.8.0.2/32"}
	profile, err := profileSvc.Create(ctx, "local", input)
	if err != nil {
		t.Fatalf("Create(profile) error = %v", err)
	}
	client, err := clientSvc.CreateClient(ctx, "local", profile.ID, WireGuardClientInput{Name: "phone"})
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}

	update := input
	update.PrivateKey = redactedProxyPassword
	update.Peers = []WireGuardPeer{}
	updated, err := profileSvc.Update(ctx, "local", profile.ID, update)
	if err != nil {
		t.Fatalf("Update(profile) error = %v", err)
	}
	if len(updated.Peers) != 1 || updated.Peers[0].PublicKey != client.PublicKey {
		t.Fatalf("Update(profile) peers = %+v, want only generated client peer", updated.Peers)
	}
	if updated.Peers[0].PublicKey == testWireGuardPublicKey {
		t.Fatalf("Update(profile) retained removed manual peer: %+v", updated.Peers)
	}

	profiles, err := store.ListWireGuardProfiles(ctx, "local")
	if err != nil {
		t.Fatalf("ListWireGuardProfiles() error = %v", err)
	}
	storedProfile := wireGuardProfileFromRow(profiles[0])
	if len(storedProfile.Peers) != 1 || storedProfile.Peers[0].PublicKey != client.PublicKey {
		t.Fatalf("stored profile peers = %+v, want only generated client peer", storedProfile.Peers)
	}
}

func TestWireGuardProfileUpdatePreservesManualPeersWhenGeneratedClientExists(t *testing.T) {
	ctx := context.Background()
	store, profileSvc, clientSvc := newTestWireGuardClientService(t)

	input := testWireGuardProfileInput()
	input.Addresses = []string{"10.8.0.1/24"}
	input.PublicEndpoint = "wg.example.com:51820"
	input.Peers[0].Endpoint = ""
	input.Peers[0].AllowedIPs = []string{"10.8.0.2/32"}
	profile, err := profileSvc.Create(ctx, "local", input)
	if err != nil {
		t.Fatalf("Create(profile) error = %v", err)
	}
	client, err := clientSvc.CreateClient(ctx, "local", profile.ID, WireGuardClientInput{Name: "phone"})
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}

	update := input
	update.PrivateKey = redactedProxyPassword
	update.Peers[0].Name = "manual-renamed"
	update.Peers[0].PresharedKey = redactedProxyPassword
	updated, err := profileSvc.Update(ctx, "local", profile.ID, update)
	if err != nil {
		t.Fatalf("Update(profile) error = %v", err)
	}
	if len(updated.Peers) != 2 {
		t.Fatalf("Update(profile) peers = %+v, want manual plus generated peer", updated.Peers)
	}

	profiles, err := store.ListWireGuardProfiles(ctx, "local")
	if err != nil {
		t.Fatalf("ListWireGuardProfiles() error = %v", err)
	}
	storedProfile := wireGuardProfileFromRow(profiles[0])
	var foundManual bool
	var foundGenerated bool
	for _, peer := range storedProfile.Peers {
		switch peer.PublicKey {
		case testWireGuardPublicKey:
			foundManual = true
			if peer.Name != "manual-renamed" || peer.PresharedKey != testWireGuardPresharedKey {
				t.Fatalf("manual peer = %+v, want edited manual peer with preserved secret", peer)
			}
		case client.PublicKey:
			foundGenerated = true
			if len(peer.AllowedIPs) != 1 || peer.AllowedIPs[0] != client.Address {
				t.Fatalf("generated peer = %+v, want generated client address %q", peer, client.Address)
			}
		}
	}
	if !foundManual || !foundGenerated {
		t.Fatalf("stored peers = %+v, want manual and generated peers", storedProfile.Peers)
	}
}

func TestWireGuardClientUpdateRejectsMissingEnabledWithoutMutation(t *testing.T) {
	ctx := context.Background()
	store, profileSvc, clientSvc := newTestWireGuardClientService(t)

	input := testWireGuardProfileInput()
	input.Addresses = []string{"10.8.0.1/24"}
	input.PublicEndpoint = "wg.example.com:51820"
	input.Peers[0].Endpoint = ""
	profile, err := profileSvc.Create(ctx, "local", input)
	if err != nil {
		t.Fatalf("Create(profile) error = %v", err)
	}
	client, err := clientSvc.CreateClient(ctx, "local", profile.ID, WireGuardClientInput{Name: "phone"})
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	beforeRows, err := store.ListWireGuardClients(ctx, "local", profile.ID)
	if err != nil {
		t.Fatalf("ListWireGuardClients(before) error = %v", err)
	}
	beforeProfiles, err := store.ListWireGuardProfiles(ctx, "local")
	if err != nil {
		t.Fatalf("ListWireGuardProfiles(before) error = %v", err)
	}

	_, err = clientSvc.UpdateClient(ctx, "local", profile.ID, client.ID, WireGuardClientInput{})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("UpdateClient(missing enabled) error = %v, want ErrInvalidArgument", err)
	}

	afterRows, err := store.ListWireGuardClients(ctx, "local", profile.ID)
	if err != nil {
		t.Fatalf("ListWireGuardClients(after) error = %v", err)
	}
	if len(afterRows) != len(beforeRows) || afterRows[0] != beforeRows[0] {
		t.Fatalf("UpdateClient(missing enabled) mutated client rows: before=%+v after=%+v", beforeRows, afterRows)
	}
	afterProfiles, err := store.ListWireGuardProfiles(ctx, "local")
	if err != nil {
		t.Fatalf("ListWireGuardProfiles(after) error = %v", err)
	}
	if len(afterProfiles) != len(beforeProfiles) || afterProfiles[0] != beforeProfiles[0] {
		t.Fatalf("UpdateClient(missing enabled) mutated profiles: before=%+v after=%+v", beforeProfiles, afterProfiles)
	}
}

func TestWireGuardClientUpdateMissingClientReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	_, profileSvc, clientSvc := newTestWireGuardClientService(t)

	input := testWireGuardProfileInput()
	input.Addresses = []string{"10.8.0.1/24"}
	input.PublicEndpoint = "wg.example.com:51820"
	input.Peers[0].Endpoint = ""
	profile, err := profileSvc.Create(ctx, "local", input)
	if err != nil {
		t.Fatalf("Create(profile) error = %v", err)
	}

	enabled := false
	_, err = clientSvc.UpdateClient(ctx, "local", profile.ID, 404, WireGuardClientInput{Enabled: &enabled})
	if !errors.Is(err, ErrWireGuardClientNotFound) {
		t.Fatalf("UpdateClient(missing) error = %v, want ErrWireGuardClientNotFound", err)
	}
}

func TestWireGuardClientConfigRejectsMissingEndpoint(t *testing.T) {
	ctx := context.Background()
	_, profileSvc, clientSvc := newTestWireGuardClientService(t)

	input := testWireGuardProfileInput()
	input.Addresses = []string{"10.8.0.1/24"}
	input.PublicEndpoint = ""
	input.Peers[0].Endpoint = ""
	profile, err := profileSvc.Create(ctx, "local", input)
	if err != nil {
		t.Fatalf("Create(profile) error = %v", err)
	}
	client, err := clientSvc.CreateClient(ctx, "local", profile.ID, WireGuardClientInput{Name: "phone"})
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}

	_, err = clientSvc.ClientConfig(ctx, "local", profile.ID, client.ID)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("ClientConfig() error = %v, want ErrInvalidArgument", err)
	}
	if err == nil || !strings.Contains(err.Error(), "public endpoint") {
		t.Fatalf("ClientConfig() error = %v, want public endpoint message", err)
	}
}

func TestWireGuardClientCreateSkipsManualPeerAllowedIP(t *testing.T) {
	ctx := context.Background()
	_, profileSvc, clientSvc := newTestWireGuardClientService(t)

	input := testWireGuardProfileInput()
	input.Addresses = []string{"10.8.0.1/24"}
	input.PublicEndpoint = "wg.example.com:51820"
	input.Peers[0].Endpoint = ""
	input.Peers[0].AllowedIPs = []string{"10.8.0.2/32"}
	profile, err := profileSvc.Create(ctx, "local", input)
	if err != nil {
		t.Fatalf("Create(profile) error = %v", err)
	}

	client, err := clientSvc.CreateClient(ctx, "local", profile.ID, WireGuardClientInput{Name: "phone"})
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	if client.Address != "10.8.0.3/32" {
		t.Fatalf("CreateClient() address = %q, want 10.8.0.3/32", client.Address)
	}
}

func TestWireGuardClientCreateSkipsManualPeerAllowedIPPrefixRange(t *testing.T) {
	ctx := context.Background()
	_, profileSvc, clientSvc := newTestWireGuardClientService(t)

	input := testWireGuardProfileInput()
	input.Addresses = []string{"10.8.0.1/24"}
	input.PublicEndpoint = "wg.example.com:51820"
	input.Peers[0].Endpoint = ""
	input.Peers[0].AllowedIPs = []string{"10.8.0.2/31"}
	profile, err := profileSvc.Create(ctx, "local", input)
	if err != nil {
		t.Fatalf("Create(profile) error = %v", err)
	}

	client, err := clientSvc.CreateClient(ctx, "local", profile.ID, WireGuardClientInput{Name: "phone"})
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	if client.Address != "10.8.0.4/32" {
		t.Fatalf("CreateClient() address = %q, want 10.8.0.4/32", client.Address)
	}
}

func TestWireGuardProfileDeleteRemovesClients(t *testing.T) {
	ctx := context.Background()
	store, profileSvc, clientSvc := newTestWireGuardClientService(t)

	input := testWireGuardProfileInput()
	input.Addresses = []string{"10.8.0.1/24"}
	input.PublicEndpoint = "wg.example.com:51820"
	input.Peers[0].Endpoint = ""
	profile, err := profileSvc.Create(ctx, "local", input)
	if err != nil {
		t.Fatalf("Create(profile) error = %v", err)
	}
	if _, err := clientSvc.CreateClient(ctx, "local", profile.ID, WireGuardClientInput{Name: "phone"}); err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}

	if _, err := profileSvc.Delete(ctx, "local", profile.ID); err != nil {
		t.Fatalf("Delete(profile) error = %v", err)
	}
	clients, err := store.ListWireGuardClients(ctx, "local", profile.ID)
	if err != nil {
		t.Fatalf("ListWireGuardClients() error = %v", err)
	}
	if len(clients) != 0 {
		t.Fatalf("clients after profile delete = %+v, want none", clients)
	}

	recreatedInput := input
	recreatedInput.ID = profile.ID
	recreated, err := profileSvc.Create(ctx, "local", recreatedInput)
	if err != nil {
		t.Fatalf("Create(reuse profile id) error = %v", err)
	}
	if recreated.ID != profile.ID {
		t.Fatalf("Create(reuse profile id) ID = %d, want %d", recreated.ID, profile.ID)
	}
	clients, err = store.ListWireGuardClients(ctx, "local", recreated.ID)
	if err != nil {
		t.Fatalf("ListWireGuardClients(reused) error = %v", err)
	}
	if len(clients) != 0 {
		t.Fatalf("clients for reused profile id = %+v, want none", clients)
	}
}

func newTestWireGuardClientService(t *testing.T) (*storage.SQLiteStore, *wireGuardProfileService, *wireGuardClientService) {
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
	return store, NewWireGuardProfileService(cfg, store), NewWireGuardClientService(cfg, store)
}

type staleWireGuardClientListStore struct {
	*storage.SQLiteStore
	staleClients []storage.WireGuardClientRow
}

func (s *staleWireGuardClientListStore) ListWireGuardClients(_ context.Context, _ string, _ int) ([]storage.WireGuardClientRow, error) {
	return append([]storage.WireGuardClientRow(nil), s.staleClients...), nil
}
