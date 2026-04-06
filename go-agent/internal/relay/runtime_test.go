package relay

import "testing"

func TestRejectsListenerWithoutTrustMaterial(t *testing.T) {
	err := ValidateListener(Listener{TLSMode: "pin_or_ca"})
	if err == nil {
		t.Fatal("expected listener without pin_set and trusted_ca_certificate_ids to fail")
	}
}
