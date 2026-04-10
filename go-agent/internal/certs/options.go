package certs

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/registration"
)

type Option func(*managerConfig)

type managerConfig struct {
	nodeRole   string
	localAgent bool
	acme       acmeConfig
	now        func() time.Time

	issuerFactory acmeIssuerFactory
}

type acmeConfig struct {
	directoryURL           string
	email                  string
	http01Interface        string
	http01Port             string
	cloudflareDNSAPIToken  string
	cloudflareZoneAPIToken string
	renewBefore            time.Duration
	renewalLoopInterval    time.Duration
	renewalAttemptTimeout  time.Duration
}

type acmeChallengeType string

const (
	challengeTypeHTTP01          acmeChallengeType = "http-01"
	challengeTypeDNS01Cloudflare acmeChallengeType = "dns-01-cloudflare"
)

type acmeIssueRequest struct {
	CertificateID int
	Domain        string
	Scope         string
	IssuerMode    string
	ChallengeType acmeChallengeType

	DirectoryURL string
	Email        string

	HTTP01Interface string
	HTTP01Port      string

	CloudflareDNSAPIToken  string
	CloudflareZoneAPIToken string

	ExistingKeyPEM []byte
	AccountKeyPEM  []byte
	Registration   *registration.Resource
}

type acmeIssueResult struct {
	CertPEM       []byte
	KeyPEM        []byte
	AccountKeyPEM []byte
	Registration  *registration.Resource
	Err           error
}

type acmeIssuer interface {
	Issue(ctx context.Context, request acmeIssueRequest) (acmeIssueResult, error)
}

type acmeIssuerFactory func(request acmeIssueRequest) (acmeIssuer, error)

func defaultManagerConfig() managerConfig {
	return managerConfig{
		nodeRole:   strings.TrimSpace(os.Getenv("NRE_NODE_ROLE")),
		localAgent: parseBoolEnv("NRE_LOCAL_AGENT"),
		acme: acmeConfig{
			directoryURL:           firstNonEmpty(strings.TrimSpace(os.Getenv("NRE_ACME_DIRECTORY_URL")), lego.LEDirectoryProduction),
			email:                  strings.TrimSpace(os.Getenv("NRE_ACME_EMAIL")),
			http01Interface:        strings.TrimSpace(os.Getenv("NRE_ACME_HTTP01_IFACE")),
			http01Port:             firstNonEmpty(strings.TrimSpace(os.Getenv("NRE_ACME_HTTP01_PORT")), "80"),
			cloudflareDNSAPIToken:  firstNonEmpty(strings.TrimSpace(os.Getenv("CLOUDFLARE_DNS_API_TOKEN")), strings.TrimSpace(os.Getenv("CF_DNS_API_TOKEN")), strings.TrimSpace(os.Getenv("CF_TOKEN")), strings.TrimSpace(os.Getenv("CF_Token"))),
			cloudflareZoneAPIToken: strings.TrimSpace(os.Getenv("CLOUDFLARE_ZONE_API_TOKEN")),
			renewBefore:            30 * 24 * time.Hour,
			renewalLoopInterval:    10 * time.Minute,
			renewalAttemptTimeout:  2 * time.Minute,
		},
		now:           time.Now,
		issuerFactory: defaultACMEIssuerFactory,
	}
}

func WithNodeRole(role string) Option {
	return func(cfg *managerConfig) {
		cfg.nodeRole = strings.TrimSpace(role)
	}
}

func WithLocalAgent(local bool) Option {
	return func(cfg *managerConfig) {
		cfg.localAgent = local
	}
}

func WithACMEDirectoryURL(url string) Option {
	return func(cfg *managerConfig) {
		cfg.acme.directoryURL = strings.TrimSpace(url)
	}
}

func WithACMEEmail(email string) Option {
	return func(cfg *managerConfig) {
		cfg.acme.email = strings.TrimSpace(email)
	}
}

func WithACMEHTTP01Address(iface, port string) Option {
	return func(cfg *managerConfig) {
		cfg.acme.http01Interface = strings.TrimSpace(iface)
		if strings.TrimSpace(port) != "" {
			cfg.acme.http01Port = strings.TrimSpace(port)
		}
	}
}

func WithCloudflareAPITokens(dnsToken, zoneToken string) Option {
	return func(cfg *managerConfig) {
		cfg.acme.cloudflareDNSAPIToken = strings.TrimSpace(dnsToken)
		cfg.acme.cloudflareZoneAPIToken = firstNonEmpty(strings.TrimSpace(zoneToken), strings.TrimSpace(dnsToken))
	}
}

func withACMEIssuerFactory(factory acmeIssuerFactory) Option {
	return func(cfg *managerConfig) {
		cfg.issuerFactory = factory
	}
}

func withNow(now func() time.Time) Option {
	return func(cfg *managerConfig) {
		if now != nil {
			cfg.now = now
		}
	}
}

func withRenewBefore(d time.Duration) Option {
	return func(cfg *managerConfig) {
		cfg.acme.renewBefore = d
	}
}

func withRenewalLoopInterval(d time.Duration) Option {
	return func(cfg *managerConfig) {
		cfg.acme.renewalLoopInterval = d
	}
}

func withRenewalAttemptTimeout(d time.Duration) Option {
	return func(cfg *managerConfig) {
		cfg.acme.renewalAttemptTimeout = d
	}
}

func parseBoolEnv(name string) bool {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return false
	}
	parsed, err := strconv.ParseBool(raw)
	return err == nil && parsed
}
