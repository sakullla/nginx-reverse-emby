package acmeflow

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/acme"
)

const (
	ChallengeHTTP01 = "http-01"
	ChallengeDNS01  = "dns-01"
)

// Challenge contains only the values a solver needs to provision and remove
// one authorization challenge. It never contains an account key or provider
// credential.
type Challenge struct {
	Type             string
	URI              string
	Token            string
	AuthorizationURI string
	Identifier       Identifier
	Wildcard         bool
	HTTPPath         string
	KeyAuthorization string
	DNSValue         string
}

// ChallengeSolver owns Present/Wait/Cleanup for exactly one challenge type.
// Cleanup must be idempotent because recovery code may repeat it later.
type ChallengeSolver interface {
	ChallengeType() string
	Present(context.Context, Challenge) error
	Wait(context.Context, Challenge) error
	Cleanup(context.Context, Challenge) error
}

type IssueRequest struct {
	DirectoryURL string
	Email        string
	Identifiers  []Identifier
	Profile      string

	ChallengeType string
	Solver        ChallengeSolver
	AccountStore  AccountStore
	HTTPClient    *http.Client

	// ExistingKeyPEM is reused for Agent renewal. Leave it empty when the
	// owner policy requires a new certificate key, such as master issuance.
	ExistingKeyPEM []byte
}

type IssueResult struct {
	CertificatePEM []byte
	PrivateKeyPEM  []byte
	AccountKeyPEM  []byte
	Account        AccountMetadata
	Profile        string
	OrderURI       string
	CertificateURL string
}

// Engine owns the RFC 8555 lifecycle but not owner persistence, challenge
// implementation, renewal scheduling, or material deployment.
type Engine struct {
	ClientFactory ProtocolClientFactory
	OrderStarter  OrderStarter
	Rand          io.Reader
	Now           func() time.Time

	// CleanupTimeout applies to a context detached from issuance cancellation,
	// ensuring a cancelled request still attempts bounded cleanup.
	CleanupTimeout time.Duration
}

