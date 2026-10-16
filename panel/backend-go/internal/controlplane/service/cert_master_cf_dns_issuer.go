package service

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/acmeflow"
	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/acmeflow/cloudflare"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

const defaultMasterACMEDirectoryURL = "https://acme-v02.api.letsencrypt.org/directory"

type masterACMEEngine interface {
	Issue(context.Context, acmeflow.IssueRequest) (acmeflow.IssueResult, error)
}

type masterACMESolver = acmeflow.ChallengeSolver

type masterCFDNSManagedCertificateIssuer struct {
	directoryURL      string
	email             string
	cfZoneToken       string
	dataDir           string
	engine            masterACMEEngine
	openState         func(string) (masterACMEStateStore, error)
	newSolver         func(masterACMEStateStore, string, string) (masterACMESolver, error)
	resolveToken      func(context.Context, string) (string, error)
	records           managedDNSRecordProvider
	propagation       pluginDNSPropagation
	cloudflareEnabled bool
	now               func() time.Time
}

func newMasterCFDNSManagedCertificateIssuer() managedCertificateRenewalIssuer {
	dnsToken := firstNonEmptyEnv("CLOUDFLARE_DNS_API_TOKEN", "CF_DNS_API_TOKEN", "CF_TOKEN", "CF_Token")
	if dnsToken == "" {
		return nil
	}
	return newMasterCFDNSManagedCertificateIssuerWithResolver(func(context.Context, string) (string, error) {
		return dnsToken, nil
	})
}

func newMasterCFDNSManagedCertificateIssuerWithResolver(resolve func(context.Context, string) (string, error)) managedCertificateRenewalIssuer {
	if resolve == nil {
		return nil
	}
	directoryURL := strings.TrimSpace(os.Getenv("NRE_ACME_DIRECTORY_URL"))
	if directoryURL == "" {
		directoryURL = defaultMasterACMEDirectoryURL
	}

	dataDir := firstNonEmptyEnv("NRE_CONTROL_PLANE_DATA_DIR", "PANEL_DATA_ROOT")
	if dataDir == "" {
		dataDir = config.Default().DataDir
	}

	issuer := &masterCFDNSManagedCertificateIssuer{
		directoryURL: directoryURL,
		email:        strings.TrimSpace(os.Getenv("NRE_ACME_EMAIL")),
		cfZoneToken:  firstNonEmptyEnv("CLOUDFLARE_ZONE_API_TOKEN", "CF_ZONE_API_TOKEN"),
		dataDir:      dataDir,
		engine:       acmeflow.Engine{},
		openState: func(dataDir string) (masterACMEStateStore, error) {
			return openMasterACMEAccountStore(dataDir)
		},
		resolveToken: resolve,
		now:          time.Now,
	}
	issuer.newSolver = func(state masterACMEStateStore, dnsToken, zoneToken string) (masterACMESolver, error) {
		return newMasterCFDNSSolver(dnsToken, zoneToken, state)
	}
	return issuer
}

func (i *masterCFDNSManagedCertificateIssuer) Issue(ctx context.Context, cert ManagedCertificate) (managedCertificateRenewalResult, error) {
	return i.issue(ctx, cert)
}

func (i *masterCFDNSManagedCertificateIssuer) Renew(ctx context.Context, cert ManagedCertificate) (managedCertificateRenewalResult, error) {
	return i.issue(ctx, cert)
}

