package acmeflow

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"net"
	"net/http"
	"strings"

	"golang.org/x/crypto/acme"
)

type IdentifierType string

const (
	IdentifierDNS IdentifierType = "dns"
	IdentifierIP  IdentifierType = "ip"
)

type Identifier struct {
	Type  IdentifierType
	Value string
}

// ClientConfig is the small construction boundary around x/crypto/acme.
type ClientConfig struct {
	Key          crypto.Signer
	AccountURI   string
	DirectoryURL string
	HTTPClient   *http.Client
}

type ProtocolClientFactory func(ClientConfig) ProtocolClient

// ProtocolClient lists only the RFC 8555 primitives used by Engine. Challenge
// providers, persistence, renewal, and deployment intentionally remain outside
// this interface.
type ProtocolClient interface {
	SetAccountURI(string)
	GetReg(context.Context, string) (*acme.Account, error)
	Register(context.Context, *acme.Account, func(string) bool) (*acme.Account, error)
	AuthorizeOrder(context.Context, []acme.AuthzID, ...acme.OrderOption) (*acme.Order, error)
	GetAuthorization(context.Context, string) (*acme.Authorization, error)
	Accept(context.Context, *acme.Challenge) (*acme.Challenge, error)
	WaitAuthorization(context.Context, string) (*acme.Authorization, error)
	WaitOrder(context.Context, string) (*acme.Order, error)
	CreateOrderCert(context.Context, string, []byte, bool) ([][]byte, string, error)
	HTTP01ChallengeResponse(string) (string, error)
	HTTP01ChallengePath(string) string
	DNS01ChallengeRecord(string) (string, error)
}

type protocolClient struct {
	client *acme.Client
}

// NewProtocolClient creates the package's default RFC 8555 primitive client.
func NewProtocolClient(config ClientConfig) ProtocolClient {
	client := &acme.Client{
		Key:          config.Key,
		HTTPClient:   config.HTTPClient,
		DirectoryURL: strings.TrimSpace(config.DirectoryURL),
		KID:          acme.KeyID(strings.TrimSpace(config.AccountURI)),
		UserAgent:    "nginx-reverse-emby/acmeflow",
	}
	return &protocolClient{client: client}
}

func (c *protocolClient) SetAccountURI(uri string) {
	c.client.KID = acme.KeyID(strings.TrimSpace(uri))
}

func (c *protocolClient) GetReg(ctx context.Context, uri string) (*acme.Account, error) {
	return c.client.GetReg(ctx, uri)
}

func (c *protocolClient) Register(ctx context.Context, account *acme.Account, prompt func(string) bool) (*acme.Account, error) {
	return c.client.Register(ctx, account, prompt)
}

func (c *protocolClient) AuthorizeOrder(ctx context.Context, identifiers []acme.AuthzID, options ...acme.OrderOption) (*acme.Order, error) {
	return c.client.AuthorizeOrder(ctx, identifiers, options...)
}

func (c *protocolClient) GetAuthorization(ctx context.Context, uri string) (*acme.Authorization, error) {
	return c.client.GetAuthorization(ctx, uri)
}

func (c *protocolClient) Accept(ctx context.Context, challenge *acme.Challenge) (*acme.Challenge, error) {
	return c.client.Accept(ctx, challenge)
}

func (c *protocolClient) WaitAuthorization(ctx context.Context, uri string) (*acme.Authorization, error) {
	return c.client.WaitAuthorization(ctx, uri)
}

func (c *protocolClient) WaitOrder(ctx context.Context, uri string) (*acme.Order, error) {
	return c.client.WaitOrder(ctx, uri)
}

func (c *protocolClient) CreateOrderCert(ctx context.Context, uri string, csr []byte, bundle bool) ([][]byte, string, error) {
	return c.client.CreateOrderCert(ctx, uri, csr, bundle)
}

func (c *protocolClient) HTTP01ChallengeResponse(token string) (string, error) {
	return c.client.HTTP01ChallengeResponse(token)
}

func (c *protocolClient) HTTP01ChallengePath(token string) string {
	return c.client.HTTP01ChallengePath(token)
}

func (c *protocolClient) DNS01ChallengeRecord(token string) (string, error) {
	return c.client.DNS01ChallengeRecord(token)
}

type OrderStartRequest struct {
	Client       ProtocolClient
	DirectoryURL string
	AccountURI   string
	AccountKey   crypto.Signer
	HTTPClient   *http.Client
	Identifiers  []Identifier
	Profile      string
}

