package acmeflow

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/crypto/acme"
)

const maxProfileResponseSize = 1 << 20

// ProfileOrderStarter implements only the profile-bearing newOrder extension.
// All subsequent RFC 8555 operations remain on ProtocolClient.
type ProfileOrderStarter struct {
	// MaxBadNonceRetries is the number of retries after the first request. A
	// zero value uses two retries; a negative value disables retries.
	MaxBadNonceRetries int
}

func (s ProfileOrderStarter) StartOrder(ctx context.Context, request OrderStartRequest) (*acme.Order, error) {
	profile := strings.TrimSpace(request.Profile)
	if profile == "" {
		return nil, WrapError(CategoryProfile, "profile_new_order", errors.New("profile is empty"))
	}
	if request.AccountKey == nil || strings.TrimSpace(request.AccountURI) == "" {
		return nil, WrapError(CategoryAccount, "profile_new_order", errors.New("account key or URI is empty"))
	}
	identifiers, err := normalizeIdentifiers(request.Identifiers)
	if err != nil {
		return nil, err
	}

	client := profileHTTPClient(request.HTTPClient)
	directory, nonce, err := loadProfileDirectory(ctx, client, request.DirectoryURL)
	if err != nil {
		return nil, err
	}
	if _, advertised := directory.Meta.Profiles[profile]; !advertised {
		return nil, WrapError(CategoryProfile, "profile_discovery", errors.New("profile is not advertised"))
	}
	if nonce == "" {
		nonce, err = fetchProfileNonce(ctx, client, directory.NewNonce)
		if err != nil {
			return nil, err
		}
	}

	payload := profileOrderPayload{
		Identifiers: make([]profileIdentifier, 0, len(identifiers)),
		Profile:     profile,
	}
	for _, identifier := range identifiers {
		payload.Identifiers = append(payload.Identifiers, profileIdentifier{
			Type:  string(identifier.Type),
			Value: identifier.Value,
		})
	}

	maxRetries := s.MaxBadNonceRetries
	if maxRetries == 0 {
		maxRetries = 2
	} else if maxRetries < 0 {
		maxRetries = 0
	}
	for attempt := 0; ; attempt++ {
		envelope, err := encodeProfileJWS(request.AccountKey, request.AccountURI, nonce, directory.NewOrder, payload)
		if err != nil {
			return nil, normalizeError("profile_jws", err)
		}
		order, nextNonce, responseErr := postProfileOrder(ctx, client, directory.NewOrder, envelope, identifiers, profile)
		if responseErr == nil {
			return order, nil
		}
		if ErrorCategoryOf(responseErr) != CategoryBadNonce || attempt >= maxRetries {
			return nil, responseErr
		}
		nonce = nextNonce
		if nonce == "" {
			nonce, err = fetchProfileNonce(ctx, client, directory.NewNonce)
			if err != nil {
				return nil, err
			}
		}
	}
}

type profileDirectory struct {
	NewNonce string `json:"newNonce"`
	NewOrder string `json:"newOrder"`
	Meta     struct {
		Profiles map[string]json.RawMessage `json:"profiles"`
	} `json:"meta"`
}

type profileIdentifier struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

type profileOrderPayload struct {
	Identifiers []profileIdentifier `json:"identifiers"`
	Profile     string              `json:"profile"`
}

