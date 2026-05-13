package wireguard

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"unicode"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

const (
	ModeGenericWireGuard = "generic_wireguard"

	defaultMTU     = 1420
	redactedSecret = "xxxxx"
)

type Config struct {
	model.WireGuardProfile

	PrivateKeyBytes []byte
	AddressPrefixes []netip.Prefix
	AddressAddrs    []netip.Addr
	DNSAddrs        []netip.Addr
	Peers           []PeerConfig
}

type PeerConfig struct {
	model.WireGuardPeer

	PublicKeyBytes    []byte
	PresharedKeyBytes []byte
	AllowedPrefixes   []netip.Prefix
	EndpointHost      string
	EndpointPort      uint16
}

func NormalizeConfig(profile model.WireGuardProfile) (Config, error) {
	if !profile.Enabled {
		return Config{}, fmt.Errorf("profile is disabled")
	}

	mode := strings.ToLower(strings.TrimSpace(profile.Mode))
	if mode == "" {
		mode = ModeGenericWireGuard
	}
	if mode != ModeGenericWireGuard {
		return Config{}, fmt.Errorf("mode must be %s", ModeGenericWireGuard)
	}
	profile.Mode = mode

	privateKey, err := decodeWireGuardKey("private_key", profile.PrivateKey, true)
	if err != nil {
		return Config{}, err
	}

	if profile.ListenPort < 0 || profile.ListenPort > 65535 {
		return Config{}, fmt.Errorf("listen_port must be between 0 and 65535")
	}
	if profile.MTU < 0 {
		return Config{}, fmt.Errorf("mtu must be non-negative")
	}
	if profile.MTU == 0 {
		profile.MTU = defaultMTU
	}

	addresses, addressAddrs, err := parseAddressPrefixes(profile.Addresses)
	if err != nil {
		return Config{}, err
	}
	dnsAddrs, err := parseDNSAddrs(profile.DNS)
	if err != nil {
		return Config{}, err
	}
	peers, err := normalizePeers(profile.Peers)
	if err != nil {
		return Config{}, err
	}

	return Config{
		WireGuardProfile: profile,
		PrivateKeyBytes:  privateKey,
		AddressPrefixes:  addresses,
		AddressAddrs:     addressAddrs,
		DNSAddrs:         dnsAddrs,
		Peers:            peers,
	}, nil
}

func Fingerprint(profile model.WireGuardProfile) (string, error) {
	cfg, err := NormalizeConfig(profile)
	if err != nil {
		return "", err
	}

	type fingerprintPeer struct {
		Name                       string   `json:"name"`
		PublicKey                  string   `json:"public_key"`
		PresharedKey               string   `json:"preshared_key,omitempty"`
		Endpoint                   string   `json:"endpoint,omitempty"`
		AllowedIPs                 []string `json:"allowed_ips"`
		PersistentKeepaliveSeconds int      `json:"persistent_keepalive_seconds,omitempty"`
	}
	type fingerprintConfig struct {
		Mode       string            `json:"mode"`
		PrivateKey string            `json:"private_key"`
		ListenPort int               `json:"listen_port"`
		Addresses  []string          `json:"addresses"`
		DNS        []string          `json:"dns"`
		MTU        int               `json:"mtu"`
		Peers      []fingerprintPeer `json:"peers"`
	}

	value := fingerprintConfig{
		Mode:       cfg.Mode,
		PrivateKey: base64.StdEncoding.EncodeToString(cfg.PrivateKeyBytes),
		ListenPort: cfg.ListenPort,
		Addresses:  prefixStrings(cfg.AddressPrefixes),
		DNS:        addrStrings(cfg.DNSAddrs),
		MTU:        cfg.MTU,
		Peers:      make([]fingerprintPeer, 0, len(cfg.Peers)),
	}
	for _, peer := range cfg.Peers {
		value.Peers = append(value.Peers, fingerprintPeer{
			Name:                       strings.TrimSpace(peer.Name),
			PublicKey:                  base64.StdEncoding.EncodeToString(peer.PublicKeyBytes),
			PresharedKey:               base64.StdEncoding.EncodeToString(peer.PresharedKeyBytes),
			Endpoint:                   strings.TrimSpace(peer.Endpoint),
			AllowedIPs:                 prefixStrings(peer.AllowedPrefixes),
			PersistentKeepaliveSeconds: peer.PersistentKeepaliveSeconds,
		})
	}

	payload, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func decodeWireGuardKey(field, raw string, required bool) ([]byte, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		if required {
			return nil, fmt.Errorf("%s is required", field)
		}
		return nil, nil
	}
	if isRedactedSecret(value) {
		return nil, fmt.Errorf("%s is redacted; runtime config requires the real secret", field)
	}
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return nil, fmt.Errorf("%s must be base64-encoded 32 bytes", field)
	}
	return decoded, nil
}

