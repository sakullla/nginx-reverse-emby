package relay

import (
	"fmt"
	"net"
	"strings"
)

const (
	tlsModePinOnly  = "pin_only"
	tlsModeCAOnly   = "ca_only"
	tlsModePinOrCA  = "pin_or_ca"
	tlsModePinAndCA = "pin_and_ca"
)

func ValidateListener(listener Listener) error {
	normalized, err := normalizeListener(listener)
	if err != nil {
		return err
	}

	if normalized.ListenHost == "" {
		return fmt.Errorf("listen_host is required")
	}
	for _, bindHost := range normalized.BindHosts {
		if !isValidListenHost(bindHost) {
			return fmt.Errorf("listen_host must be a valid IP address or hostname")
		}
	}
	if normalized.PublicHost != "" && !isValidListenHost(normalized.PublicHost) {
		return fmt.Errorf("public_host must be a valid IP address or hostname")
	}
	if normalized.ListenPort < 1 || normalized.ListenPort > 65535 {
		return fmt.Errorf("listen_port must be between 1 and 65535")
	}
	if normalized.PublicPort < 1 || normalized.PublicPort > 65535 {
		return fmt.Errorf("public_port must be between 1 and 65535")
	}

	mode, err := normalizeTLSMode(normalized.TLSMode)
	if err != nil {
		return err
	}
	for _, pin := range normalized.PinSet {
		if !isSupportedPinType(pin.Type) {
			return fmt.Errorf("unsupported pin type %q", pin.Type)
		}
		if strings.TrimSpace(pin.Value) == "" {
			return fmt.Errorf("pin_set.value is required")
		}
	}
	switch mode {
	case tlsModePinOnly:
		if len(normalized.PinSet) == 0 {
			return fmt.Errorf("pin_only requires pin_set")
		}
	case tlsModeCAOnly:
		if len(normalized.TrustedCACertificateIDs) == 0 {
			return fmt.Errorf("ca_only requires trusted_ca_certificate_ids")
		}
	case tlsModePinOrCA:
		if len(normalized.PinSet) == 0 && len(normalized.TrustedCACertificateIDs) == 0 {
			return fmt.Errorf("pin_set and trusted_ca_certificate_ids cannot both be empty")
		}
	case tlsModePinAndCA:
		if len(normalized.PinSet) == 0 || len(normalized.TrustedCACertificateIDs) == 0 {
			return fmt.Errorf("pin_and_ca requires pin_set and trusted_ca_certificate_ids")
		}
	}
	return nil
}

func normalizeListener(listener Listener) (Listener, error) {
	normalized := listener

	bindHosts := make([]string, 0, len(listener.BindHosts))
	seen := make(map[string]struct{}, len(listener.BindHosts))
	for _, rawHost := range listener.BindHosts {
		host := strings.TrimSpace(rawHost)
		if host == "" {
			continue
		}
		if _, ok := seen[host]; ok {
			continue
		}
		seen[host] = struct{}{}
		bindHosts = append(bindHosts, host)
	}

	listenHost := strings.TrimSpace(listener.ListenHost)
	if len(bindHosts) == 0 && listenHost != "" {
		bindHosts = append(bindHosts, listenHost)
	}
	if len(bindHosts) == 0 {
		return Listener{}, fmt.Errorf("listen_host is required")
	}

	normalized.BindHosts = bindHosts
	normalized.ListenHost = bindHosts[0]

	publicHost := strings.TrimSpace(listener.PublicHost)
	if publicHost == "" {
		publicHost = normalized.ListenHost
	}
	normalized.PublicHost = publicHost
	if normalized.PublicPort <= 0 {
		normalized.PublicPort = normalized.ListenPort
	}

	transportMode, err := normalizeListenerTransportMode(listener.TransportMode)
	if err != nil {
		return Listener{}, err
	}
	normalized.TransportMode = transportMode

	obfsMode, err := normalizeListenerObfsMode(listener.ObfsMode)
	if err != nil {
		return Listener{}, err
	}
	normalized.ObfsMode = obfsMode

	return normalized, nil
}

func normalizeListenerTransportMode(mode string) (string, error) {
	normalized := normalizeListenerTransportModeValue(mode)
	switch normalized {
	case ListenerTransportModeTLSTCP, ListenerTransportModeQUIC:
		return normalized, nil
	default:
		return "", fmt.Errorf("unsupported transport_mode %q", strings.TrimSpace(mode))
	}
}

func normalizeListenerTransportModeValue(mode string) string {
	normalized := strings.ToLower(strings.TrimSpace(mode))
	if normalized == "" {
		return ListenerTransportModeTLSTCP
	}
	return normalized
}

func normalizeListenerObfsMode(mode string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(mode))
	if normalized == "" {
		return RelayObfsModeOff, nil
	}
	switch normalized {
	case RelayObfsModeOff, RelayObfsModeEarlyWindowV2:
		return normalized, nil
	default:
		return "", fmt.Errorf("unsupported obfs_mode %q", strings.TrimSpace(mode))
	}
}

func normalizeTLSMode(mode string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case tlsModePinOnly:
		return tlsModePinOnly, nil
	case tlsModeCAOnly:
		return tlsModeCAOnly, nil
	case tlsModePinOrCA:
		return tlsModePinOrCA, nil
	case tlsModePinAndCA:
		return tlsModePinAndCA, nil
	default:
		return "", fmt.Errorf("unsupported tls_mode")
	}
}

func isSupportedPinType(pinType string) bool {
	switch strings.ToLower(strings.TrimSpace(pinType)) {
	case "spki_sha256", "sha256":
		return true
	default:
		return false
	}
}

func isValidListenHost(host string) bool {
	if strings.Contains(host, " ") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return true
	}
	if strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") {
		return false
	}
	if len(host) > 253 {
		return false
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 {
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
