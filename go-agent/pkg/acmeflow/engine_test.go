package acmeflow

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"reflect"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/acme"
)

func TestEngineIssuesDomainReusesKeyAndCleansUp(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	existingKey := mustTestRSAKey(t)
	existingKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(existingKey)})

	events := []string{}
	store := &fakeAccountStore{events: &events}
	client := newHappyProtocolClient(t, now, &events)
	solver := &fakeChallengeSolver{challengeType: ChallengeHTTP01, events: &events}
	starter := OrderStarterFunc(func(_ context.Context, request OrderStartRequest) (*acme.Order, error) {
		events = append(events, "start_order")
		if request.Profile != "" {
			t.Fatalf("profile = %q, want empty", request.Profile)
		}
		return &acme.Order{
			URI:         "https://ca.invalid/order/1",
			Status:      acme.StatusPending,
			Identifiers: []acme.AuthzID{{Type: "dns", Value: "example.com"}},
			AuthzURLs:   []string{"https://ca.invalid/authz/1"},
			FinalizeURL: "https://ca.invalid/order/1/finalize",
		}, nil
	})

	engine := Engine{
		ClientFactory: func(config ClientConfig) ProtocolClient {
			events = append(events, "new_client")
			client.key = config.Key
			return client
		},
		OrderStarter:   starter,
		Rand:           rand.Reader,
		Now:            func() time.Time { return now },
		CleanupTimeout: time.Second,
	}
	result, err := engine.Issue(context.Background(), IssueRequest{
		DirectoryURL:   "https://ca.invalid/directory",
		Email:          "ops@example.com",
		Identifiers:    []Identifier{{Type: IdentifierDNS, Value: "example.com"}},
		ChallengeType:  ChallengeHTTP01,
		Solver:         solver,
		AccountStore:   store,
		ExistingKeyPEM: existingKeyPEM,
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if string(result.PrivateKeyPEM) != string(existingKeyPEM) {
		t.Fatal("Issue() did not preserve the existing certificate key PEM")
	}
	if result.Account.URI != "https://ca.invalid/acct/1" {
		t.Fatalf("account URI = %q", result.Account.URI)
	}
	if len(result.AccountKeyPEM) == 0 {
		t.Fatal("account key PEM is empty")
	}
	if solver.cleanupCalls != 1 {
		t.Fatalf("cleanup calls = %d, want 1", solver.cleanupCalls)
	}
	if want := []string{
		"load_account", "save_account_key", "new_client", "get_reg", "register", "save_account_metadata",
		"start_order", "get_authorization", "present", "wait_solver", "accept", "wait_authorization", "cleanup", "wait_order", "create_order_cert",
	}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events =\n%#v\nwant\n%#v", events, want)
	}
	if _, err := ValidateMaterial(CertificateMaterial{
		CertificatePEM: result.CertificatePEM,
		PrivateKeyPEM:  result.PrivateKeyPEM,
		Profile:        result.Profile,
	}, MaterialPolicy{
		Identifiers: []Identifier{{Type: IdentifierDNS, Value: "example.com"}},
		Now:         now,
	}); err != nil {
		t.Fatalf("result material invalid: %v", err)
	}
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
}

func TestEngineCancellationStillCleansUpOnce(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	events := []string{}
	store := &fakeAccountStore{events: &events}
	client := newHappyProtocolClient(t, now, &events)
	ctx, cancel := context.WithCancel(context.Background())
	solver := &fakeChallengeSolver{
		challengeType: ChallengeHTTP01,
		events:        &events,
		waitFn: func(context.Context, Challenge) error {
			cancel()
			return context.Canceled
		},
		cleanupFn: func(cleanupCtx context.Context, _ Challenge) error {
			if err := cleanupCtx.Err(); err != nil {
				t.Errorf("cleanup context inherited cancellation: %v", err)
			}
			return nil
		},
	}
	engine := testEngine(client, now, &events)
	_, err := engine.Issue(ctx, IssueRequest{
		DirectoryURL:  "https://ca.invalid/directory",
		Email:         "ops@example.com",
		Identifiers:   []Identifier{{Type: IdentifierDNS, Value: "example.com"}},
		ChallengeType: ChallengeHTTP01,
		Solver:        solver,
		AccountStore:  store,
	})
	if got := ErrorCategoryOf(err); got != CategoryCancelled {
		t.Fatalf("error category = %q, want %q (err=%v)", got, CategoryCancelled, err)
	}
	if solver.cleanupCalls != 1 {
		t.Fatalf("cleanup calls = %d, want 1", solver.cleanupCalls)
	}
}

