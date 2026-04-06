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
	listenHost := strings.TrimSpace(listener.ListenHost)
	if listenHost == "" {
		return fmt.Errorf("listen_host is required")
	}
	if !isValidListenHost(listenHost) {
		return fmt.Errorf("listen_host must be a valid IP address or hostname")
	}
	if listener.ListenPort < 1 || listener.ListenPort > 65535 {
		return fmt.Errorf("listen_port must be between 1 and 65535")
	}
	mode, err := normalizeTLSMode(listener.TLSMode)
	if err != nil {
		return err
	}
	for _, pin := range listener.PinSet {
		if !isSupportedPinType(pin.Type) {
			return fmt.Errorf("unsupported pin type %q", pin.Type)
		}
		if strings.TrimSpace(pin.Value) == "" {
			return fmt.Errorf("pin_set.value is required")
		}
	}
	switch mode {
	case tlsModePinOnly:
		if len(listener.PinSet) == 0 {
			return fmt.Errorf("pin_only requires pin_set")
		}
	case tlsModeCAOnly:
		if len(listener.TrustedCACertificateIDs) == 0 {
			return fmt.Errorf("ca_only requires trusted_ca_certificate_ids")
		}
	case tlsModePinOrCA:
		if len(listener.PinSet) == 0 && len(listener.TrustedCACertificateIDs) == 0 {
			return fmt.Errorf("pin_set and trusted_ca_certificate_ids cannot both be empty")
		}
	case tlsModePinAndCA:
		if len(listener.PinSet) == 0 || len(listener.TrustedCACertificateIDs) == 0 {
			return fmt.Errorf("pin_and_ca requires pin_set and trusted_ca_certificate_ids")
		}
	}
	return nil
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