func (e Engine) Issue(ctx context.Context, request IssueRequest) (IssueResult, error) {
	var result IssueResult
	if ctx == nil {
		return result, WrapError(CategoryProtocol, "issue", errors.New("context is nil"))
	}
	if err := ctx.Err(); err != nil {
		return result, normalizeError("issue", err)
	}
	identifiers, err := normalizeIdentifiers(request.Identifiers)
	if err != nil {
		return result, err
	}
	request.DirectoryURL = strings.TrimSpace(request.DirectoryURL)
	request.Email = strings.TrimSpace(request.Email)
	request.Profile = strings.TrimSpace(request.Profile)
	if request.Solver == nil {
		return result, WrapError(CategoryChallenge, "challenge_solver", errors.New("challenge solver is nil"))
	}
	solverType := strings.TrimSpace(request.Solver.ChallengeType())
	if solverType == "" {
		return result, WrapError(CategoryChallenge, "challenge_solver", errors.New("challenge type is empty"))
	}
	if configuredType := strings.TrimSpace(request.ChallengeType); configuredType != "" && configuredType != solverType {
		return result, WrapError(CategoryChallenge, "challenge_solver", errors.New("challenge type mismatch"))
	}

	random := e.Rand
	if random == nil {
		random = rand.Reader
	}
	account, err := prepareAccount(
		ctx,
		AccountLookup{DirectoryURL: request.DirectoryURL, Email: request.Email},
		request.AccountStore,
		e.ClientFactory,
		request.HTTPClient,
		random,
	)
	if err != nil {
		return result, err
	}

	starter := e.OrderStarter
	if starter == nil {
		starter = DefaultOrderStarter{}
	}
	order, err := starter.StartOrder(ctx, OrderStartRequest{
		Client:       account.client,
		DirectoryURL: request.DirectoryURL,
		AccountURI:   account.record.Metadata.URI,
		AccountKey:   account.key,
		HTTPClient:   request.HTTPClient,
		Identifiers:  identifiers,
		Profile:      request.Profile,
	})
	if err != nil {
		return result, normalizeError("new_order", err)
	}
	if err := validateStartedOrder(order, identifiers); err != nil {
		return result, err
	}

	for _, authorizationURI := range order.AuthzURLs {
		if err := e.fulfillAuthorization(ctx, account.client, request.Solver, authorizationURI, identifiers); err != nil {
			return result, err
		}
	}
	readyOrder, err := account.client.WaitOrder(ctx, order.URI)
	if err != nil {
		return result, normalizeError("wait_order", err)
	}
	if readyOrder == nil {
		return result, WrapError(CategoryOrder, "wait_order", errors.New("order response is nil"))
	}
	finalizeURL := strings.TrimSpace(readyOrder.FinalizeURL)
	if finalizeURL == "" {
		finalizeURL = strings.TrimSpace(order.FinalizeURL)
	}
	if finalizeURL == "" {
		return result, WrapError(CategoryOrder, "wait_order", errors.New("order finalize URL is empty"))
	}

	certificateKey, certificateKeyPEM, _, err := prepareCertificateKey(request.ExistingKeyPEM, random)
	if err != nil {
		return result, normalizeError("certificate_key", err)
	}
	csr, err := createCSR(certificateKey, identifiers)
	if err != nil {
		return result, err
	}
	var chain [][]byte
	var certificateURL string
	if finalizer, ok := account.client.(OrderCertificateFinalizer); ok {
		chain, certificateURL, err = finalizer.CreateOrderCertForOrder(ctx, order.URI, finalizeURL, csr, true)
	} else {
		chain, certificateURL, err = account.client.CreateOrderCert(ctx, finalizeURL, csr, true)
	}
	if err != nil {
		return result, normalizeError("finalize_order", err)
	}
	certificatePEM, err := encodeCertificateChain(chain)
	if err != nil {
		return result, normalizeError("material_certificate", err)
	}
	now := time.Now()
	if e.Now != nil {
		now = e.Now()
	}
	if _, err := ValidateMaterial(CertificateMaterial{
		CertificatePEM: certificatePEM,
		PrivateKeyPEM:  certificateKeyPEM,
		Profile:        request.Profile,
	}, MaterialPolicy{
		Identifiers: identifiers,
		Profile:     request.Profile,
		Now:         now,
	}); err != nil {
		return result, err
	}

	result = IssueResult{
		CertificatePEM: append([]byte(nil), certificatePEM...),
		PrivateKeyPEM:  append([]byte(nil), certificateKeyPEM...),
		AccountKeyPEM:  append([]byte(nil), account.record.KeyPEM...),
		Account:        cloneAccountMetadata(account.record.Metadata),
		Profile:        request.Profile,
		OrderURI:       order.URI,
		CertificateURL: certificateURL,
	}
	return result, nil
}

func (e Engine) fulfillAuthorization(
	ctx context.Context,
	client ProtocolClient,
	solver ChallengeSolver,
	authorizationURI string,
	expectedIdentifiers []Identifier,
) error {
	if err := ctx.Err(); err != nil {
		return normalizeError("get_authorization", err)
	}
	authorization, err := client.GetAuthorization(ctx, authorizationURI)
	if err != nil {
		return normalizeError("get_authorization", err)
	}
	if authorization == nil {
		return WrapError(CategoryAuthorization, "get_authorization", errors.New("authorization is nil"))
	}
	authorizationIdentifier := Identifier{Type: IdentifierType(authorization.Identifier.Type), Value: authorization.Identifier.Value}
	if authorization.Wildcard && authorizationIdentifier.Type == IdentifierDNS && !strings.HasPrefix(authorizationIdentifier.Value, "*.") {
		authorizationIdentifier.Value = "*." + authorizationIdentifier.Value
	}
	if !containsIdentifier(expectedIdentifiers, authorizationIdentifier) {
		return WrapError(CategoryAuthorization, "get_authorization", errors.New("authorization identifier is outside the order"))
	}
	if authorization.Status == acme.StatusValid {
		return nil
	}
	if authorization.Status != acme.StatusPending {
		return WrapError(CategoryAuthorization, "get_authorization", errors.New("authorization is not pending"))
	}

	challengeType := strings.TrimSpace(solver.ChallengeType())
	selected := selectChallenge(authorization.Challenges, challengeType)
	if selected == nil {
		return WrapError(CategoryChallenge, "challenge_select", errors.New("requested challenge is unavailable"))
	}
	if strings.TrimSpace(selected.URI) == "" || strings.TrimSpace(selected.Token) == "" {
		return WrapError(CategoryChallenge, "challenge_select", errors.New("challenge response is incomplete"))
	}
	challenge, err := buildChallenge(client, authorizationURI, authorization, selected)
	if err != nil {
		return err
	}

	primary := solver.Present(ctx, challenge)
	if primary != nil {
		primary = normalizeError("challenge_present", primary)
	} else if primary = solver.Wait(ctx, challenge); primary != nil {
		primary = normalizeError("challenge_wait", primary)
	} else if _, primary = client.Accept(ctx, selected); primary != nil {
		primary = normalizeError("challenge_accept", primary)
	} else {
		var completed *acme.Authorization
		completed, primary = client.WaitAuthorization(ctx, authorizationURI)
		if primary != nil {
			primary = normalizeError("wait_authorization", primary)
		} else if completed == nil || completed.Status != acme.StatusValid {
			primary = WrapError(CategoryAuthorization, "wait_authorization", errors.New("authorization did not become valid"))
		}
	}

	cleanupErr := e.cleanupChallenge(ctx, solver, challenge)
	return mergeCleanupError(primary, cleanupErr)
}

