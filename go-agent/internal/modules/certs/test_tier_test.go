package certs

import "testing"

func requireCertificateLifecycle(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("certificate issuance and persistence scenarios run in the full test tier")
	}
}