type profileJWSEnvelope struct {
	Protected string `json:"protected"`
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

func loadProfileDirectory(ctx context.Context, client *http.Client, directoryURL string) (profileDirectory, string, error) {
	var directory profileDirectory
	if err := validateEndpointURL(directoryURL); err != nil {
		return directory, "", WrapError(CategoryProfile, "profile_discovery", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, directoryURL, nil)
	if err != nil {
		return directory, "", normalizeError("profile_discovery", err)
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return directory, "", normalizeError("profile_discovery", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return directory, response.Header.Get("Replay-Nonce"), normalizeHTTPProblem("profile_discovery", response)
	}
	if err := decodeLimitedJSON(response.Body, &directory); err != nil {
		return directory, "", normalizeError("profile_discovery", err)
	}
	if err := validateEndpointURL(directory.NewNonce); err != nil {
		return directory, "", WrapError(CategoryProfile, "profile_discovery", err)
	}
	if err := validateEndpointURL(directory.NewOrder); err != nil {
		return directory, "", WrapError(CategoryProfile, "profile_discovery", err)
	}
	if directory.Meta.Profiles == nil {
		directory.Meta.Profiles = map[string]json.RawMessage{}
	}
	return directory, strings.TrimSpace(response.Header.Get("Replay-Nonce")), nil
}

func fetchProfileNonce(ctx context.Context, client *http.Client, nonceURL string) (string, error) {
	for _, method := range []string{http.MethodHead, http.MethodGet} {
		request, err := http.NewRequestWithContext(ctx, method, nonceURL, nil)
		if err != nil {
			return "", normalizeError("profile_nonce", err)
		}
		response, err := client.Do(request)
		if err != nil {
			return "", normalizeError("profile_nonce", err)
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		response.Body.Close()
		nonce := strings.TrimSpace(response.Header.Get("Replay-Nonce"))
		if (response.StatusCode == http.StatusOK || response.StatusCode == http.StatusNoContent) && nonce != "" {
			return nonce, nil
		}
		if method == http.MethodHead && response.StatusCode == http.StatusMethodNotAllowed {
			continue
		}
		if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusNoContent {
			return "", normalizeHTTPProblem("profile_nonce", response)
		}
		return "", WrapError(CategoryProtocol, "profile_nonce", errors.New("replay nonce is missing"))
	}
	return "", WrapError(CategoryProtocol, "profile_nonce", errors.New("replay nonce is missing"))
}

func postProfileOrder(
	ctx context.Context,
	client *http.Client,
	orderURL string,
	envelope profileJWSEnvelope,
	expectedIdentifiers []Identifier,
	expectedProfile string,
) (*acme.Order, string, error) {
	body, err := json.Marshal(envelope)
	if err != nil {
		return nil, "", normalizeError("profile_jws", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, orderURL, bytes.NewReader(body))
	if err != nil {
		return nil, "", normalizeError("profile_new_order", err)
	}
	request.Header.Set("Content-Type", "application/jose+json")
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return nil, "", normalizeError("profile_new_order", err)
	}
	defer response.Body.Close()
	nextNonce := strings.TrimSpace(response.Header.Get("Replay-Nonce"))
	if response.StatusCode != http.StatusCreated {
		return nil, nextNonce, normalizeHTTPProblem("profile_new_order", response)
	}

	var wire struct {
		Status         string              `json:"status"`
		Expires        time.Time           `json:"expires"`
		Identifiers    []profileIdentifier `json:"identifiers"`
		NotBefore      time.Time           `json:"notBefore"`
		NotAfter       time.Time           `json:"notAfter"`
		Authorizations []string            `json:"authorizations"`
		Finalize       string              `json:"finalize"`
		Certificate    string              `json:"certificate"`
		Profile        string              `json:"profile"`
	}
	if err := decodeLimitedJSON(response.Body, &wire); err != nil {
		return nil, nextNonce, normalizeError("profile_new_order", err)
	}
	if wire.Profile != expectedProfile {
		return nil, nextNonce, WrapError(CategoryProfile, "profile_new_order", errors.New("order profile mismatch"))
	}
	actualIdentifiers := make([]Identifier, 0, len(wire.Identifiers))
	for _, identifier := range wire.Identifiers {
		actualIdentifiers = append(actualIdentifiers, Identifier{Type: IdentifierType(identifier.Type), Value: identifier.Value})
	}
	if !equalIdentifiers(actualIdentifiers, expectedIdentifiers) {
		return nil, nextNonce, WrapError(CategoryOrder, "profile_new_order", errors.New("order identifiers mismatch"))
	}
	location := strings.TrimSpace(response.Header.Get("Location"))
	if location == "" || strings.TrimSpace(wire.Finalize) == "" {
		return nil, nextNonce, WrapError(CategoryProtocol, "profile_new_order", errors.New("order response is incomplete"))
	}
	switch wire.Status {
	case acme.StatusPending, acme.StatusReady, acme.StatusProcessing, acme.StatusValid:
	default:
		return nil, nextNonce, WrapError(CategoryOrder, "profile_new_order", errors.New("unexpected order status"))
	}
	order := &acme.Order{
		URI:         location,
		Status:      wire.Status,
		Expires:     wire.Expires,
		NotBefore:   wire.NotBefore,
		NotAfter:    wire.NotAfter,
		AuthzURLs:   append([]string(nil), wire.Authorizations...),
		FinalizeURL: wire.Finalize,
		CertURL:     wire.Certificate,
	}
	for _, identifier := range wire.Identifiers {
		order.Identifiers = append(order.Identifiers, acme.AuthzID{Type: identifier.Type, Value: identifier.Value})
	}
	return order, nextNonce, nil
}

func encodeProfileJWS(key crypto.Signer, accountURI, nonce, orderURL string, payload profileOrderPayload) (profileJWSEnvelope, error) {
	var envelope profileJWSEnvelope
	if strings.TrimSpace(nonce) == "" {
		return envelope, errors.New("replay nonce is empty")
	}
	algorithm, hash, signatureSize, err := jwsAlgorithm(key)
	if err != nil {
		return envelope, err
	}
	protectedJSON, err := json.Marshal(struct {
		Algorithm string `json:"alg"`
		KID       string `json:"kid"`
		Nonce     string `json:"nonce"`
		URL       string `json:"url"`
	}{
		Algorithm: algorithm,
		KID:       strings.TrimSpace(accountURI),
		Nonce:     nonce,
		URL:       orderURL,
	})
	if err != nil {
		return envelope, err
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return envelope, err
	}
	envelope.Protected = base64.RawURLEncoding.EncodeToString(protectedJSON)
	envelope.Payload = base64.RawURLEncoding.EncodeToString(payloadJSON)
	signingInput := []byte(envelope.Protected + "." + envelope.Payload)
	digest := hash.New()
	_, _ = digest.Write(signingInput)
	signature, err := key.Sign(rand.Reader, digest.Sum(nil), hash)
	if err != nil {
		return envelope, err
	}
	if signatureSize > 0 {
		signature, err = normalizeECDSASignature(signature, signatureSize)
		if err != nil {
			return envelope, err
		}
	}
	envelope.Signature = base64.RawURLEncoding.EncodeToString(signature)
	return envelope, nil
}

func jwsAlgorithm(key crypto.Signer) (string, crypto.Hash, int, error) {
	if key == nil {
		return "", 0, 0, errors.New("account signer is nil")
	}
	switch publicKey := key.Public().(type) {
	case *rsa.PublicKey:
		if publicKey.N == nil || publicKey.N.BitLen() < 2048 {
			return "", 0, 0, errors.New("RSA account key is smaller than 2048 bits")
		}
		return "RS256", crypto.SHA256, 0, nil
	case *ecdsa.PublicKey:
		if publicKey.Curve == nil {
			return "", 0, 0, errors.New("ECDSA account key has no curve")
		}
		switch publicKey.Curve.Params().BitSize {
		case 256:
			return "ES256", crypto.SHA256, 32, nil
		case 384:
			return "ES384", crypto.SHA384, 48, nil
		case 521:
			return "ES512", crypto.SHA512, 66, nil
		default:
			return "", 0, 0, errors.New("unsupported ECDSA account curve")
		}
	default:
		return "", 0, 0, errors.New("unsupported account key type")
	}
}

func normalizeECDSASignature(signature []byte, size int) ([]byte, error) {
	if len(signature) == size*2 {
		return signature, nil
	}
	var parsed struct {
		R *big.Int
		S *big.Int
	}
	rest, err := asn1.Unmarshal(signature, &parsed)
	if err != nil || len(rest) != 0 || parsed.R == nil || parsed.S == nil || parsed.R.Sign() <= 0 || parsed.S.Sign() <= 0 {
		return nil, errors.New("invalid ECDSA signature")
	}
	if parsed.R.BitLen() > size*8 || parsed.S.BitLen() > size*8 {
		return nil, errors.New("oversized ECDSA signature")
	}
	raw := make([]byte, size*2)
	parsed.R.FillBytes(raw[:size])
	parsed.S.FillBytes(raw[size:])
	return raw, nil
}

func normalizeHTTPProblem(operation string, response *http.Response) error {
	problemType := ""
	if response.Body != nil {
		var problem struct {
			Type string `json:"type"`
		}
		_ = decodeLimitedJSON(response.Body, &problem)
		problemType = problem.Type
	}
	return normalizeError(operation, &acme.Error{
		StatusCode:  response.StatusCode,
		ProblemType: problemType,
		Header:      response.Header.Clone(),
	})
}

func decodeLimitedJSON(reader io.Reader, destination any) error {
	limited := io.LimitReader(reader, maxProfileResponseSize+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return err
	}
	if len(data) > maxProfileResponseSize {
		return errors.New("ACME response is too large")
	}
	if err := json.Unmarshal(data, destination); err != nil {
		return fmt.Errorf("invalid ACME JSON response")
	}
	return nil
}

func equalIdentifiers(left, right []Identifier) bool {
	leftNormalized, leftErr := normalizeIdentifiers(left)
	rightNormalized, rightErr := normalizeIdentifiers(right)
	if leftErr != nil || rightErr != nil || len(leftNormalized) != len(rightNormalized) {
		return false
	}
	want := make(map[string]int, len(leftNormalized))
	for _, identifier := range leftNormalized {
		want[string(identifier.Type)+"\x00"+identifier.Value]++
	}
	for _, identifier := range rightNormalized {
		key := string(identifier.Type) + "\x00" + identifier.Value
		if want[key] == 0 {
			return false
		}
		want[key]--
	}
	return true
}

func validateEndpointURL(rawURL string) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.User != nil {
		return errors.New("invalid ACME endpoint URL")
	}
	return nil
}

func profileHTTPClient(base *http.Client) *http.Client {
	if base == nil {
		base = newDefaultACMEHTTPClient()
	}
	clone := *base
	clone.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &clone
}