func (i *masterCFDNSManagedCertificateIssuer) issue(ctx context.Context, cert ManagedCertificate) (managedCertificateRenewalResult, error) {
	if ctx == nil {
		return managedCertificateRenewalResult{}, normalizeManagedCertificateACMEError("master_issue", acmeflow.CategoryProtocol, errors.New("context is nil"))
	}
	if err := ctx.Err(); err != nil {
		return managedCertificateRenewalResult{}, normalizeManagedCertificateACMEError("master_issue", acmeflow.CategoryProtocol, err)
	}

	domain := strings.TrimSpace(cert.Domain)
	if domain == "" {
		return managedCertificateRenewalResult{}, normalizeManagedCertificateACMEError("master_issue", acmeflow.CategoryProtocol, errors.New("managed certificate domain is empty"))
	}
	buildSolver, err := i.solverFor(ctx, domain)
	if err != nil {
		return managedCertificateRenewalResult{}, err
	}
	releaseAccount, err := acquireMasterACMEAccountLifecycle(ctx, i.dataDir, i.directoryURL, i.email)
	if err != nil {
		return managedCertificateRenewalResult{}, normalizeManagedCertificateACMEError("master_account_wait", acmeflow.CategoryAccount, err)
	}
	defer releaseAccount()

	openState := i.openState
	if openState == nil {
		openState = func(dataDir string) (masterACMEStateStore, error) {
			return openMasterACMEAccountStore(dataDir)
		}
	}
	state, err := openState(i.dataDir)
	if err != nil {
		return managedCertificateRenewalResult{}, normalizeManagedCertificateACMEError("master_state_open", acmeflow.CategoryAccount, err)
	}
	defer state.Close()
	if _, err := state.Reconcile(ctx); err != nil {
		return managedCertificateRenewalResult{}, normalizeManagedCertificateACMEError("master_state_reconcile", acmeflow.CategoryCleanup, err)
	}

	solver, err := buildSolver(state)
	if err != nil {
		return managedCertificateRenewalResult{}, normalizeManagedCertificateACMEError("master_solver_create", acmeflow.CategoryChallenge, err)
	}
	if recoverer, ok := solver.(interface{ RecoverPending(context.Context) error }); ok {
		if err := recoverer.RecoverPending(ctx); err != nil {
			return managedCertificateRenewalResult{}, normalizeManagedCertificateACMEError("master_solver_recover", acmeflow.CategoryCleanup, err)
		}
	}

	engine := i.engine
	if engine == nil {
		engine = acmeflow.Engine{}
	}
	result, err := engine.Issue(ctx, acmeflow.IssueRequest{
		DirectoryURL: i.directoryURL,
		Email:        i.email,
		Identifiers: []acmeflow.Identifier{{
			Type:  acmeflow.IdentifierDNS,
			Value: domain,
		}},
		ChallengeType: acmeflow.ChallengeDNS01,
		Solver:        solver,
		AccountStore:  state,
	})
	if err != nil {
		return managedCertificateRenewalResult{}, annotateDomesticDNSError(normalizeManagedCertificateACMEError("master_issue", acmeflow.CategoryProtocol, err), err)
	}

	leaf, err := parseManagedCertificateLeaf(result.CertificatePEM)
	if err != nil {
		return managedCertificateRenewalResult{}, normalizeManagedCertificateACMEError("master_certificate_parse", acmeflow.CategoryMaterial, err)
	}

	material := storage.ManagedCertificateBundle{
		Domain:  domain,
		CertPEM: strings.TrimSpace(string(result.CertificatePEM)),
		KeyPEM:  strings.TrimSpace(string(result.PrivateKeyPEM)),
	}
	issuedAt := time.Now().UTC()
	if i.now != nil {
		issuedAt = i.now().UTC()
	}
	return managedCertificateRenewalResult{
		Changed:      true,
		LastIssueAt:  issuedAt.Format(time.RFC3339),
		MaterialHash: hashManagedCertificateMaterial(material.CertPEM, material.KeyPEM),
		NotAfter:     leaf.NotAfter.UTC().Format(time.RFC3339),
		ACMEInfo: ManagedCertificateACMEInfo{
			MainDomain: domain,
			KeyLength:  managedCertificateKeyLength(leaf),
			SANDomains: strings.Join(leaf.DNSNames, ","),
			Profile:    result.Profile,
			CA:         strings.TrimSpace(leaf.Issuer.CommonName),
			Created:    issuedAt.Format(time.RFC3339),
			Renew:      leaf.NotAfter.Add(-30 * 24 * time.Hour).UTC().Format(time.RFC3339),
		},
		Material: material,
	}, nil
}

type masterSolverBuilder func(masterACMEStateStore) (masterACMESolver, error)

func (i *masterCFDNSManagedCertificateIssuer) solverFor(ctx context.Context, domain string) (masterSolverBuilder, error) {
	if i.records != nil {
		credential, err := i.records.ResolveCredential(ctx, domain)
		if err != nil {
			return nil, readableManagedDNSError(err)
		}
		if pluginhost.IsDomesticDNSProvider(credential.Provider) {
			if credential.Token != "" {
				return nil, errors.New("domestic DNS provider returned a token")
			}
			return func(masterACMEStateStore) (masterACMESolver, error) {
				propagation, propagationErr := i.challengePropagation()
				if propagationErr != nil {
					return nil, propagationErr
				}
				return newPluginDNS01Solver(pluginDNS01Config{
					Domain:      domain,
					Records:     i.records,
					Propagation: propagation,
					TTL:         cloudflare.DefaultRecordTTL,
				})
			}, nil
		}
		if !i.cloudflareEnabled {
			return nil, fmt.Errorf("%w: managed certificates require ACME_DNS_PROVIDER=cf and CF_Token", ErrInvalidArgument)
		}
		token := strings.TrimSpace(credential.Token)
		if token == "" {
			return nil, fmt.Errorf("Cloudflare domain %s has no available token", strings.TrimSpace(domain))
		}
		return func(state masterACMEStateStore) (masterACMESolver, error) {
			return i.cloudflareSolver(state, token)
		}, nil
	}
	token, err := i.tokenForDomain(ctx, domain)
	if err != nil {
		return nil, err
	}
	return func(state masterACMEStateStore) (masterACMESolver, error) {
		return i.cloudflareSolver(state, token)
	}, nil
}

