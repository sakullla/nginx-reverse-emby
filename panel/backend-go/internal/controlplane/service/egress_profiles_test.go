package service

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestEgressProfileServiceCreateRedactsProxyURLInOutput(t *testing.T) {
	store := newEgressProfileTestStore(t)
	svc := NewEgressProfileService(store)

	profile, err := svc.Create(t.Context(), EgressProfileInput{
		Name:     stringPtrEgress("office socks"),
		Type:     stringPtrEgress("socks"),
		ProxyURL: stringPtrEgress("socks5://user:secret@127.0.0.1:1080"),
		Enabled:  boolPtrEgress(true),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if profile.ProxyURL != "socks5://user:xxxxx@127.0.0.1:1080" {
		t.Fatalf("ProxyURL = %q, want redacted password", profile.ProxyURL)
	}

	rows, err := store.ListEgressProfiles(t.Context())
	if err != nil {
		t.Fatalf("ListEgressProfiles() error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("stored profile count = %d, want 1", len(rows))
	}
	if rows[0].ProxyURL != "socks5://user:secret@127.0.0.1:1080" {
		t.Fatalf("stored ProxyURL = %q, want raw secret preserved", rows[0].ProxyURL)
	}
}

func TestEgressProfileServiceCreateValidatesProfileTypesAndSchemes(t *testing.T) {
	tests := []struct {
		name     string
		input    EgressProfileInput
		wantType string
	}{
		{
			name: "direct clears transport-specific fields",
			input: EgressProfileInput{
				Name:            stringPtrEgress("direct"),
				Type:            stringPtrEgress("direct"),
				ProxyURL:        stringPtrEgress("socks5://user:secret@127.0.0.1:1080"),
				WireGuardConfig: testEgressWireGuardConfig(),
			},
			wantType: "direct",
		},
		{
			name: "socks accepts socks scheme",
			input: EgressProfileInput{
				Name:     stringPtrEgress("socks proxy"),
				Type:     stringPtrEgress("socks"),
				ProxyURL: stringPtrEgress("socks://127.0.0.1:1080"),
			},
			wantType: "socks",
		},
		{
			name: "socks accepts socks5 scheme",
			input: EgressProfileInput{
				Name:     stringPtrEgress("socks5 proxy"),
				Type:     stringPtrEgress("socks"),
				ProxyURL: stringPtrEgress("socks5://127.0.0.1:1080"),
			},
			wantType: "socks",
		},
		{
			name: "socks accepts socks5h scheme",
			input: EgressProfileInput{
				Name:     stringPtrEgress("socks5h proxy"),
				Type:     stringPtrEgress("socks"),
				ProxyURL: stringPtrEgress("socks5h://127.0.0.1:1080"),
			},
			wantType: "socks",
		},
		{
			name: "http accepts http scheme",
			input: EgressProfileInput{
				Name:     stringPtrEgress("http proxy"),
				Type:     stringPtrEgress("http"),
				ProxyURL: stringPtrEgress("http://proxy.example.com:8080"),
			},
			wantType: "http",
		},
		{
			name: "wireguard accepts config",
			input: EgressProfileInput{
				Name:            stringPtrEgress("wg"),
				Type:            stringPtrEgress("wireguard"),
				ProxyURL:        stringPtrEgress("http://proxy.example.com"),
				WireGuardConfig: testEgressWireGuardConfig(),
			},
			wantType: "wireguard",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newEgressProfileTestStore(t)
			svc := NewEgressProfileService(store)

			profile, err := svc.Create(t.Context(), tc.input)
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			if profile.Type != tc.wantType {
				t.Fatalf("Type = %q, want %q", profile.Type, tc.wantType)
			}
			if profile.Type == "direct" && (profile.ProxyURL != "" || profile.WireGuardConfig != nil) {
				t.Fatalf("direct profile retained transport fields: %+v", profile)
			}
			if profile.Type == "wireguard" && profile.ProxyURL != "" {
				t.Fatalf("wireguard ProxyURL = %q, want empty", profile.ProxyURL)
			}
		})
	}
}

func TestEgressProfileServiceCreateRejectsProxyURLsUnsupportedByAgent(t *testing.T) {
	tests := []struct {
		name  string
		input EgressProfileInput
	}{
		{
			name: "http rejects https scheme",
			input: EgressProfileInput{
				Name:     stringPtrEgress("https proxy"),
				Type:     stringPtrEgress("http"),
				ProxyURL: stringPtrEgress("https://proxy.example.com:443"),
			},
		},
		{
			name: "http rejects missing port",
			input: EgressProfileInput{
				Name:     stringPtrEgress("http proxy"),
				Type:     stringPtrEgress("http"),
				ProxyURL: stringPtrEgress("http://proxy.example.com"),
			},
		},
		{
			name: "socks rejects missing port",
			input: EgressProfileInput{
				Name:     stringPtrEgress("socks proxy"),
				Type:     stringPtrEgress("socks"),
				ProxyURL: stringPtrEgress("socks5://proxy.example.com"),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newEgressProfileTestStore(t)
			svc := NewEgressProfileService(store)

			_, err := svc.Create(t.Context(), tc.input)
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("Create() error = %v, want ErrInvalidArgument", err)
			}
		})
	}
}

func TestEgressProfileServiceCreateRejectsInvalidProfileTypesAndSchemes(t *testing.T) {
	tests := []struct {
		name  string
		input EgressProfileInput
	}{
		{
			name: "unknown type",
			input: EgressProfileInput{
				Name: stringPtrEgress("bad"),
				Type: stringPtrEgress("ssh"),
			},
		},
		{
			name: "missing proxy url",
			input: EgressProfileInput{
				Name: stringPtrEgress("missing"),
				Type: stringPtrEgress("socks"),
			},
		},
		{
			name: "socks rejects http scheme",
			input: EgressProfileInput{
				Name:     stringPtrEgress("wrong socks"),
				Type:     stringPtrEgress("socks"),
				ProxyURL: stringPtrEgress("http://proxy.example.com"),
			},
		},
		{
			name: "http rejects socks scheme",
			input: EgressProfileInput{
				Name:     stringPtrEgress("wrong http"),
				Type:     stringPtrEgress("http"),
				ProxyURL: stringPtrEgress("socks5://127.0.0.1:1080"),
			},
		},
		{
			name: "proxy url requires host",
			input: EgressProfileInput{
				Name:     stringPtrEgress("bad proxy"),
				Type:     stringPtrEgress("http"),
				ProxyURL: stringPtrEgress("http:///missing-host"),
			},
		},
		{
			name: "wireguard requires config",
			input: EgressProfileInput{
				Name: stringPtrEgress("wg"),
				Type: stringPtrEgress("wireguard"),
			},
		},
		{
			name: "wireguard requires private key",
			input: EgressProfileInput{
				Name: stringPtrEgress("wg"),
				Type: stringPtrEgress("wireguard"),
				WireGuardConfig: &EgressWireGuardConfig{
					Addresses: []string{"10.0.0.2/32"},
				},
			},
		},
		{
			name: "wireguard requires addresses",
			input: EgressProfileInput{
				Name: stringPtrEgress("wg"),
				Type: stringPtrEgress("wireguard"),
				WireGuardConfig: &EgressWireGuardConfig{
					PrivateKey: testEgressWireGuardPrivateKey,
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newEgressProfileTestStore(t)
			svc := NewEgressProfileService(store)

			_, err := svc.Create(t.Context(), tc.input)
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("Create() error = %v, want ErrInvalidArgument", err)
			}
		})
	}
}

func TestEgressProfileServiceDeleteRejectsReferencesRegardlessOfEnabledState(t *testing.T) {
	tests := []struct {
		name string
		seed func(t *testing.T, store *storage.SQLiteStore, profileID int)
		want string
	}{
		{
			name: "enabled http rule",
			seed: func(t *testing.T, store *storage.SQLiteStore, profileID int) {
				t.Helper()
				if err := store.SaveHTTPRules(t.Context(), "local", []storage.HTTPRuleRow{{
					ID:              20,
					AgentID:         "local",
					FrontendURL:     "http://example.com",
					BackendsJSON:    `[{"url":"http://127.0.0.1:8096"}]`,
					EgressProfileID: &profileID,
					Enabled:         true,
					Revision:        1,
				}}); err != nil {
					t.Fatalf("SaveHTTPRules() error = %v", err)
				}
			},
			want: "HTTP rule 20",
		},
		{
			name: "disabled http rule",
			seed: func(t *testing.T, store *storage.SQLiteStore, profileID int) {
				t.Helper()
				if err := store.SaveHTTPRules(t.Context(), "local", []storage.HTTPRuleRow{{
					ID:              22,
					AgentID:         "local",
					FrontendURL:     "http://example.com",
					BackendsJSON:    `[{"url":"http://127.0.0.1:8096"}]`,
					EgressProfileID: &profileID,
					Enabled:         false,
					Revision:        1,
				}}); err != nil {
					t.Fatalf("SaveHTTPRules() error = %v", err)
				}
			},
			want: "HTTP rule 22",
		},
		{
			name: "enabled l4 rule",
			seed: func(t *testing.T, store *storage.SQLiteStore, profileID int) {
				t.Helper()
				if err := store.SaveL4Rules(t.Context(), "local", []storage.L4RuleRow{{
					ID:              21,
					AgentID:         "local",
					Name:            "enabled l4",
					Protocol:        "tcp",
					ListenHost:      "0.0.0.0",
					ListenPort:      8443,
					BackendsJSON:    `[{"host":"127.0.0.1","port":443}]`,
					EgressProfileID: &profileID,
					Enabled:         true,
					Revision:        1,
				}}); err != nil {
					t.Fatalf("SaveL4Rules() error = %v", err)
				}
			},
			want: "l4 rule 21",
		},
		{
			name: "disabled l4 rule",
			seed: func(t *testing.T, store *storage.SQLiteStore, profileID int) {
				t.Helper()
				if err := store.SaveL4Rules(t.Context(), "local", []storage.L4RuleRow{{
					ID:              23,
					AgentID:         "local",
					Name:            "disabled l4",
					Protocol:        "tcp",
					ListenHost:      "0.0.0.0",
					ListenPort:      8443,
					BackendsJSON:    `[{"host":"127.0.0.1","port":443}]`,
					EgressProfileID: &profileID,
					Enabled:         false,
					Revision:        1,
				}}); err != nil {
					t.Fatalf("SaveL4Rules() error = %v", err)
				}
			},
			want: "l4 rule 23",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newEgressProfileTestStore(t)
			svc := NewEgressProfileService(store)
			profile := createTestEgressProfile(t, svc)
			tc.seed(t, store, profile.ID)

			_, err := svc.Delete(t.Context(), profile.ID)
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("Delete() error = %v, want ErrInvalidArgument", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Delete() error = %v, want reference %q", err, tc.want)
			}
		})
	}
}

