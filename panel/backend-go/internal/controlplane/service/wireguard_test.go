package service

import (
	"context"
	"encoding/json"
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

func TestWireGuardProfileCreateGeneratesBlankPrivateKey(t *testing.T) {
	ctx := context.Background()
	store, svc := newTestWireGuardProfileService(t)

	input := testWireGuardProfileInput()
	input.PrivateKey = ""
	profile, err := svc.Create(ctx, "local", input)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if profile.PrivateKey != redactedProxyPassword {
		t.Fatalf("Create() private_key = %q, want redacted", profile.PrivateKey)
	}

	rawRows, err := store.ListWireGuardProfiles(ctx, "local")
	if err != nil {
		t.Fatalf("ListWireGuardProfiles() error = %v", err)
	}
	if len(rawRows) != 1 {
		t.Fatalf("raw row length = %d, want 1", len(rawRows))
	}
	if rawRows[0].PrivateKey == "" || rawRows[0].PrivateKey == testWireGuardPrivateKey {
		t.Fatalf("raw private_key = %q, want generated key", rawRows[0].PrivateKey)
	}
	if err := validateWireGuardKey(rawRows[0].PrivateKey, true); err != nil {
		t.Fatalf("raw private_key validation error = %v", err)
	}
}

func TestWireGuardProfileServiceEnsureDefaultCreatesProfile(t *testing.T) {
	store, svc := newTestWireGuardProfileService(t)
	agentID := "local"
	profile, err := svc.EnsureDefault(t.Context(), agentID)
	if err != nil {
		t.Fatalf("EnsureDefault() error = %v", err)
	}
	if profile.ID <= 0 || profile.Name != "Default WireGuard" || profile.ListenPort != 51820 || len(profile.Addresses) != 1 || profile.Enabled != true {
		t.Fatalf("profile = %+v", profile)
	}
	rows, err := store.ListWireGuardProfiles(t.Context(), agentID)
	if err != nil {
		t.Fatalf("ListWireGuardProfiles() error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want one default profile", rows)
	}
}

func TestWireGuardProfileServiceEnsureDefaultReusesExistingDefault(t *testing.T) {
	store, svc := newTestWireGuardProfileService(t)
	first, err := svc.EnsureDefault(t.Context(), "local")
	if err != nil {
		t.Fatalf("EnsureDefault(first) error = %v", err)
	}
	second, err := svc.EnsureDefault(t.Context(), "local")
	if err != nil {
		t.Fatalf("EnsureDefault(second) error = %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("default profile IDs = %d and %d, want reuse", first.ID, second.ID)
	}
	rows, err := store.ListWireGuardProfiles(t.Context(), "local")
	if err != nil {
		t.Fatalf("ListWireGuardProfiles() error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want one reused default profile", rows)
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

func TestWireGuardProfileCreateRejectsRemoteAgentWithoutWireGuardCapability(t *testing.T) {
	ctx := context.Background()
	store, svc := newTestWireGuardProfileService(t)
	if err := store.SaveAgent(ctx, storage.AgentRow{
		ID:               "edge-no-wg",
		Name:             "edge-no-wg",
		AgentToken:       "token-edge-no-wg",
		CapabilitiesJSON: `["http_rules","l4"]`,
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}

	_, err := svc.Create(ctx, "edge-no-wg", testWireGuardProfileInput())
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() error = %v, want ErrInvalidArgument", err)
	}
	if err == nil || !strings.Contains(err.Error(), "agent does not support WireGuard") {
		t.Fatalf("Create() error = %v, want wireguard capability message", err)
	}
	rows, err := store.ListWireGuardProfiles(ctx, "edge-no-wg")
	if err != nil {
		t.Fatalf("ListWireGuardProfiles() error = %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("stored wireguard profiles = %+v, want none", rows)
	}
}

func TestWireGuardProfileUpdateRejectsRemoteAgentWithoutWireGuardCapability(t *testing.T) {
	ctx := context.Background()
	store, svc := newTestWireGuardProfileService(t)
	if err := store.SaveAgent(ctx, storage.AgentRow{
		ID:               "edge-no-wg",
		Name:             "edge-no-wg",
		AgentToken:       "token-edge-no-wg",
		CapabilitiesJSON: `["http_rules","l4"]`,
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	seed := wireGuardProfileToRow(WireGuardProfile{
		ID:         41,
		AgentID:    "edge-no-wg",
		Name:       "seed",
		Mode:       "generic_wireguard",
		PrivateKey: testWireGuardPrivateKey,
		Addresses:  []string{"10.44.0.1/24"},
		Peers:      []WireGuardPeer{},
		Enabled:    false,
		Revision:   1,
	})
	if err := store.SaveWireGuardProfiles(ctx, "edge-no-wg", []storage.WireGuardProfileRow{seed}); err != nil {
		t.Fatalf("SaveWireGuardProfiles() error = %v", err)
	}

	update := testWireGuardProfileInput()
	update.Name = "updated"
	_, err := svc.Update(ctx, "edge-no-wg", 41, update)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Update() error = %v, want ErrInvalidArgument", err)
	}
	if err == nil || !strings.Contains(err.Error(), "agent does not support WireGuard") {
		t.Fatalf("Update() error = %v, want wireguard capability message", err)
	}
	rows, err := store.ListWireGuardProfiles(ctx, "edge-no-wg")
	if err != nil {
		t.Fatalf("ListWireGuardProfiles() error = %v", err)
	}
	if len(rows) != 1 || rows[0] != seed {
		t.Fatalf("stored wireguard profiles = %+v, want unchanged seed %+v", rows, seed)
	}
}

func TestWireGuardProfileCreateAllocatesAddressWhenOmitted(t *testing.T) {
	ctx := context.Background()
	_, svc := newTestWireGuardProfileService(t)

	input := testWireGuardProfileInput()
	input.Addresses = nil
	created, err := svc.Create(ctx, "local", input)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if len(created.Addresses) != 1 || created.Addresses[0] != "10.8.0.1/24" {
		t.Fatalf("Create() addresses = %+v, want allocated 10.8.0.1/24", created.Addresses)
	}
}

func TestWireGuardProfileCreateAllocatesNextAvailableAddress(t *testing.T) {
	ctx := context.Background()
	_, svc := newTestWireGuardProfileService(t)

	first := testWireGuardProfileInput()
	first.Addresses = []string{"10.8.0.1/24"}
	if _, err := svc.Create(ctx, "local", first); err != nil {
		t.Fatalf("Create(first) error = %v", err)
	}

	second := testWireGuardProfileInput()
	second.PrivateKey = testWireGuardPresharedKey
	second.Peers[0].PublicKey = testWireGuardPublicKeyB
	second.Peers[0].PresharedKey = testWireGuardPresharedKeyB
	second.Addresses = nil
	second.ListenPort = 51821
	created, err := svc.Create(ctx, "local", second)
	if err != nil {
		t.Fatalf("Create(second) error = %v", err)
	}
	if len(created.Addresses) != 1 || created.Addresses[0] != "10.8.1.1/24" {
		t.Fatalf("Create(second) addresses = %+v, want allocated 10.8.1.1/24", created.Addresses)
	}
}

func TestWireGuardProfileEnsureDefaultAllocatesNextGlobalAddressAcrossAgents(t *testing.T) {
	ctx := context.Background()
	store, svc := newTestWireGuardProfileService(t)

	if err := store.SaveAgent(ctx, storage.AgentRow{
		ID:               "remote",
		Name:             "remote",
		AgentToken:       "token-remote",
		CapabilitiesJSON: `["wireguard"]`,
	}); err != nil {
		t.Fatalf("SaveAgent(remote) error = %v", err)
	}

	remoteProfile, err := svc.EnsureDefault(ctx, "remote")
	if err != nil {
		t.Fatalf("EnsureDefault(remote) error = %v", err)
	}
	if len(remoteProfile.Addresses) != 1 || remoteProfile.Addresses[0] != "10.8.0.1/24" {
		t.Fatalf("EnsureDefault(remote) addresses = %+v, want allocated 10.8.0.1/24", remoteProfile.Addresses)
	}

	localProfile, err := svc.EnsureDefault(ctx, "local")
	if err != nil {
		t.Fatalf("EnsureDefault(local) error = %v", err)
	}
	if len(localProfile.Addresses) != 1 || localProfile.Addresses[0] != "10.8.1.1/24" {
		t.Fatalf("EnsureDefault(local) addresses = %+v, want allocated 10.8.1.1/24", localProfile.Addresses)
	}
}

func TestWireGuardProfileCreateAllocatesNextGlobalAddressAcrossAgentsWhenOmitted(t *testing.T) {
	ctx := context.Background()
	store, svc := newTestWireGuardProfileService(t)

	if err := store.SaveAgent(ctx, storage.AgentRow{
		ID:               "remote",
		Name:             "remote",
		AgentToken:       "token-remote",
		CapabilitiesJSON: `["wireguard"]`,
	}); err != nil {
		t.Fatalf("SaveAgent(remote) error = %v", err)
	}
	if _, err := svc.EnsureDefault(ctx, "remote"); err != nil {
		t.Fatalf("EnsureDefault(remote) error = %v", err)
	}

	input := testWireGuardProfileInput()
	input.Addresses = nil
	created, err := svc.Create(ctx, "local", input)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if len(created.Addresses) != 1 || created.Addresses[0] != "10.8.1.1/24" {
		t.Fatalf("Create() addresses = %+v, want allocated 10.8.1.1/24", created.Addresses)
	}
}

func TestWireGuardProfileCreateAllowsEmptyPeersForGeneratedClientBootstrap(t *testing.T) {
	ctx := context.Background()
	store, svc := newTestWireGuardProfileService(t)

	input := testWireGuardProfileInput()
	input.Peers = []WireGuardPeer{}
	created, err := svc.Create(ctx, "local", input)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if len(created.Peers) != 0 {
		t.Fatalf("Create() peers = %+v, want empty generated-client bootstrap profile", created.Peers)
	}

	rawRows, err := store.ListWireGuardProfiles(ctx, "local")
	if err != nil {
		t.Fatalf("ListWireGuardProfiles() error = %v", err)
	}
	if len(rawRows) != 1 {
		t.Fatalf("raw row length = %d, want 1", len(rawRows))
	}
	rawProfile := wireGuardProfileFromRow(rawRows[0])
	if !rawProfile.Enabled || len(rawProfile.Peers) != 0 {
		t.Fatalf("raw profile = %+v, want enabled profile with no runtime peers until generated clients are added", rawProfile)
	}
}

func TestWireGuardProfileCreateRejectsDNSHostname(t *testing.T) {
	ctx := context.Background()
	_, svc := newTestWireGuardProfileService(t)

	input := testWireGuardProfileInput()
	input.DNS = []string{"dns.example.com"}
	_, err := svc.Create(ctx, "local", input)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() error = %v, want ErrInvalidArgument", err)
	}
	if err == nil || !strings.Contains(err.Error(), "dns must be IP addresses") {
		t.Fatalf("Create() error = %v, want DNS IP address message", err)
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

func TestWireGuardProfileRejectsDuplicateEnabledListenPort(t *testing.T) {
	ctx := context.Background()
	_, svc := newTestWireGuardProfileService(t)

	first := testWireGuardProfileInput()
	first.ListenPort = 51820
	created, err := svc.Create(ctx, "local", first)
	if err != nil {
		t.Fatalf("Create(first) error = %v", err)
	}

	duplicate := testWireGuardProfileInput()
	duplicate.Name = "wg duplicate"
	duplicate.PrivateKey = testWireGuardPresharedKey
	duplicate.ListenPort = 51820
	duplicate.Peers[0].PublicKey = testWireGuardPublicKeyB
	duplicate.Peers[0].PresharedKey = testWireGuardPresharedKeyB
	_, err = svc.Create(ctx, "local", duplicate)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create(duplicate) error = %v, want ErrInvalidArgument", err)
	}
	if err == nil || !strings.Contains(err.Error(), "duplicate listen_port") {
		t.Fatalf("Create(duplicate) error = %v, want duplicate listen_port message", err)
	}

	disabled := duplicate
	disabled.Enabled = boolPtr(false)
	disabledCreated, err := svc.Create(ctx, "local", disabled)
	if err != nil {
		t.Fatalf("Create(disabled duplicate) error = %v", err)
	}

	enableDuplicate := disabled
	enableDuplicate.PrivateKey = redactedProxyPassword
	enableDuplicate.Peers[0].PresharedKey = redactedProxyPassword
	enableDuplicate.Enabled = boolPtr(true)
	_, err = svc.Update(ctx, "local", disabledCreated.ID, enableDuplicate)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Update(enable duplicate) error = %v, want ErrInvalidArgument", err)
	}

	zeroPort := duplicate
	zeroPort.ListenPort = 0
	zeroPort.Enabled = boolPtr(true)
	zeroPort.PrivateKey = testWireGuardPublicKey
	zeroPort.Peers[0].PublicKey = testWireGuardPrivateKey
	zeroPort.Peers[0].PresharedKey = testWireGuardPresharedKey
	if _, err := svc.Create(ctx, "local", zeroPort); err != nil {
		t.Fatalf("Create(zero listen_port) error = %v", err)
	}

	if _, err := svc.Delete(ctx, "local", created.ID); err != nil {
		t.Fatalf("Delete(first) error = %v", err)
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

func TestWireGuardProfileUpdateRejectsExplicitEmptyAddresses(t *testing.T) {
	ctx := context.Background()
	_, svc := newTestWireGuardProfileService(t)

	created, err := svc.Create(ctx, "local", testWireGuardProfileInput())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	var update WireGuardProfileInput
	if err := json.Unmarshal([]byte(`{
		"name":"wg relay",
		"mode":"generic_wireguard",
		"private_key":"xxxxx",
		"addresses":[],
		"peers":[{
			"name":"peer-a",
			"public_key":"`+testWireGuardPublicKey+`",
			"preshared_key":"xxxxx",
			"endpoint":"example.com:51820",
			"allowed_ips":["10.0.0.2/32"],
			"persistent_keepalive_seconds":25
		}],
		"dns":["1.1.1.1"],
		"mtu":1420,
		"enabled":true,
		"tags":["relay"]
	}`), &update); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	_, err = svc.Update(ctx, "local", created.ID, update)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Update() error = %v, want ErrInvalidArgument", err)
	}
	if err == nil || !strings.Contains(err.Error(), "addresses is required") {
		t.Fatalf("Update() error = %v, want addresses required message", err)
	}
}

func TestWireGuardProfileUpdateAcceptsExplicitEmptyPeers(t *testing.T) {
	ctx := context.Background()
	store, svc := newTestWireGuardProfileService(t)

	created, err := svc.Create(ctx, "local", testWireGuardProfileInput())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	var update WireGuardProfileInput
	if err := json.Unmarshal([]byte(`{
		"name":"wg relay",
		"mode":"generic_wireguard",
		"private_key":"xxxxx",
		"addresses":["10.0.0.1/24"],
		"peers":[],
		"dns":["1.1.1.1"],
		"mtu":1420,
		"enabled":true,
		"tags":["relay"]
	}`), &update); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	updated, err := svc.Update(ctx, "local", created.ID, update)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if len(updated.Peers) != 0 {
		t.Fatalf("Update() peers = %+v, want explicit empty peer list", updated.Peers)
	}

	rawRows, err := store.ListWireGuardProfiles(ctx, "local")
	if err != nil {
		t.Fatalf("ListWireGuardProfiles() error = %v", err)
	}
	rawProfile := wireGuardProfileFromRow(rawRows[0])
	if len(rawProfile.Peers) != 0 {
		t.Fatalf("raw profile peers = %+v, want explicit empty peer list", rawProfile.Peers)
	}
}

func TestWireGuardProfileUpdateCanClearListenPortFromJSONNull(t *testing.T) {
	ctx := context.Background()
	store, svc := newTestWireGuardProfileService(t)

	created, err := svc.Create(ctx, "local", testWireGuardProfileInput())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	var update WireGuardProfileInput
	if err := json.Unmarshal([]byte(`{
		"name":"wg relay",
		"mode":"generic_wireguard",
		"private_key":"xxxxx",
		"listen_port":null,
		"addresses":["10.0.0.1/24"],
		"peers":[{
			"name":"peer-a",
			"public_key":"`+testWireGuardPublicKey+`",
			"preshared_key":"xxxxx",
			"endpoint":"example.com:51820",
			"allowed_ips":["10.0.0.2/32"],
			"persistent_keepalive_seconds":25
		}],
		"dns":["1.1.1.1"],
		"mtu":1420,
		"enabled":true,
		"tags":["relay"]
	}`), &update); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	updated, err := svc.Update(ctx, "local", created.ID, update)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.ListenPort != 0 {
		t.Fatalf("Update() listen_port = %d, want 0", updated.ListenPort)
	}

	rawRows, err := store.ListWireGuardProfiles(ctx, "local")
	if err != nil {
		t.Fatalf("ListWireGuardProfiles() error = %v", err)
	}
	if len(rawRows) != 1 || rawRows[0].ListenPort != 0 {
		t.Fatalf("raw listen_port rows = %+v, want listen_port 0", rawRows)
	}
}

func TestWireGuardProfileUpdateBumpsAgentsThatReferenceRelayProfile(t *testing.T) {
	ctx := context.Background()
	store, svc := newTestWireGuardProfileService(t)

	if err := store.SaveAgent(ctx, storage.AgentRow{
		ID:               "relay-agent",
		Name:             "relay-agent",
		AgentToken:       "token-relay",
		DesiredRevision:  1,
		CurrentRevision:  1,
		CapabilitiesJSON: `["wireguard"]`,
	}); err != nil {
		t.Fatalf("SaveAgent(relay-agent) error = %v", err)
	}
	if err := store.SaveAgent(ctx, storage.AgentRow{
		ID:              "client-agent",
		Name:            "client-agent",
		AgentToken:      "token-client",
		DesiredRevision: 50,
		CurrentRevision: 50,
	}); err != nil {
		t.Fatalf("SaveAgent(client-agent) error = %v", err)
	}

	input := testWireGuardProfileInput()
	input.ListenPort = 51820
	created, err := svc.Create(ctx, "relay-agent", input)
	if err != nil {
		t.Fatalf("Create(relay profile) error = %v", err)
	}

	profileID := created.ID
	if err := store.SaveRelayListeners(ctx, "relay-agent", []storage.RelayListenerRow{{
		ID:                 100,
		AgentID:            "relay-agent",
		Name:               "wg-relay",
		ListenPort:         8443,
		Enabled:            true,
		TransportMode:      "wireguard",
		WireGuardProfileID: &profileID,
		Revision:           2,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners(relay-agent) error = %v", err)
	}
	if err := store.SaveHTTPRules(ctx, "client-agent", []storage.HTTPRuleRow{{
		ID:              200,
		AgentID:         "client-agent",
		FrontendURL:     "https://client.example.com",
		BackendsJSON:    `[{"url":"http://127.0.0.1:8096"}]`,
		Enabled:         true,
		RelayLayersJSON: `[[100]]`,
		Revision:        50,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules(client-agent) error = %v", err)
	}

	update := testWireGuardProfileInput()
	update.PrivateKey = redactedProxyPassword
	update.Peers[0].PresharedKey = redactedProxyPassword
	update.Peers[0].Endpoint = "updated.example.com:51820"
	if _, err := svc.Update(ctx, "relay-agent", created.ID, update); err != nil {
		t.Fatalf("Update(relay profile) error = %v", err)
	}

	agents, err := store.ListAgents(ctx)
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	var client storage.AgentRow
	for _, row := range agents {
		if row.ID == "client-agent" {
			client = row
			break
		}
	}
	if client.ID == "" {
		t.Fatal("client-agent not found")
	}
	if client.DesiredRevision <= client.CurrentRevision {
		t.Fatalf("client-agent revisions = desired %d current %d, want desired bumped above current", client.DesiredRevision, client.CurrentRevision)
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
		name        string
		wantMessage string
		seed        func(*testing.T, *storage.SQLiteStore, int)
	}{
		{
			name:        "relay listener",
			wantMessage: "wireguard profile is referenced",
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
			name:        "l4 listen",
			wantMessage: "wireguard profile is referenced",
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
			name:        "l4 egress",
			wantMessage: "wireguard profile is referenced",
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
		{
			name:        "http wireguard entry",
			wantMessage: "HTTP rule 103",
			seed: func(t *testing.T, store *storage.SQLiteStore, profileID int) {
				t.Helper()
				if err := store.SaveHTTPRules(ctx, "local", []storage.HTTPRuleRow{{
					ID:                       103,
					AgentID:                  "local",
					FrontendURL:              "http://app.example.com",
					BackendsJSON:             `[{"url":"http://127.0.0.1:8096"}]`,
					LoadBalancingJSON:        `{"strategy":"adaptive"}`,
					Enabled:                  false,
					TagsJSON:                 `[]`,
					RelayChainJSON:           `[]`,
					RelayLayersJSON:          `[]`,
					CustomHeadersJSON:        `[]`,
					WireGuardEntryEnabled:    true,
					WireGuardProfileID:       &profileID,
					WireGuardEntryListenHost: "10.0.0.1",
					WireGuardEntryListenPort: 8080,
					Revision:                 1,
				}}); err != nil {
					t.Fatalf("SaveHTTPRules() error = %v", err)
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
			if err == nil || !strings.Contains(err.Error(), "wireguard profile is referenced") || !strings.Contains(err.Error(), tt.wantMessage) {
				t.Fatalf("Update() error = %v, want referenced message containing %q", err, tt.wantMessage)
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
			if err == nil || !strings.Contains(err.Error(), "wireguard profile is referenced") || !strings.Contains(err.Error(), tt.wantMessage) {
				t.Fatalf("Delete() error = %v, want referenced message containing %q", err, tt.wantMessage)
			}
		})
	}
}

func TestWireGuardProfileUpdateRejectsRemovingAddressUsedByDependentListener(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name        string
		wantMessage string
		seed        func(*testing.T, *storage.SQLiteStore, int)
	}{
		{
			name:        "relay listener",
			wantMessage: "relay listener 200",
			seed: func(t *testing.T, store *storage.SQLiteStore, profileID int) {
				t.Helper()
				if err := store.SaveRelayListeners(ctx, "local", []storage.RelayListenerRow{{
					ID:                 200,
					AgentID:            "local",
					Name:               "wg relay",
					ListenHost:         "10.0.0.1",
					BindHostsJSON:      `["10.0.0.1"]`,
					ListenPort:         7443,
					PublicHost:         "",
					PublicPort:         0,
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
			name:        "l4 listen",
			wantMessage: "l4 rule 201",
			seed: func(t *testing.T, store *storage.SQLiteStore, profileID int) {
				t.Helper()
				if err := store.SaveL4Rules(ctx, "local", []storage.L4RuleRow{{
					ID:                   201,
					AgentID:              "local",
					Name:                 "wg listen",
					Protocol:             "tcp",
					ListenHost:           "0.0.0.0",
					ListenPort:           9443,
					BackendsJSON:         `[{"host":"127.0.0.1","port":9444}]`,
					LoadBalancingJSON:    `{"strategy":"adaptive"}`,
					TuningJSON:           `{"proxy_protocol":{"decode":false,"send":false}}`,
					RelayLayersJSON:      `[]`,
					ListenMode:           "wireguard",
					WireGuardProfileID:   &profileID,
					WireGuardInboundMode: "address",
					WireGuardListenHost:  "10.0.0.1",
					Enabled:              true,
					TagsJSON:             `[]`,
					Revision:             1,
				}}); err != nil {
					t.Fatalf("SaveL4Rules() error = %v", err)
				}
			},
		},
		{
			name:        "http wireguard entry",
			wantMessage: "HTTP rule 202",
			seed: func(t *testing.T, store *storage.SQLiteStore, profileID int) {
				t.Helper()
				if err := store.SaveHTTPRules(ctx, "local", []storage.HTTPRuleRow{{
					ID:                       202,
					AgentID:                  "local",
					FrontendURL:              "http://app.example.com",
					BackendsJSON:             `[{"url":"http://127.0.0.1:8096"}]`,
					LoadBalancingJSON:        `{"strategy":"adaptive"}`,
					Enabled:                  true,
					TagsJSON:                 `[]`,
					RelayChainJSON:           `[]`,
					RelayLayersJSON:          `[]`,
					CustomHeadersJSON:        `[]`,
					WireGuardEntryEnabled:    true,
					WireGuardProfileID:       &profileID,
					WireGuardEntryListenHost: "10.0.0.1",
					WireGuardEntryListenPort: 80,
					Revision:                 1,
				}}); err != nil {
					t.Fatalf("SaveHTTPRules() error = %v", err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, svc := newTestWireGuardProfileService(t)
			created, err := svc.Create(ctx, "local", testWireGuardProfileInput())
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			tt.seed(t, store, created.ID)

			update := testWireGuardProfileInput()
			update.PrivateKey = redactedProxyPassword
			update.Peers[0].PresharedKey = redactedProxyPassword
			update.Addresses = []string{"10.0.0.2/24"}
			_, err = svc.Update(ctx, "local", created.ID, update)
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("Update() error = %v, want ErrInvalidArgument", err)
			}
			if err == nil || !strings.Contains(err.Error(), "wireguard profile address is referenced") || !strings.Contains(err.Error(), tt.wantMessage) {
				t.Fatalf("Update() error = %v, want address referenced message containing %q", err, tt.wantMessage)
			}

			rows, err := store.ListWireGuardProfiles(ctx, "local")
			if err != nil {
				t.Fatalf("ListWireGuardProfiles() error = %v", err)
			}
			if len(rows) != 1 || rows[0].AddressesJSON != `["10.0.0.1/24"]` {
				t.Fatalf("profile rows after rejected update = %+v", rows)
			}
		})
	}
}

func TestWireGuardProfileUpdateRejectsShrinkingAddressesWithExistingClients(t *testing.T) {
	ctx := context.Background()
	store, svc := newTestWireGuardProfileService(t)

	input := testWireGuardProfileInput()
	input.Addresses = []string{"10.0.0.1/24"}
	created, err := svc.Create(ctx, "local", input)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	clientSvc := NewWireGuardClientService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)
	if _, err := clientSvc.CreateClient(ctx, "local", created.ID, WireGuardClientInput{Name: "phone"}); err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}

	update := input
	update.PrivateKey = redactedProxyPassword
	update.Peers[0].PresharedKey = redactedProxyPassword
	update.Addresses = []string{"10.0.1.1/24"}

	_, err = svc.Update(ctx, "local", created.ID, update)
	if err == nil || !strings.Contains(err.Error(), "wireguard client") {
		t.Fatalf("Update() error = %v, want client-based rejection", err)
	}

	rows, err := store.ListWireGuardProfiles(ctx, "local")
	if err != nil {
		t.Fatalf("ListWireGuardProfiles() error = %v", err)
	}
	if len(rows) != 1 || rows[0].AddressesJSON != `["10.0.0.1/24"]` {
		t.Fatalf("profile rows after rejected update = %+v", rows)
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
		ID:               "edge-1",
		Name:             "edge-1",
		DesiredRevision:  8,
		CurrentRevision:  11,
		CapabilitiesJSON: `["wireguard"]`,
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
		ID:               "edge-1",
		Name:             "edge-1",
		CapabilitiesJSON: `["wireguard"]`,
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