func TestEnginePrimaryFailureWinsCleanupFailure(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	events := []string{}
	store := &fakeAccountStore{events: &events}
	client := newHappyProtocolClient(t, now, &events)
	solver := &fakeChallengeSolver{
		challengeType: ChallengeHTTP01,
		events:        &events,
		waitFn: func(context.Context, Challenge) error {
			return WrapError(CategoryAuthorization, "challenge_wait", errors.New("provider token canary"))
		},
		cleanupFn: func(context.Context, Challenge) error {
			return errors.New("cleanup token canary")
		},
	}
	engine := testEngine(client, now, &events)
	_, err := engine.Issue(context.Background(), IssueRequest{
		DirectoryURL:  "https://ca.invalid/directory",
		Email:         "ops@example.com",
		Identifiers:   []Identifier{{Type: IdentifierDNS, Value: "example.com"}},
		ChallengeType: ChallengeHTTP01,
		Solver:        solver,
		AccountStore:  store,
	})
	var safe *SafeError
	if !errors.As(err, &safe) {
		t.Fatalf("Issue() error type = %T, want *SafeError", err)
	}
	if safe.Category != CategoryAuthorization || !safe.CleanupFailed {
		t.Fatalf("safe error = %#v", safe)
	}
	if solver.cleanupCalls != 1 {
		t.Fatalf("cleanup calls = %d, want 1", solver.cleanupCalls)
	}
}

func TestEngineCleanupFailurePreventsFinalize(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	events := []string{}
	store := &fakeAccountStore{events: &events}
	client := newHappyProtocolClient(t, now, &events)
	solver := &fakeChallengeSolver{
		challengeType: ChallengeHTTP01,
		events:        &events,
		cleanupFn: func(context.Context, Challenge) error {
			return errors.New("cleanup failed")
		},
	}
	engine := testEngine(client, now, &events)
	_, err := engine.Issue(context.Background(), IssueRequest{
		DirectoryURL:  "https://ca.invalid/directory",
		Email:         "ops@example.com",
		Identifiers:   []Identifier{{Type: IdentifierDNS, Value: "example.com"}},
		ChallengeType: ChallengeHTTP01,
		Solver:        solver,
		AccountStore:  store,
	})
	if got := ErrorCategoryOf(err); got != CategoryCleanup {
		t.Fatalf("error category = %q, want %q (err=%v)", got, CategoryCleanup, err)
	}
	for _, event := range events {
		if event == "wait_order" || event == "create_order_cert" {
			t.Fatalf("engine finalized after cleanup failure; events = %#v", events)
		}
	}
}

func TestEngineRejectsAuthorizationOutsideOrder(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	events := []string{}
	store := &fakeAccountStore{events: &events}
	client := newHappyProtocolClient(t, now, &events)
	client.authz = &acme.Authorization{
		URI:        "https://ca.invalid/authz/1",
		Status:     acme.StatusPending,
		Identifier: acme.AuthzID{Type: "dns", Value: "unrelated.example"},
		Challenges: []*acme.Challenge{{Type: ChallengeHTTP01, URI: "https://ca.invalid/challenge/1", Token: "token"}},
	}
	solver := &fakeChallengeSolver{challengeType: ChallengeHTTP01, events: &events}
	engine := testEngine(client, now, &events)
	_, err := engine.Issue(context.Background(), IssueRequest{
		DirectoryURL:  "https://ca.invalid/directory",
		Email:         "ops@example.com",
		Identifiers:   []Identifier{{Type: IdentifierDNS, Value: "example.com"}},
		ChallengeType: ChallengeHTTP01,
		Solver:        solver,
		AccountStore:  store,
	})
	if got := ErrorCategoryOf(err); got != CategoryAuthorization {
		t.Fatalf("error category = %q, want %q (err=%v)", got, CategoryAuthorization, err)
	}
	if solver.cleanupCalls != 0 {
		t.Fatalf("cleanup calls = %d, want 0 before presentation", solver.cleanupCalls)
	}
	for _, event := range events {
		if event == "present" {
			t.Fatalf("solver presented an unrelated authorization; events = %#v", events)
		}
	}
}

