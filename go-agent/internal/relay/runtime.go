package relay

import (
	"fmt"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type Listener = model.RelayListener

func ValidateListener(listener Listener) error {
	if len(listener.PinSet) == 0 && len(listener.TrustedCACertificateIDs) == 0 {
		return fmt.Errorf("pin_set or trusted_ca_certificate_ids is required")
	}
	return nil
}