func containsIdentifier(identifiers []Identifier, candidate Identifier) bool {
	normalizedCandidate, err := normalizeIdentifiers([]Identifier{candidate})
	if err != nil {
		return false
	}
	normalizedIdentifiers, err := normalizeIdentifiers(identifiers)
	if err != nil {
		return false
	}
	for _, identifier := range normalizedIdentifiers {
		if identifier == normalizedCandidate[0] {
			return true
		}
	}
	return false
}

func (e Engine) cleanupChallenge(parent context.Context, solver ChallengeSolver, challenge Challenge) error {
	cleanupContext := context.WithoutCancel(parent)
	timeout := e.CleanupTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	cleanupContext, cancel := context.WithTimeout(cleanupContext, timeout)
	defer cancel()
	if err := solver.Cleanup(cleanupContext, challenge); err != nil {
		return normalizeError("challenge_cleanup", err)
	}
	return nil
}

func selectChallenge(challenges []*acme.Challenge, challengeType string) *acme.Challenge {
	for _, challenge := range challenges {
		if challenge != nil && challenge.Type == challengeType {
			return challenge
		}
	}
	return nil
}

func buildChallenge(client ProtocolClient, authorizationURI string, authorization *acme.Authorization, selected *acme.Challenge) (Challenge, error) {
	identifier := Identifier{Type: IdentifierType(authorization.Identifier.Type), Value: authorization.Identifier.Value}
	if authorization.Wildcard && identifier.Type == IdentifierDNS && !strings.HasPrefix(identifier.Value, "*.") {
		identifier.Value = "*." + identifier.Value
	}
	challenge := Challenge{
		Type:             selected.Type,
		URI:              selected.URI,
		Token:            selected.Token,
		AuthorizationURI: authorizationURI,
		Identifier:       identifier,
		Wildcard:         authorization.Wildcard,
	}
	var err error
	switch selected.Type {
	case ChallengeHTTP01:
		challenge.HTTPPath = client.HTTP01ChallengePath(selected.Token)
		challenge.KeyAuthorization, err = client.HTTP01ChallengeResponse(selected.Token)
	case ChallengeDNS01:
		challenge.DNSValue, err = client.DNS01ChallengeRecord(selected.Token)
	default:
		return Challenge{}, WrapError(CategoryChallenge, "challenge_response", errors.New("unsupported challenge type"))
	}
	if err != nil {
		return Challenge{}, normalizeError("challenge_response", err)
	}
	return challenge, nil
}

func validateStartedOrder(order *acme.Order, identifiers []Identifier) error {
	if order == nil || strings.TrimSpace(order.URI) == "" || strings.TrimSpace(order.FinalizeURL) == "" {
		return WrapError(CategoryOrder, "new_order", errors.New("order response is incomplete"))
	}
	actual := make([]Identifier, 0, len(order.Identifiers))
	for _, identifier := range order.Identifiers {
		actual = append(actual, Identifier{Type: IdentifierType(identifier.Type), Value: identifier.Value})
	}
	if !equalIdentifiers(actual, identifiers) {
		return WrapError(CategoryOrder, "new_order", errors.New("order identifiers mismatch"))
	}
	switch order.Status {
	case acme.StatusPending, acme.StatusReady, acme.StatusProcessing, acme.StatusValid:
		return nil
	default:
		return WrapError(CategoryOrder, "new_order", errors.New("unexpected order status"))
	}
}