func TestEngineNegativeCleanupTimeoutStillUsesBoundedContext(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	events := []string{}
	store := &fakeAccountStore{events: &events}
	client := newHappyProtocolClient(t, now, &events)
	deadlineObserved := false
	solver := &fakeChallengeSolver{
		challengeType: ChallengeHTTP01,
		events:        &events,
		cleanupFn: func(cleanupCtx context.Context, _ Challenge) error {
			deadline, ok := cleanupCtx.Deadline()
			if !ok {
				t.Error("cleanup context has no deadline")
				return nil
			}
			remaining := time.Until(deadline)
			if remaining <= 0 || remaining > 31*time.Second {
				t.Errorf("cleanup deadline remaining = %s, want bounded default near 30s", remaining)
			}
			deadlineObserved = true
			return nil
		},
	}
	engine := testEngine(client, now, &events)
	engine.CleanupTimeout = -time.Second
	_, err := engine.Issue(context.Background(), IssueRequest{
		DirectoryURL:  "https://ca.invalid/directory",
		Email:         "ops@example.com",
		Identifiers:   []Identifier{{Type: IdentifierDNS, Value: "example.com"}},
		ChallengeType: ChallengeHTTP01,
		Solver:        solver,
		AccountStore:  store,
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if !deadlineObserved {
		t.Fatal("cleanup did not observe a bounded deadline")
	}
}

func testEngine(client *fakeProtocolClient, now time.Time, events *[]string) Engine {
	return Engine{
		ClientFactory: func(config ClientConfig) ProtocolClient {
			client.key = config.Key
			return client
		},
		OrderStarter: OrderStarterFunc(func(context.Context, OrderStartRequest) (*acme.Order, error) {
			return &acme.Order{
				URI:         "https://ca.invalid/order/1",
				Status:      acme.StatusPending,
				Identifiers: []acme.AuthzID{{Type: "dns", Value: "example.com"}},
				AuthzURLs:   []string{"https://ca.invalid/authz/1"},
				FinalizeURL: "https://ca.invalid/order/1/finalize",
			}, nil
		}),
		Now:            func() time.Time { return now },
		Rand:           rand.Reader,
		CleanupTimeout: time.Second,
	}
}

type fakeAccountStore struct {
	record AccountRecord
	found  bool
	events *[]string
}

func (s *fakeAccountStore) LoadAccount(context.Context, AccountLookup) (AccountRecord, error) {
	if s.events != nil {
		*s.events = append(*s.events, "load_account")
	}
	if !s.found {
		return AccountRecord{}, ErrAccountNotFound
	}
	return cloneAccountRecord(s.record), nil
}

func (s *fakeAccountStore) SaveAccountKey(_ context.Context, lookup AccountLookup, keyPEM []byte) error {
	if s.events != nil {
		*s.events = append(*s.events, "save_account_key")
	}
	s.record.Metadata.DirectoryURL = lookup.DirectoryURL
	s.record.Metadata.Email = lookup.Email
	s.record.KeyPEM = append([]byte(nil), keyPEM...)
	s.found = true
	return nil
}

func (s *fakeAccountStore) SaveAccountMetadata(_ context.Context, metadata AccountMetadata) error {
	if s.events != nil {
		*s.events = append(*s.events, "save_account_metadata")
	}
	s.record.Metadata = cloneAccountMetadata(metadata)
	return nil
}

type fakeProtocolClient struct {
	key                  crypto.Signer
	accountURI           string
	authz                *acme.Authorization
	authorizeOrderFn     func(context.Context, []acme.AuthzID, ...acme.OrderOption) (*acme.Order, error)
	getRegFn             func(context.Context, string) (*acme.Account, error)
	registerFn           func(context.Context, *acme.Account, func(string) bool) (*acme.Account, error)
	getAuthzFn           func(context.Context, string) (*acme.Authorization, error)
	acceptFn             func(context.Context, *acme.Challenge) (*acme.Challenge, error)
	waitAuthzFn          func(context.Context, string) (*acme.Authorization, error)
	waitOrderFn          func(context.Context, string) (*acme.Order, error)
	createCertFn         func(context.Context, string, []byte, bool) ([][]byte, string, error)
	createCertForOrderFn func(context.Context, string, string, []byte, bool) ([][]byte, string, error)
}

func (c *fakeProtocolClient) SetAccountURI(uri string) { c.accountURI = uri }

func (c *fakeProtocolClient) GetReg(ctx context.Context, uri string) (*acme.Account, error) {
	return c.getRegFn(ctx, uri)
}

func (c *fakeProtocolClient) Register(ctx context.Context, account *acme.Account, prompt func(string) bool) (*acme.Account, error) {
	return c.registerFn(ctx, account, prompt)
}

func (c *fakeProtocolClient) AuthorizeOrder(ctx context.Context, ids []acme.AuthzID, opts ...acme.OrderOption) (*acme.Order, error) {
	return c.authorizeOrderFn(ctx, ids, opts...)
}

func (c *fakeProtocolClient) GetAuthorization(ctx context.Context, uri string) (*acme.Authorization, error) {
	return c.getAuthzFn(ctx, uri)
}

func (c *fakeProtocolClient) Accept(ctx context.Context, challenge *acme.Challenge) (*acme.Challenge, error) {
	return c.acceptFn(ctx, challenge)
}

func (c *fakeProtocolClient) WaitAuthorization(ctx context.Context, uri string) (*acme.Authorization, error) {
	return c.waitAuthzFn(ctx, uri)
}

func (c *fakeProtocolClient) WaitOrder(ctx context.Context, uri string) (*acme.Order, error) {
	return c.waitOrderFn(ctx, uri)
}

func (c *fakeProtocolClient) CreateOrderCert(ctx context.Context, uri string, csr []byte, bundle bool) ([][]byte, string, error) {
	return c.createCertFn(ctx, uri, csr, bundle)
}

func (c *fakeProtocolClient) CreateOrderCertForOrder(ctx context.Context, orderURL, finalizeURL string, csr []byte, bundle bool) ([][]byte, string, error) {
	if c.createCertForOrderFn != nil {
		return c.createCertForOrderFn(ctx, orderURL, finalizeURL, csr, bundle)
	}
	return c.createCertFn(ctx, finalizeURL, csr, bundle)
}

type legacyProtocolClient struct {
	ProtocolClient
}

func containsTestEvent(events []string, target string) bool {
	for _, event := range events {
		if event == target {
			return true
		}
	}
	return false
}

func (c *fakeProtocolClient) HTTP01ChallengeResponse(token string) (string, error) {
	return "response-for-" + token, nil
}

func (c *fakeProtocolClient) HTTP01ChallengePath(token string) string {
	return "/.well-known/acme-challenge/" + token
}

func (c *fakeProtocolClient) DNS01ChallengeRecord(token string) (string, error) {
	return "dns-for-" + token, nil
}

func newHappyProtocolClient(t *testing.T, now time.Time, events *[]string) *fakeProtocolClient {
	t.Helper()
	client := &fakeProtocolClient{}
	client.getRegFn = func(context.Context, string) (*acme.Account, error) {
		*events = append(*events, "get_reg")
		return nil, acme.ErrNoAccount
	}
	client.registerFn = func(_ context.Context, account *acme.Account, prompt func(string) bool) (*acme.Account, error) {
		*events = append(*events, "register")
		if !prompt("https://ca.invalid/tos") {
			t.Fatal("terms were not accepted")
		}
		return &acme.Account{URI: "https://ca.invalid/acct/1", Contact: account.Contact}, nil
	}
	client.getAuthzFn = func(context.Context, string) (*acme.Authorization, error) {
		*events = append(*events, "get_authorization")
		if client.authz != nil {
			return client.authz, nil
		}
		return &acme.Authorization{
			URI:        "https://ca.invalid/authz/1",
			Status:     acme.StatusPending,
			Identifier: acme.AuthzID{Type: "dns", Value: "example.com"},
			Challenges: []*acme.Challenge{{Type: ChallengeHTTP01, URI: "https://ca.invalid/challenge/1", Token: "token"}},
		}, nil
	}
	client.acceptFn = func(_ context.Context, challenge *acme.Challenge) (*acme.Challenge, error) {
		*events = append(*events, "accept")
		return challenge, nil
	}
	client.waitAuthzFn = func(context.Context, string) (*acme.Authorization, error) {
		*events = append(*events, "wait_authorization")
		return &acme.Authorization{Status: acme.StatusValid}, nil
	}
	client.waitOrderFn = func(context.Context, string) (*acme.Order, error) {
		*events = append(*events, "wait_order")
		return &acme.Order{URI: "https://ca.invalid/order/1", Status: acme.StatusReady, FinalizeURL: "https://ca.invalid/order/1/finalize"}, nil
	}
	client.createCertFn = func(_ context.Context, _ string, csrDER []byte, _ bool) ([][]byte, string, error) {
		*events = append(*events, "create_order_cert")
		return issueCSRChain(t, csrDER, now), "https://ca.invalid/cert/1", nil
	}
	client.authorizeOrderFn = func(context.Context, []acme.AuthzID, ...acme.OrderOption) (*acme.Order, error) {
		return nil, errors.New("unexpected AuthorizeOrder call")
	}
	return client
}

type fakeChallengeSolver struct {
	challengeType string
	events        *[]string
	waitFn        func(context.Context, Challenge) error
	cleanupFn     func(context.Context, Challenge) error
	cleanupCalls  int
}

func (s *fakeChallengeSolver) ChallengeType() string { return s.challengeType }

func (s *fakeChallengeSolver) Present(_ context.Context, challenge Challenge) error {
	*s.events = append(*s.events, "present")
	if challenge.Token == "" || (challenge.KeyAuthorization == "" && challenge.DNSValue == "") {
		return errors.New("missing challenge response")
	}
	return nil
}

func (s *fakeChallengeSolver) Wait(ctx context.Context, challenge Challenge) error {
	*s.events = append(*s.events, "wait_solver")
	if s.waitFn != nil {
		return s.waitFn(ctx, challenge)
	}
	return nil
}

func (s *fakeChallengeSolver) Cleanup(ctx context.Context, challenge Challenge) error {
	*s.events = append(*s.events, "cleanup")
	s.cleanupCalls++
	if s.cleanupFn != nil {
		return s.cleanupFn(ctx, challenge)
	}
	return nil
}

var testRSAKeyFixture struct {
	once      sync.Once
	primary   *rsa.PrivateKey
	secondary *rsa.PrivateKey
	err       error
}

func mustTestRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	initializeTestRSAKeys()
	if testRSAKeyFixture.err != nil {
		t.Fatal(testRSAKeyFixture.err)
	}
	return testRSAKeyFixture.primary
}

func mustOtherTestRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	initializeTestRSAKeys()
	if testRSAKeyFixture.err != nil {
		t.Fatal(testRSAKeyFixture.err)
	}
	return testRSAKeyFixture.secondary
}

