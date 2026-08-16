//go:build integration

package acmeflow

import (
	"context"
	"crypto/rand"
	"testing"
	"time"

	"golang.org/x/crypto/acme"
)

type legacyProtocolClient struct{ ProtocolClient }

func containsTestEvent(events []string, target string) bool {
	for _, event := range events {
		if event == target {
			return true
		}
	}
	return false
}

func TestEnginePassesOriginalOrderURLToRecoveryFinalizer(t *testing.T) {
	now := time.Date(2026, 7, 26, 5, 0, 0, 0, time.UTC)
	events := []string{}
	client := newHappyProtocolClient(t, now, &events)
	var orderURL, finalizeURL string
	client.createCertForOrderFn = func(_ context.Context, gotOrderURL, gotFinalizeURL string, csr []byte, bundle bool) ([][]byte, string, error) {
		orderURL, finalizeURL = gotOrderURL, gotFinalizeURL
		return client.createCertFn(context.Background(), gotFinalizeURL, csr, bundle)
	}
	engine := testEngine(client, now, &events)
	_, err := engine.Issue(context.Background(), IssueRequest{
		DirectoryURL:  "https://ca.invalid/directory",
		Email:         "ops@example.com",
		Identifiers:   []Identifier{{Type: IdentifierDNS, Value: "example.com"}},
		ChallengeType: ChallengeHTTP01,
		Solver:        &fakeChallengeSolver{challengeType: ChallengeHTTP01, events: &events},
		AccountStore:  &fakeAccountStore{events: &events},
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if orderURL != "https://ca.invalid/order/1" || finalizeURL != "https://ca.invalid/order/1/finalize" {
		t.Fatalf("finalizer URLs = order %q finalize %q", orderURL, finalizeURL)
	}
}

func TestEngineKeepsLegacyProtocolClientFinalization(t *testing.T) {
	now := time.Date(2026, 7, 26, 5, 5, 0, 0, time.UTC)
	events := []string{}
	client := newHappyProtocolClient(t, now, &events)
	legacy := &legacyProtocolClient{ProtocolClient: client}
	engine := testEngine(client, now, &events)
	engine.ClientFactory = func(ClientConfig) ProtocolClient { return legacy }
	_, err := engine.Issue(context.Background(), IssueRequest{
		DirectoryURL:  "https://ca.invalid/directory",
		Email:         "ops@example.com",
		Identifiers:   []Identifier{{Type: IdentifierDNS, Value: "example.com"}},
		ChallengeType: ChallengeHTTP01,
		Solver:        &fakeChallengeSolver{challengeType: ChallengeHTTP01, events: &events},
		AccountStore:  &fakeAccountStore{events: &events},
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if !containsTestEvent(events, "create_order_cert") {
		t.Fatalf("legacy finalization events = %v", events)
	}
}

func TestEngineProfileIPOrderUsesInjectedProfileStarter(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	events := []string{}
	store := &fakeAccountStore{
		events: &events,
		record: AccountRecord{
			KeyPEM: mustRSAKeyPEM(t),
			Metadata: AccountMetadata{
				Version:      AccountMetadataVersion,
				DirectoryURL: "https://ca.invalid/directory",
				Email:        "ops@example.com",
				URI:          "https://ca.invalid/acct/1",
			},
		},
		found: true,
	}
	client := newHappyProtocolClient(t, now, &events)
	client.authz = &acme.Authorization{
		URI:        "https://ca.invalid/authz/1",
		Status:     acme.StatusPending,
		Identifier: acme.AuthzID{Type: "ip", Value: "192.0.2.40"},
		Challenges: []*acme.Challenge{{Type: ChallengeHTTP01, URI: "https://ca.invalid/challenge/1", Token: "token"}},
	}
	solver := &fakeChallengeSolver{challengeType: ChallengeHTTP01, events: &events}
	profileCalled := false
	engine := Engine{
		ClientFactory: func(config ClientConfig) ProtocolClient {
			client.key = config.Key
			return client
		},
		OrderStarter: OrderStarterFunc(func(_ context.Context, request OrderStartRequest) (*acme.Order, error) {
			profileCalled = true
			if request.Profile != "shortlived" || request.Identifiers[0].Type != IdentifierIP {
				t.Fatalf("profile order request = %#v", request)
			}
			return &acme.Order{
				URI:         "https://ca.invalid/order/1",
				Status:      acme.StatusPending,
				Identifiers: []acme.AuthzID{{Type: "ip", Value: "192.0.2.40"}},
				AuthzURLs:   []string{"https://ca.invalid/authz/1"},
				FinalizeURL: "https://ca.invalid/order/1/finalize",
			}, nil
		}),
		Now:  func() time.Time { return now },
		Rand: rand.Reader,
	}
	result, err := engine.Issue(context.Background(), IssueRequest{
		DirectoryURL:  "https://ca.invalid/directory",
		Email:         "ops@example.com",
		Identifiers:   []Identifier{{Type: IdentifierIP, Value: "192.0.2.40"}},
		Profile:       "shortlived",
		ChallengeType: ChallengeHTTP01,
		Solver:        solver,
		AccountStore:  store,
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if !profileCalled {
		t.Fatal("profile order starter was not called")
	}
	if result.Profile != "shortlived" {
		t.Fatalf("result profile = %q", result.Profile)
	}
	leaf, _ := firstCertificate(result.CertificatePEM)
	if leaf.Subject.CommonName != "" {
		t.Fatalf("IP certificate CommonName = %q, want empty", leaf.Subject.CommonName)
	}
	if len(leaf.IPAddresses) != 1 || leaf.IPAddresses[0].String() != "192.0.2.40" {
		t.Fatalf("IP certificate SANs = %v, want [192.0.2.40]", leaf.IPAddresses)
	}
}