func TestEgressProfileServiceUpdateRejectsMismatchedBodyIDAndPreservesProfile(t *testing.T) {
	store := newEgressProfileTestStore(t)
	svc := NewEgressProfileService(store)
	profile, err := svc.Create(t.Context(), EgressProfileInput{
		Name:     stringPtrEgress("office socks"),
		Type:     stringPtrEgress("socks"),
		ProxyURL: stringPtrEgress("socks5://127.0.0.1:1080"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	_, err = svc.Update(t.Context(), profile.ID, EgressProfileInput{
		ID:   intPtrEgress(profile.ID + 1),
		Name: stringPtrEgress("mutated"),
		Type: stringPtrEgress("direct"),
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Update() error = %v, want ErrInvalidArgument", err)
	}

	got, err := svc.Get(t.Context(), profile.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.ID != profile.ID || got.Name != profile.Name || got.Type != profile.Type || got.ProxyURL != profile.ProxyURL {
		t.Fatalf("profile after rejected update = %+v, want unchanged %+v", got, profile)
	}
	profiles, err := svc.List(t.Context())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(profiles) != 1 || profiles[0].ID != profile.ID {
		t.Fatalf("profiles after rejected update = %+v, want only original profile", profiles)
	}
}

func TestEgressProfileServiceDeleteRejectsOrphanedAgentReferences(t *testing.T) {
	tests := []struct {
		name string
		seed func(t *testing.T, store *storage.SQLiteStore, profileID int)
		want string
	}{
		{
			name: "orphaned http rule",
			seed: func(t *testing.T, store *storage.SQLiteStore, profileID int) {
				t.Helper()
				if err := store.SaveHTTPRules(t.Context(), "orphan-agent", []storage.HTTPRuleRow{{
					ID:              30,
					AgentID:         "orphan-agent",
					FrontendURL:     "http://orphan.example.com",
					BackendsJSON:    `[{"url":"http://127.0.0.1:8096"}]`,
					EgressProfileID: &profileID,
					Enabled:         true,
					Revision:        1,
				}}); err != nil {
					t.Fatalf("SaveHTTPRules() error = %v", err)
				}
			},
			want: "HTTP rule 30",
		},
		{
			name: "orphaned l4 rule",
			seed: func(t *testing.T, store *storage.SQLiteStore, profileID int) {
				t.Helper()
				if err := store.SaveL4Rules(t.Context(), "orphan-agent", []storage.L4RuleRow{{
					ID:              31,
					AgentID:         "orphan-agent",
					Name:            "orphan l4",
					Protocol:        "tcp",
					ListenHost:      "0.0.0.0",
					ListenPort:      9443,
					BackendsJSON:    `[{"host":"127.0.0.1","port":443}]`,
					EgressProfileID: &profileID,
					Enabled:         true,
					Revision:        1,
				}}); err != nil {
					t.Fatalf("SaveL4Rules() error = %v", err)
				}
			},
			want: "l4 rule 31",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newEgressProfileTestStore(t)
			svc := NewEgressProfileService(store)
			profile := createTestEgressProfile(t, svc)
			tc.seed(t, store, profile.ID)

			_, err := svc.Delete(t.Context(), profile.ID)
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("Delete() error = %v, want ErrInvalidArgument", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Delete() error = %v, want reference %q", err, tc.want)
			}
		})
	}
}

func TestEgressProfileServiceListAndGetRedactSecrets(t *testing.T) {
	store := newEgressProfileTestStore(t)
	svc := NewEgressProfileService(store)

	proxyProfile, err := svc.Create(t.Context(), EgressProfileInput{
		Name:     stringPtrEgress("office socks"),
		Type:     stringPtrEgress("socks"),
		ProxyURL: stringPtrEgress("socks5://user:secret@127.0.0.1:1080"),
	})
	if err != nil {
		t.Fatalf("Create(proxy) error = %v", err)
	}
	wireGuardProfile, err := svc.Create(t.Context(), EgressProfileInput{
		Name:            stringPtrEgress("wg"),
		Type:            stringPtrEgress("wireguard"),
		WireGuardConfig: testEgressWireGuardConfig(),
	})
	if err != nil {
		t.Fatalf("Create(wireguard) error = %v", err)
	}

	profiles, err := svc.List(t.Context())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(profiles) != 2 {
		t.Fatalf("List() count = %d, want 2", len(profiles))
	}
	if profiles[0].ProxyURL != "socks5://user:xxxxx@127.0.0.1:1080" {
		t.Fatalf("List()[0].ProxyURL = %q, want redacted password", profiles[0].ProxyURL)
	}
	assertRedactedEgressWireGuardConfig(t, profiles[1].WireGuardConfig)

	gotProxyProfile, err := svc.Get(t.Context(), proxyProfile.ID)
	if err != nil {
		t.Fatalf("Get(proxy) error = %v", err)
	}
	if gotProxyProfile.ProxyURL != "socks5://user:xxxxx@127.0.0.1:1080" {
		t.Fatalf("Get(proxy).ProxyURL = %q, want redacted password", gotProxyProfile.ProxyURL)
	}
	gotWireGuardProfile, err := svc.Get(t.Context(), wireGuardProfile.ID)
	if err != nil {
		t.Fatalf("Get(wireguard) error = %v", err)
	}
	assertRedactedEgressWireGuardConfig(t, gotWireGuardProfile.WireGuardConfig)
}

func TestEgressProfileServiceUpdatePreservesSecretsOnRedactedInput(t *testing.T) {
	store := newEgressProfileTestStore(t)
	svc := NewEgressProfileService(store)
	profile, err := svc.Create(t.Context(), EgressProfileInput{
		Name:            stringPtrEgress("wg"),
		Type:            stringPtrEgress("wireguard"),
		WireGuardConfig: testEgressWireGuardConfig(),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	updated, err := svc.Update(t.Context(), profile.ID, EgressProfileInput{
		Description: stringPtrEgress("updated"),
		WireGuardConfig: &EgressWireGuardConfig{
			PrivateKey: "xxxxx",
			Addresses:  []string{"10.0.0.2/32"},
			Peers: []WireGuardPeer{{
				Name:         "peer",
				PublicKey:    testEgressWireGuardPeerPublicKey,
				PresharedKey: "xxxxx",
				Endpoint:     "vpn.example.com:51820",
				AllowedIPs:   []string{"0.0.0.0/0"},
			}},
		},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Description != "updated" {
		t.Fatalf("Description = %q, want updated", updated.Description)
	}
	assertRedactedEgressWireGuardConfig(t, updated.WireGuardConfig)

	rows, err := store.ListEgressProfiles(t.Context())
	if err != nil {
		t.Fatalf("ListEgressProfiles() error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("row count = %d, want 1", len(rows))
	}
	if strings.Contains(rows[0].WireGuardConfigJSON, "xxxxx") {
		t.Fatalf("stored WireGuard config contains redaction token: %s", rows[0].WireGuardConfigJSON)
	}
	if !strings.Contains(rows[0].WireGuardConfigJSON, testEgressWireGuardPrivateKey) {
		t.Fatalf("stored WireGuard config did not preserve private key: %s", rows[0].WireGuardConfigJSON)
	}
	if !strings.Contains(rows[0].WireGuardConfigJSON, testEgressWireGuardPresharedKey) {
		t.Fatalf("stored WireGuard config did not preserve preshared key: %s", rows[0].WireGuardConfigJSON)
	}
}

func TestEgressProfileServiceUpdatePreservesProxyPasswordOnRedactedInput(t *testing.T) {
	store := newEgressProfileTestStore(t)
	svc := NewEgressProfileService(store)
	profile, err := svc.Create(t.Context(), EgressProfileInput{
		Name:     stringPtrEgress("office socks"),
		Type:     stringPtrEgress("socks"),
		ProxyURL: stringPtrEgress("socks5://user:secret@127.0.0.1:1080"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	updated, err := svc.Update(t.Context(), profile.ID, EgressProfileInput{
		ProxyURL: stringPtrEgress(profile.ProxyURL),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.ProxyURL != profile.ProxyURL {
		t.Fatalf("ProxyURL = %q, want %q", updated.ProxyURL, profile.ProxyURL)
	}

	rows, err := store.ListEgressProfiles(t.Context())
	if err != nil {
		t.Fatalf("ListEgressProfiles() error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("row count = %d, want 1", len(rows))
	}
	if rows[0].ProxyURL != "socks5://user:secret@127.0.0.1:1080" {
		t.Fatalf("stored ProxyURL = %q, want raw secret preserved", rows[0].ProxyURL)
	}
}

func newEgressProfileTestStore(t *testing.T) *storage.SQLiteStore {
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
	return store
}

func createTestEgressProfile(t *testing.T, svc *egressProfileService) EgressProfile {
	t.Helper()
	profile, err := svc.Create(t.Context(), EgressProfileInput{
		Name:     stringPtrEgress("office socks"),
		Type:     stringPtrEgress("socks"),
		ProxyURL: stringPtrEgress("socks5://127.0.0.1:1080"),
		Enabled:  boolPtrEgress(true),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return profile
}

func testEgressWireGuardConfig() *EgressWireGuardConfig {
	return &EgressWireGuardConfig{
		PrivateKey: testEgressWireGuardPrivateKey,
		Addresses:  []string{"10.0.0.2/32"},
		Peers: []WireGuardPeer{{
			Name:         "peer",
			PublicKey:    testEgressWireGuardPeerPublicKey,
			PresharedKey: testEgressWireGuardPresharedKey,
			Endpoint:     "vpn.example.com:51820",
			AllowedIPs:   []string{"0.0.0.0/0"},
		}},
		DNS: []string{"1.1.1.1"},
		MTU: 1280,
	}
}

func assertRedactedEgressWireGuardConfig(t *testing.T, config *EgressWireGuardConfig) {
	t.Helper()
	if config == nil {
		t.Fatalf("WireGuardConfig is nil")
	}
	if config.PrivateKey != "xxxxx" {
		t.Fatalf("PrivateKey = %q, want redacted", config.PrivateKey)
	}
	if len(config.Peers) != 1 {
		t.Fatalf("peer count = %d, want 1", len(config.Peers))
	}
	if config.Peers[0].PresharedKey != "xxxxx" {
		t.Fatalf("PresharedKey = %q, want redacted", config.Peers[0].PresharedKey)
	}
}

func stringPtrEgress(value string) *string {
	return &value
}

func boolPtrEgress(value bool) *bool {
	return &value
}

func intPtrEgress(value int) *int {
	return &value
}

const (
	testEgressWireGuardPrivateKey    = "yAnzJsdbLTM3g2E5tbvhXfqz1aOBsKSOCWDJvuYEH2M="
	testEgressWireGuardPeerPublicKey = "ZiHvSwADcEppH6wKlffryv7ApEPcl+Kf0/x4AMY0iUw="
	testEgressWireGuardPresharedKey  = "WkE3qkRM7VCG59azvTz3WntYWK2Uhv1YVXBvXWP7t3I="
)
