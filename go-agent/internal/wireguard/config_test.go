package wireguard

import (
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

const (
	testPrivateKey   = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	testPublicKeyA   = "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB="
	testPresharedKey = "CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC="
)

func TestNormalizeConfigAcceptsValidProfile(t *testing.T) {
	t.Parallel()

	cfg, err := NormalizeConfig(validProfile())
	if err != nil {
		t.Fatalf("NormalizeConfig() error = %v", err)
	}
	if cfg.Mode != ModeGenericWireGuard {
		t.Fatalf("Mode = %q, want %q", cfg.Mode, ModeGenericWireGuard)
	}
	if len(cfg.AddressAddrs) != 1 || cfg.AddressAddrs[0].String() != "10.20.0.1" {
		t.Fatalf("AddressAddrs = %+v", cfg.AddressAddrs)
	}
	if cfg.Peers[0].EndpointHost != "peer.example.com" || cfg.Peers[0].EndpointPort != 51820 {
		t.Fatalf("Endpoint = %q:%d", cfg.Peers[0].EndpointHost, cfg.Peers[0].EndpointPort)
	}
}

func TestNormalizeConfigDefaultsEmptyMode(t *testing.T) {
	t.Parallel()

	profile := validProfile()
	profile.Mode = ""
	cfg, err := NormalizeConfig(profile)
	if err != nil {
		t.Fatalf("NormalizeConfig() error = %v", err)
	}
	if cfg.Mode != ModeGenericWireGuard {
		t.Fatalf("Mode = %q, want %q", cfg.Mode, ModeGenericWireGuard)
	}
}

func TestNormalizeConfigRejectsInvalidKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*model.WireGuardProfile)
		wantErr string
	}{
		{
			name: "missing private key",
			mutate: func(profile *model.WireGuardProfile) {
				profile.PrivateKey = ""
			},
			wantErr: "private_key is required",
		},
		{
			name: "invalid private key",
			mutate: func(profile *model.WireGuardProfile) {
				profile.PrivateKey = "not-base64"
			},
			wantErr: "private_key must be base64-encoded 32 bytes",
		},
		{
			name: "missing public key",
			mutate: func(profile *model.WireGuardProfile) {
				profile.Peers[0].PublicKey = ""
			},
			wantErr: "peers[0].public_key is required",
		},
		{
			name: "invalid public key",
			mutate: func(profile *model.WireGuardProfile) {
				profile.Peers[0].PublicKey = "short"
			},
			wantErr: "peers[0].public_key must be base64-encoded 32 bytes",
		},
		{
			name: "invalid preshared key",
			mutate: func(profile *model.WireGuardProfile) {
				profile.Peers[0].PresharedKey = "short"
			},
			wantErr: "peers[0].preshared_key must be base64-encoded 32 bytes",
		},
		{
			name: "redacted private key",
			mutate: func(profile *model.WireGuardProfile) {
				profile.PrivateKey = "xxxxx"
			},
			wantErr: "private_key is redacted",
		},
		{
			name: "redacted public key",
			mutate: func(profile *model.WireGuardProfile) {
				profile.Peers[0].PublicKey = "xxxxx"
			},
			wantErr: "peers[0].public_key is redacted",
		},
		{
			name: "redacted preshared key",
			mutate: func(profile *model.WireGuardProfile) {
				profile.Peers[0].PresharedKey = "xxxxx"
			},
			wantErr: "peers[0].preshared_key is redacted",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			profile := validProfile()
			tc.mutate(&profile)
			_, err := NormalizeConfig(profile)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("NormalizeConfig() error = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestNormalizeConfigRejectsCIDRAndEndpointErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*model.WireGuardProfile)
		wantErr string
	}{
		{
			name: "empty addresses",
			mutate: func(profile *model.WireGuardProfile) {
				profile.Addresses = nil
			},
			wantErr: "addresses",
		},
		{
			name: "invalid address",
			mutate: func(profile *model.WireGuardProfile) {
				profile.Addresses = []string{"10.20.0.1"}
			},
			wantErr: "addresses[0] must be CIDR",
		},
		{
			name: "invalid allowed ip",
			mutate: func(profile *model.WireGuardProfile) {
				profile.Peers[0].AllowedIPs = []string{"10.20.0.2"}
			},
			wantErr: "peers[0].allowed_ips[0] must be CIDR",
		},
		{
			name: "missing endpoint port",
			mutate: func(profile *model.WireGuardProfile) {
				profile.Peers[0].Endpoint = "peer.example.com"
			},
			wantErr: "endpoint must be host:port",
		},
		{
			name: "bad endpoint host",
			mutate: func(profile *model.WireGuardProfile) {
				profile.Peers[0].Endpoint = "bad host:51820"
			},
			wantErr: "endpoint host must be a valid IP address or DNS name",
		},
		{
			name: "bad endpoint port",
			mutate: func(profile *model.WireGuardProfile) {
				profile.Peers[0].Endpoint = "peer.example.com:99999"
			},
			wantErr: "endpoint port must be numeric and between 1 and 65535",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			profile := validProfile()
			tc.mutate(&profile)
			_, err := NormalizeConfig(profile)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("NormalizeConfig() error = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestNormalizeConfigAllowsEmptyPeersForGeneratedClientBootstrap(t *testing.T) {
	t.Parallel()

	profile := validProfile()
	profile.Peers = nil

	cfg, err := NormalizeConfig(profile)
	if err != nil {
		t.Fatalf("NormalizeConfig() error = %v", err)
	}
	if len(cfg.Peers) != 0 {
		t.Fatalf("NormalizeConfig() peers = %+v, want empty bootstrap peer set", cfg.Peers)
	}
}

func TestNormalizeConfigRejectsDisabledAndUnsupportedMode(t *testing.T) {
	t.Parallel()

	profile := validProfile()
	profile.Enabled = false
	if _, err := NormalizeConfig(profile); err == nil || !strings.Contains(err.Error(), "profile is disabled") {
		t.Fatalf("NormalizeConfig(disabled) error = %v", err)
	}

	profile = validProfile()
	profile.Mode = "kernel"
	if _, err := NormalizeConfig(profile); err == nil || !strings.Contains(err.Error(), "mode must be generic_wireguard") {
		t.Fatalf("NormalizeConfig(mode) error = %v", err)
	}
}

func TestFingerprintChangesWhenEndpointChanges(t *testing.T) {
	t.Parallel()

	first, err := Fingerprint(validProfile())
	if err != nil {
		t.Fatalf("Fingerprint(first) error = %v", err)
	}
	nextProfile := validProfile()
	nextProfile.Peers[0].Endpoint = "peer.example.com:51821"
	second, err := Fingerprint(nextProfile)
	if err != nil {
		t.Fatalf("Fingerprint(second) error = %v", err)
	}
	if first == second {
		t.Fatal("fingerprint did not change after endpoint change")
	}
}

func TestFingerprintIsStableForSameInput(t *testing.T) {
	t.Parallel()

	first, err := Fingerprint(validProfile())
	if err != nil {
		t.Fatalf("Fingerprint(first) error = %v", err)
	}
	second, err := Fingerprint(validProfile())
	if err != nil {
		t.Fatalf("Fingerprint(second) error = %v", err)
	}
	if first != second {
		t.Fatalf("fingerprint changed for same input: %q != %q", first, second)
	}
}

func validProfile() model.WireGuardProfile {
	return model.WireGuardProfile{
		ID:         7,
		AgentID:    "agent-a",
		Name:       "wg-a",
		Mode:       ModeGenericWireGuard,
		PrivateKey: testPrivateKey,
		ListenPort: 51820,
		Addresses:  []string{"10.20.0.1/24"},
		Peers: []model.WireGuardPeer{{
			Name:                       "peer-a",
			PublicKey:                  testPublicKeyA,
			PresharedKey:               testPresharedKey,
			Endpoint:                   "peer.example.com:51820",
			AllowedIPs:                 []string{"10.20.0.2/32"},
			PersistentKeepaliveSeconds: 25,
		}},
		DNS:      []string{"1.1.1.1"},
		MTU:      1420,
		Enabled:  true,
		Tags:     []string{"edge"},
		Revision: 9,
	}
}
