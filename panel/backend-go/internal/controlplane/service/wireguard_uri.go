package service

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

type ParsedWireGuardURI struct {
	Name         string
	PrivateKey   string
	Endpoint     string
	PublicKey    string
	PresharedKey string
	Addresses    []string
	AllowedIPs   []string
	DNS          []string
	MTU          int
	Reserved     []byte
}

func ParseWireGuardURI(raw string) (ParsedWireGuardURI, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ParsedWireGuardURI{}, fmt.Errorf("%w: invalid wireguard URI", ErrInvalidArgument)
	}
	if !strings.EqualFold(parsed.Scheme, "wireguard") {
		return ParsedWireGuardURI{}, fmt.Errorf("%w: wireguard URI scheme must be wireguard", ErrInvalidArgument)
	}
	if parsed.User == nil || strings.TrimSpace(parsed.User.Username()) == "" {
		return ParsedWireGuardURI{}, fmt.Errorf("%w: wireguard URI private key is required", ErrInvalidArgument)
	}

	host := parsed.Hostname()
	portValue := parsed.Port()
	if strings.TrimSpace(host) == "" || strings.TrimSpace(portValue) == "" {
		return ParsedWireGuardURI{}, fmt.Errorf("%w: wireguard URI endpoint host and port are required", ErrInvalidArgument)
	}
	endpoint := net.JoinHostPort(host, portValue)
	if err := validateWireGuardPeerEndpoint(endpoint); err != nil {
		return ParsedWireGuardURI{}, err
	}

	query := parseWireGuardURIQuery(parsed.RawQuery)
	result := ParsedWireGuardURI{
		Name:         strings.TrimSpace(parsed.Fragment),
		PrivateKey:   strings.TrimSpace(parsed.User.Username()),
		Endpoint:     endpoint,
		PublicKey:    strings.TrimSpace(query["publickey"]),
		PresharedKey: firstNonEmptyTrimmed(query["preshared-key"], query["psk"]),
		Addresses:    splitWireGuardURIList(query["address"]),
		AllowedIPs:   splitWireGuardURIList(firstNonEmptyTrimmed(query["allowedips"], query["allowed-ips"])),
		DNS:          splitWireGuardURIList(query["dns"]),
	}
	if len(result.AllowedIPs) == 0 {
		result.AllowedIPs = []string{"0.0.0.0/0", "::/0"}
	}
	if err := validateWireGuardKey(result.PrivateKey, true); err != nil {
		return ParsedWireGuardURI{}, fmt.Errorf("%w: private key must be a WireGuard key", ErrInvalidArgument)
	}
	if err := validateWireGuardKey(result.PublicKey, true); err != nil {
		return ParsedWireGuardURI{}, fmt.Errorf("%w: publickey must be a WireGuard key", ErrInvalidArgument)
	}
	if err := validateWireGuardKey(result.PresharedKey, false); err != nil {
		return ParsedWireGuardURI{}, fmt.Errorf("%w: preshared-key must be a WireGuard key", ErrInvalidArgument)
	}
	if len(result.Addresses) == 0 {
		return ParsedWireGuardURI{}, fmt.Errorf("%w: address is required", ErrInvalidArgument)
	}
	if err := validateWireGuardPrefixes(result.Addresses, "address"); err != nil {
		return ParsedWireGuardURI{}, err
	}
	if err := validateWireGuardPrefixes(result.AllowedIPs, "allowedips"); err != nil {
		return ParsedWireGuardURI{}, err
	}
	if err := validateWireGuardDNSAddrs(result.DNS); err != nil {
		return ParsedWireGuardURI{}, err
	}

	if mtuRaw := strings.TrimSpace(query["mtu"]); mtuRaw != "" {
		mtu, err := strconv.Atoi(mtuRaw)
		if err != nil || mtu < 0 {
			return ParsedWireGuardURI{}, fmt.Errorf("%w: mtu must be non-negative", ErrInvalidArgument)
		}
		result.MTU = mtu
	}
	if reservedRaw := strings.TrimSpace(query["reserved"]); reservedRaw != "" {
		reserved, err := parseWireGuardURIReserved(reservedRaw)
		if err != nil {
			return ParsedWireGuardURI{}, err
		}
		result.Reserved = reserved
	}
	return result, nil
}

