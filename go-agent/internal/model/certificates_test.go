package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestManagedCertificateACMEStateAcceptsLegacyRegistrationAndWritesNeutralMetadata(t *testing.T) {
	t.Parallel()

	var legacy ManagedCertificateACMEState
	if err := json.Unmarshal([]byte(`{"account":{"registration":{"uri":"https://ca.example/account/7"}}}`), &legacy); err != nil {
		t.Fatalf("legacy Unmarshal() error = %v", err)
	}
	if !strings.Contains(string(legacy.Account.Registration), "https://ca.example/account/7") {
		t.Fatalf("legacy registration = %s", legacy.Account.Registration)
	}

	state := ManagedCertificateACMEState{Account: ManagedCertificateACMEAccountState{
		Metadata: &ManagedCertificateACMEAccountMetadata{
			Version:      1,
			DirectoryURL: "https://ca.example/directory",
			Email:        "ops@example.com",
			URI:          "https://ca.example/account/7",
			Contact:      []string{"mailto:ops@example.com"},
		},
	}}
	payload, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	text := string(payload)
	if !strings.Contains(text, `"metadata"`) || strings.Contains(text, `"registration"`) {
		t.Fatalf("neutral account JSON = %s", text)
	}
}
