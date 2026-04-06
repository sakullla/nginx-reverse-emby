package relay

import (
	"fmt"
	"strings"
)

const (
	tlsModePinOnly  = "pin_only"
	tlsModeCAOnly   = "ca_only"
	tlsModePinOrCA  = "pin_or_ca"
	tlsModePinAndCA = "pin_and_ca"
)

func ValidateListener(listener Listener) error {
	if strings.TrimSpace(listener.ListenHost) == "" {
		return fmt.Errorf("listen_host is required")
	}
	if listener.ListenPort < 1 || listener.ListenPort > 65535 {
		return fmt.Errorf("listen_port must be between 1 and 65535")
	}
	if _, err := normalizeTLSMode(listener.TLSMode); err != nil {
		return err
	}
	if len(listener.PinSet) == 0 && len(listener.TrustedCACertificateIDs) == 0 {
		return fmt.Errorf("pin_set and trusted_ca_certificate_ids cannot both be empty")
	}
	for _, pin := range listener.PinSet {
		if !isSupportedPinType(pin.Type) {
			return fmt.Errorf("unsupported pin type %q", pin.Type)
		}
		if strings.TrimSpace(pin.Value) == "" {
			return fmt.Errorf("pin_set.value is required")
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
