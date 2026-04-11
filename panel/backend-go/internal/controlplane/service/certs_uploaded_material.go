package service

import (
	"crypto/tls"
	"fmt"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func normalizeUploadedPEMField(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func joinUploadedCertificatePEM(certificatePEM string, caPEM string) string {
	base := strings.TrimSpace(certificatePEM)
	ca := strings.TrimSpace(caPEM)
	switch {
	case base == "" && ca == "":
		return ""
	case ca == "":
		return base
	case base == "":
		return ca
	default:
		return base + "\n" + ca
	}
}

func validateUploadedManagedCertificateBundle(bundle storage.ManagedCertificateBundle) error {
	certPEM := strings.TrimSpace(bundle.CertPEM)
	keyPEM := strings.TrimSpace(bundle.KeyPEM)
	if certPEM == "" {
		return fmt.Errorf("%w: certificate_pem is required for uploaded certificates", ErrInvalidArgument)
	}
	if keyPEM == "" {
		return fmt.Errorf("%w: private_key_pem is required for uploaded certificates", ErrInvalidArgument)
	}
	if _, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM)); err != nil {
		return fmt.Errorf("%w: invalid uploaded certificate material: %v", ErrInvalidArgument, err)
	}
	return nil
}