func RedactWireGuardURIPreview(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("%w: invalid wireguard URI", ErrInvalidArgument)
	}
	if _, err := ParseWireGuardURI(raw); err != nil {
		return "", err
	}
	parsed.User = url.User(redactedProxyPassword)
	query := parsed.Query()
	if strings.TrimSpace(query.Get("preshared-key")) != "" {
		query.Set("preshared-key", redactedProxyPassword)
	}
	if strings.TrimSpace(query.Get("psk")) != "" {
		query.Set("psk", redactedProxyPassword)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func RedactWireGuardURIPreviewOrRaw(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	preview, err := RedactWireGuardURIPreview(trimmed)
	if err != nil {
		return trimmed
	}
	return preview
}

func redactWireGuardURIPreviewOrRaw(raw string) string {
	return RedactWireGuardURIPreviewOrRaw(raw)
}

func parseWireGuardURIQuery(raw string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(raw, "&") {
		if part == "" {
			continue
		}
		key, value, _ := strings.Cut(part, "=")
		decodedKey, err := url.QueryUnescape(key)
		if err != nil {
			continue
		}
		decodedValue, err := url.PathUnescape(value)
		if err != nil {
			decodedValue = value
		}
		out[strings.ToLower(strings.TrimSpace(decodedKey))] = decodedValue
	}
	return out
}

func wireGuardURIValueHasUnsupportedReserved(parsed ParsedWireGuardURI) bool {
	return len(parsed.Reserved) > 0
}

func WireGuardProfileInputFromURI(parsed ParsedWireGuardURI, name string) (WireGuardProfileInput, error) {
	if wireGuardURIValueHasUnsupportedReserved(parsed) {
		return WireGuardProfileInput{}, fmt.Errorf("%w: wireguard URI reserved is not supported for imported profiles yet", ErrInvalidArgument)
	}
	return wireGuardProfileInputFromURI(parsed, name), nil
}

func splitWireGuardURIList(raw string) []string {
	values := strings.Split(raw, ",")
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			normalized = append(normalized, trimmed)
		}
	}
	return normalized
}

func firstNonEmptyTrimmed(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func parseWireGuardURIReserved(raw string) ([]byte, error) {
	parts := splitWireGuardURIList(raw)
	if len(parts) < 1 || len(parts) > 3 {
		return nil, fmt.Errorf("%w: reserved must contain 1 to 3 bytes", ErrInvalidArgument)
	}
	reserved := make([]byte, 0, len(parts))
	for _, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 || value > 255 {
			return nil, fmt.Errorf("%w: reserved bytes must be between 0 and 255", ErrInvalidArgument)
		}
		reserved = append(reserved, byte(value))
	}
	return reserved, nil
}

func wireGuardProfileInputFromURI(parsed ParsedWireGuardURI, name string) WireGuardProfileInput {
	profileName := strings.TrimSpace(parsed.Name)
	if profileName == "" {
		profileName = strings.TrimSpace(name)
	}
	enabled := true
	return WireGuardProfileInput{
		Name:              profileName,
		Mode:              "generic_wireguard",
		PrivateKey:        parsed.PrivateKey,
		ListenPort:        0,
		ListenPortSet:     true,
		PublicEndpoint:    parsed.Endpoint,
		PublicEndpointSet: true,
		Addresses:         append([]string(nil), parsed.Addresses...),
		AddressesSet:      true,
		Peers: []WireGuardPeer{{
			PublicKey:    parsed.PublicKey,
			PresharedKey: parsed.PresharedKey,
			Endpoint:     parsed.Endpoint,
			AllowedIPs:   append([]string(nil), parsed.AllowedIPs...),
			Reserved:     append([]byte(nil), parsed.Reserved...),
		}},
		PeersSet: true,
		DNS:      append([]string(nil), parsed.DNS...),
		MTU:      parsed.MTU,
		Enabled:  &enabled,
	}
}