type OrderStarter interface {
	StartOrder(context.Context, OrderStartRequest) (*acme.Order, error)
}

type OrderStarterFunc func(context.Context, OrderStartRequest) (*acme.Order, error)

func (f OrderStarterFunc) StartOrder(ctx context.Context, request OrderStartRequest) (*acme.Order, error) {
	return f(ctx, request)
}

// DefaultOrderStarter keeps ordinary orders on x/crypto/acme and delegates
// only profile-bearing newOrder requests to the isolated extension.
type DefaultOrderStarter struct {
	ProfileStarter OrderStarter
}

func (s DefaultOrderStarter) StartOrder(ctx context.Context, request OrderStartRequest) (*acme.Order, error) {
	if strings.TrimSpace(request.Profile) == "" {
		if request.Client == nil {
			return nil, WrapError(CategoryOrder, "new_order", errors.New("protocol client is nil"))
		}
		identifiers, err := toACMEIdentifiers(request.Identifiers)
		if err != nil {
			return nil, err
		}
		order, err := request.Client.AuthorizeOrder(ctx, identifiers)
		if err != nil {
			return nil, normalizeError("new_order", err)
		}
		return order, nil
	}
	profileStarter := s.ProfileStarter
	if profileStarter == nil {
		profileStarter = ProfileOrderStarter{}
	}
	return profileStarter.StartOrder(ctx, request)
}

func normalizeIdentifiers(identifiers []Identifier) ([]Identifier, error) {
	if len(identifiers) == 0 {
		return nil, WrapError(CategoryOrder, "identifiers", errors.New("no identifiers"))
	}
	normalized := make([]Identifier, 0, len(identifiers))
	seen := make(map[string]struct{}, len(identifiers))
	for _, identifier := range identifiers {
		value := strings.TrimSpace(identifier.Value)
		switch identifier.Type {
		case IdentifierDNS:
			value = strings.ToLower(strings.TrimSuffix(value, "."))
			if err := validateDNSIdentifier(value); err != nil {
				return nil, WrapError(CategoryOrder, "identifiers", err)
			}
		case IdentifierIP:
			ip := net.ParseIP(value)
			if ip == nil || strings.HasPrefix(value, "*") {
				return nil, WrapError(CategoryOrder, "identifiers", errors.New("invalid IP identifier"))
			}
			value = ip.String()
		default:
			return nil, WrapError(CategoryOrder, "identifiers", errors.New("unsupported identifier type"))
		}
		key := string(identifier.Type) + "\x00" + value
		if _, exists := seen[key]; exists {
			return nil, WrapError(CategoryOrder, "identifiers", errors.New("duplicate identifier"))
		}
		seen[key] = struct{}{}
		normalized = append(normalized, Identifier{Type: identifier.Type, Value: value})
	}
	return normalized, nil
}

func validateDNSIdentifier(value string) error {
	base := value
	if strings.HasPrefix(base, "*.") {
		base = strings.TrimPrefix(base, "*.")
	}
	if base == "" || strings.Contains(base, "*") || len(base) > 253 || strings.ContainsAny(base, " /\\\t\r\n") {
		return errors.New("invalid DNS identifier")
	}
	for _, label := range strings.Split(base, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return errors.New("invalid DNS identifier")
		}
	}
	return nil
}

func toACMEIdentifiers(identifiers []Identifier) ([]acme.AuthzID, error) {
	normalized, err := normalizeIdentifiers(identifiers)
	if err != nil {
		return nil, err
	}
	result := make([]acme.AuthzID, 0, len(normalized))
	for _, identifier := range normalized {
		result = append(result, acme.AuthzID{Type: string(identifier.Type), Value: identifier.Value})
	}
	return result, nil
}

func createCSR(key crypto.Signer, identifiers []Identifier) ([]byte, error) {
	if key == nil {
		return nil, WrapError(CategoryMaterial, "csr", errors.New("certificate key is nil"))
	}
	normalized, err := normalizeIdentifiers(identifiers)
	if err != nil {
		return nil, err
	}
	template := &x509.CertificateRequest{}
	for _, identifier := range normalized {
		switch identifier.Type {
		case IdentifierDNS:
			template.DNSNames = append(template.DNSNames, identifier.Value)
			if template.Subject.CommonName == "" {
				template.Subject = pkix.Name{CommonName: identifier.Value}
			}
		case IdentifierIP:
			template.IPAddresses = append(template.IPAddresses, net.ParseIP(identifier.Value))
		}
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, template, key)
	if err != nil {
		return nil, normalizeError("csr", err)
	}
	return csr, nil
}