func isRedactedSecret(value string) bool {
	value = strings.TrimSpace(value)
	return value == redactedSecret || strings.EqualFold(value, "redacted") || strings.EqualFold(value, "<redacted>")
}

func parseAddressPrefixes(values []string) ([]netip.Prefix, []netip.Addr, error) {
	prefixes := make([]netip.Prefix, 0, len(values))
	addrs := make([]netip.Addr, 0, len(values))
	for i, value := range values {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(value))
		if err != nil {
			return nil, nil, fmt.Errorf("addresses[%d] must be CIDR: %w", i, err)
		}
		prefixes = append(prefixes, prefix)
		addrs = append(addrs, prefix.Addr())
	}
	return prefixes, addrs, nil
}

func parseDNSAddrs(values []string) ([]netip.Addr, error) {
	addrs := make([]netip.Addr, 0, len(values))
	for i, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		addr, err := netip.ParseAddr(trimmed)
		if err != nil {
			return nil, fmt.Errorf("dns[%d] must be an IP address: %w", i, err)
		}
		addrs = append(addrs, addr)
	}
	return addrs, nil
}

func normalizePeers(peers []model.WireGuardPeer) ([]PeerConfig, error) {
	out := make([]PeerConfig, 0, len(peers))
	for i, peer := range peers {
		publicKey, err := decodeWireGuardKey(fmt.Sprintf("peers[%d].public_key", i), peer.PublicKey, true)
		if err != nil {
			return nil, err
		}
		presharedKey, err := decodeWireGuardKey(fmt.Sprintf("peers[%d].preshared_key", i), peer.PresharedKey, false)
		if err != nil {
			return nil, err
		}
		allowed, err := parsePeerAllowedPrefixes(i, peer.AllowedIPs)
		if err != nil {
			return nil, err
		}
		host, port, err := parseEndpoint(i, peer.Endpoint)
		if err != nil {
			return nil, err
		}
		if host != "" {
			peer.Endpoint = net.JoinHostPort(host, strconv.Itoa(int(port)))
		}
		if peer.PersistentKeepaliveSeconds < 0 {
			return nil, fmt.Errorf("peers[%d].persistent_keepalive_seconds must be non-negative", i)
		}
		out = append(out, PeerConfig{
			WireGuardPeer:     peer,
			PublicKeyBytes:    publicKey,
			PresharedKeyBytes: presharedKey,
			AllowedPrefixes:   allowed,
			EndpointHost:      host,
			EndpointPort:      port,
		})
	}
	return out, nil
}

func parsePeerAllowedPrefixes(peerIndex int, values []string) ([]netip.Prefix, error) {
	prefixes := make([]netip.Prefix, 0, len(values))
	for i, value := range values {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(value))
		if err != nil {
			return nil, fmt.Errorf("peers[%d].allowed_ips[%d] must be CIDR: %w", peerIndex, i, err)
		}
		prefixes = append(prefixes, prefix)
	}
	return prefixes, nil
}

func parseEndpoint(peerIndex int, endpoint string) (string, uint16, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return "", 0, nil
	}
	host, portValue, err := net.SplitHostPort(endpoint)
	if err != nil {
		return "", 0, fmt.Errorf("peers[%d].endpoint must be host:port", peerIndex)
	}
	host = strings.TrimSpace(host)
	portValue = strings.TrimSpace(portValue)
	if host == "" || portValue == "" {
		return "", 0, fmt.Errorf("peers[%d].endpoint must include host and port", peerIndex)
	}
	if !isValidEndpointHost(host) {
		return "", 0, fmt.Errorf("peers[%d].endpoint host must be a valid IP address or DNS name", peerIndex)
	}
	port, err := strconv.Atoi(portValue)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("peers[%d].endpoint port must be numeric and between 1 and 65535", peerIndex)
	}
	if addr, err := netip.ParseAddr(host); err == nil && addr.IsValid() {
		return addr.String(), uint16(port), nil
	}
	return strings.ToLower(host), uint16(port), nil
}

func isValidEndpointHost(host string) bool {
	if host == "" || host != strings.TrimSpace(host) || len(host) > 253 {
		return false
	}
	for _, r := range host {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	if addr, err := netip.ParseAddr(host); err == nil && addr.IsValid() {
		return true
	}
	return isValidDNSName(host)
}

func isValidDNSName(host string) bool {
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 {
			return false
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func prefixStrings(prefixes []netip.Prefix) []string {
	out := make([]string, 0, len(prefixes))
	for _, prefix := range prefixes {
		out = append(out, prefix.String())
	}
	return out
}

func addrStrings(addrs []netip.Addr) []string {
	out := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		out = append(out, addr.String())
	}
	return out
}
