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

	rows, err := store.ListWireGuardClients(ctx, "local", profile.ID)
	if err != nil {
		t.Fatalf("ListWireGuardClients() error = %v", err)
	}
	if len(rows) != 1 || rows[0].PrivateKey == "" || rows[0].PresharedKey == "" {
		t.Fatalf("client row secrets were not persisted: %+v", rows)
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
