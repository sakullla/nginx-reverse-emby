package certs

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/acmeflow"
)

func TestACMEFlowIssuerMapsShortLivedIPRequest(t *testing.T) {
	t.Parallel()

	account := acmeflow.AccountMetadata{
		Version:      acmeflow.AccountMetadataVersion,
		DirectoryURL: "https://ca.example/directory",
		Email:        "ops@example.com",
		URI:          "https://ca.example/account/1",
		Contact:      []string{"mailto:ops@example.com"},
	}
	engine := &recordingACMEFlowEngine{result: acmeflow.IssueResult{
		CertificatePEM: []byte("certificate"),
		PrivateKeyPEM:  []byte("certificate-key"),
		AccountKeyPEM:  []byte("account-key"),
		Account:        account,
	}}
	solver := staticChallengeSolver{challengeType: acmeflow.ChallengeHTTP01}
	issuer := acmeflowACMEIssuer{
		engine: engine,
		solverFactory: func(acmeIssueRequest) (acmeflow.ChallengeSolver, error) {
			return solver, nil
		},
	}
	store := &recordingAccountStore{}

	result, err := issuer.Issue(context.Background(), acmeIssueRequest{
		Domain:         "192.0.2.14",
		Scope:          "ip",
		Profile:        "shortlived",
		ChallengeType:  challengeTypeHTTP01,
		DirectoryURL:   "https://ca.example/directory",
		Email:          "ops@example.com",
		ExistingKeyPEM: []byte("existing-key"),
		AccountStore:   store,
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if len(engine.requests) != 1 {
		t.Fatalf("engine requests = %d, want 1", len(engine.requests))
	}
	request := engine.requests[0]
	if got, want := request.Identifiers, []acmeflow.Identifier{{Type: acmeflow.IdentifierIP, Value: "192.0.2.14"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("identifiers = %#v, want %#v", got, want)
	}
	if request.Profile != "shortlived" || request.ChallengeType != acmeflow.ChallengeHTTP01 {
		t.Fatalf("profile/challenge = %q/%q", request.Profile, request.ChallengeType)
	}
	if string(request.ExistingKeyPEM) != "existing-key" || request.AccountStore != store || request.Solver != solver {
		t.Fatalf("issuer request mapping = %#v", request)
	}
	if string(result.CertPEM) != "certificate" || string(result.KeyPEM) != "certificate-key" || string(result.AccountKeyPEM) != "account-key" {
		t.Fatalf("Issue() result = %#v", result)
	}
	if !reflect.DeepEqual(result.Account, account) {
		t.Fatalf("account metadata = %#v, want %#v", result.Account, account)
	}
}

func TestLegacyRegistrationMetadataIsNeutralAndCorruptionIsIgnored(t *testing.T) {
	t.Parallel()

	lookup := acmeflow.AccountLookup{DirectoryURL: "https://ca.example/directory", Email: "ops@example.com"}
	payload := []byte(`{"uri":"https://ca.example/account/42","body":{"contact":["mailto:ops@example.com"]}}`)
	metadata, ok := metadataFromLegacyRegistration(payload, lookup)
	if !ok {
		t.Fatal("metadataFromLegacyRegistration() did not migrate valid legacy state")
	}
	want := acmeflow.AccountMetadata{
		Version:      acmeflow.AccountMetadataVersion,
		DirectoryURL: lookup.DirectoryURL,
		Email:        lookup.Email,
		URI:          "https://ca.example/account/42",
		Contact:      []string{"mailto:ops@example.com"},
	}
	if !reflect.DeepEqual(metadata, want) {
		t.Fatalf("metadata = %#v, want %#v", metadata, want)
	}

	secretCanary := "legacy-registration-secret-canary"
	if metadata, ok := metadataFromLegacyRegistration([]byte(`{"uri":"`+secretCanary), lookup); ok || !reflect.DeepEqual(metadata, acmeflow.AccountMetadata{}) {
		t.Fatalf("corrupt legacy metadata = %#v, ok=%v", metadata, ok)
	}
}

type recordingACMEFlowEngine struct {
	requests []acmeflow.IssueRequest
	result   acmeflow.IssueResult
	err      error
}

func (e *recordingACMEFlowEngine) Issue(_ context.Context, request acmeflow.IssueRequest) (acmeflow.IssueResult, error) {
	e.requests = append(e.requests, request)
	return e.result, e.err
}

type staticChallengeSolver struct {
	challengeType string
}

func (s staticChallengeSolver) ChallengeType() string                           { return s.challengeType }
func (staticChallengeSolver) Present(context.Context, acmeflow.Challenge) error { return nil }
func (staticChallengeSolver) Wait(context.Context, acmeflow.Challenge) error    { return nil }
func (staticChallengeSolver) Cleanup(context.Context, acmeflow.Challenge) error { return nil }

type recordingAccountStore struct{}

func (*recordingAccountStore) LoadAccount(context.Context, acmeflow.AccountLookup) (acmeflow.AccountRecord, error) {
	return acmeflow.AccountRecord{}, acmeflow.ErrAccountNotFound
}
func (*recordingAccountStore) SaveAccountKey(context.Context, acmeflow.AccountLookup, []byte) error {
	return nil
}
func (*recordingAccountStore) SaveAccountMetadata(context.Context, acmeflow.AccountMetadata) error {
	return nil
}

func TestACMEFlowIssuerPropagatesCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (acmeflowACMEIssuer{}).Issue(ctx, acmeIssueRequest{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Issue() error = %v, want context cancellation", err)
	}
}