func initializeTestRSAKeys() {
	testRSAKeyFixture.once.Do(func() {
		testRSAKeyFixture.primary, testRSAKeyFixture.err = rsa.GenerateKey(rand.Reader, 2048)
		if testRSAKeyFixture.err != nil {
			return
		}
		testRSAKeyFixture.primary.Precompute()
		testRSAKeyFixture.secondary, testRSAKeyFixture.err = rsa.GenerateKey(rand.Reader, 2048)
		if testRSAKeyFixture.err == nil {
			testRSAKeyFixture.secondary.Precompute()
		}
	})
}

func mustRSAKeyPEM(t *testing.T) []byte {
	t.Helper()
	key := mustTestRSAKey(t)
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
}

func issueCSRChain(t *testing.T, csrDER []byte, now time.Time) [][]byte {
	t.Helper()
	csr, err := x509.ParseCertificateRequest(csrDER)
	if err != nil {
		t.Fatal(err)
	}
	if err := csr.CheckSignature(); err != nil {
		t.Fatal(err)
	}
	caKey := mustOtherTestRSAKey(t)
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(100),
		Subject:               pkix.Name{CommonName: "Engine Test CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(48 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(101),
		Subject:      csr.Subject,
		DNSNames:     append([]string(nil), csr.DNSNames...),
		IPAddresses:  append([]net.IP(nil), csr.IPAddresses...),
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, ca, csr.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	return [][]byte{leafDER, caDER}
}