func (i *masterCFDNSManagedCertificateIssuer) cloudflareSolver(state masterACMEStateStore, dnsToken string) (masterACMESolver, error) {
	zoneToken := strings.TrimSpace(i.cfZoneToken)
	if zoneToken == "" {
		zoneToken = dnsToken
	}
	newSolver := i.newSolver
	if newSolver == nil {
		newSolver = func(state masterACMEStateStore, dnsToken, zoneToken string) (masterACMESolver, error) {
			return newMasterCFDNSSolver(dnsToken, zoneToken, state)
		}
	}
	return newSolver(state, dnsToken, zoneToken)
}

func (i *masterCFDNSManagedCertificateIssuer) challengePropagation() (pluginDNSPropagation, error) {
	if i.propagation != nil {
		return i.propagation, nil
	}
	resolver, err := cloudflare.NewWireResolver(cloudflare.WireResolverConfig{})
	if err != nil {
		return nil, err
	}
	return cloudflare.NewPropagation(cloudflare.PropagationConfig{Resolver: resolver})
}

func readableManagedDNSError(err error) error {
	if err == nil {
		return nil
	}
	return domesticProviderError(err)
}

func annotateDomesticDNSError(normalized, cause error) error {
	var domestic *domesticDNSError
	if normalized == nil || !errors.As(cause, &domestic) || strings.TrimSpace(domestic.detail) == "" || strings.Contains(normalized.Error(), domestic.detail) {
		return normalized
	}
	return fmt.Errorf("%w: %s", normalized, domestic.detail)
}

func (i *masterCFDNSManagedCertificateIssuer) tokenForDomain(ctx context.Context, domain string) (string, error) {
	resolve := i.resolveToken
	if resolve == nil {
		dnsToken := firstNonEmptyEnv("CLOUDFLARE_DNS_API_TOKEN", "CF_DNS_API_TOKEN", "CF_TOKEN", "CF_Token")
		resolve = func(context.Context, string) (string, error) {
			if dnsToken == "" {
				return "", errors.New("Cloudflare DNS API token is unavailable")
			}
			return dnsToken, nil
		}
	}
	return resolve(ctx, domain)
}

func newMasterCFDNSSolver(token, zoneToken string, intents cloudflare.ChallengeIntentStore) (masterACMESolver, error) {
	client, err := cloudflare.NewClient(cloudflare.ClientConfig{
		DNSAPIToken:  token,
		ZoneAPIToken: zoneToken,
	})
	if err != nil {
		return nil, err
	}
	resolver, err := cloudflare.NewWireResolver(cloudflare.WireResolverConfig{})
	if err != nil {
		return nil, err
	}
	propagation, err := cloudflare.NewPropagation(cloudflare.PropagationConfig{Resolver: resolver})
	if err != nil {
		return nil, err
	}
	return cloudflare.NewDNS01Solver(cloudflare.DNS01Config{
		Client:      client,
		Propagation: propagation,
		Intents:     intents,
	})
}

func parseManagedCertificateLeaf(certPEM []byte) (*x509.Certificate, error) {
	for rest := certPEM; len(rest) > 0; {
		block, next := pem.Decode(rest)
		if block == nil {
			break
		}
		rest = next
		if block.Type != "CERTIFICATE" {
			continue
		}
		leaf, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, err
		}
		return leaf, nil
	}
	return nil, errors.New("issued certificate response did not contain a certificate")
}

func managedCertificateKeyLength(cert *x509.Certificate) string {
	if cert == nil || cert.PublicKey == nil {
		return ""
	}
	if key, ok := cert.PublicKey.(*rsa.PublicKey); ok {
		return fmt.Sprintf("rsa-%d", key.N.BitLen())
	}
	return strings.ToLower(cert.PublicKeyAlgorithm.String())
}

func firstNonEmptyEnv(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}
