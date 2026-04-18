package service

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/providers/dns/cloudflare"
	"github.com/go-acme/lego/v4/registration"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type masterCFDNSManagedCertificateIssuer struct {
	directoryURL string
	email        string
	cfToken      string
	cfZoneToken  string
}

func newMasterCFDNSManagedCertificateIssuer() managedCertificateRenewalIssuer {
	cfToken := firstNonEmptyCertificateEnv(
		"CLOUDFLARE_DNS_API_TOKEN",
		"CF_DNS_API_TOKEN",
		"CF_TOKEN",
		"CF_Token",
	)
	if strings.TrimSpace(cfToken) == "" {
		return nil
	}
	cfZoneToken := firstNonEmptyCertificateEnv("CLOUDFLARE_ZONE_API_TOKEN", "CF_ZONE_API_TOKEN")
	if strings.TrimSpace(cfZoneToken) == "" {
		cfZoneToken = cfToken
	}
	directoryURL := firstNonEmptyCertificateEnv("NRE_ACME_DIRECTORY_URL")
	if strings.TrimSpace(directoryURL) == "" {
		directoryURL = lego.LEDirectoryProduction
	}
	return &masterCFDNSManagedCertificateIssuer{
		directoryURL: strings.TrimSpace(directoryURL),
		email:        strings.TrimSpace(firstNonEmptyCertificateEnv("NRE_ACME_EMAIL")),
		cfToken:      strings.TrimSpace(cfToken),
		cfZoneToken:  strings.TrimSpace(cfZoneToken),
	}
}

func (i *masterCFDNSManagedCertificateIssuer) Issue(ctx context.Context, cert ManagedCertificate) (managedCertificateRenewalResult, error) {
	return i.issue(ctx, cert)
}

func (i *masterCFDNSManagedCertificateIssuer) Renew(ctx context.Context, cert ManagedCertificate) (managedCertificateRenewalResult, error) {
	return i.issue(ctx, cert)
}

func (i *masterCFDNSManagedCertificateIssuer) issue(ctx context.Context, cert ManagedCertificate) (managedCertificateRenewalResult, error) {
	if err := ctx.Err(); err != nil {
		return managedCertificateRenewalResult{}, err
	}
	accountKey, _, err := loadOrCreateManagedCertificateAccountKey(nil)
	if err != nil {
		return managedCertificateRenewalResult{}, err
	}
	user := &managedCertificateLegoUser{
		email:      i.email,
		privateKey: accountKey,
	}
	cfg := lego.NewConfig(user)
	cfg.CADirURL = i.directoryURL

	client, err := lego.NewClient(cfg)
	if err != nil {
		return managedCertificateRenewalResult{}, err
	}
	dnsConfig := cloudflare.NewDefaultConfig()
	dnsConfig.AuthToken = i.cfToken
	dnsConfig.ZoneToken = i.cfZoneToken
	provider, err := cloudflare.NewDNSProviderConfig(dnsConfig)
	if err != nil {
		return managedCertificateRenewalResult{}, err
	}
	if err := client.Challenge.SetDNS01Provider(provider); err != nil {
		return managedCertificateRenewalResult{}, err
	}
	registrationResource, err := client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
	if err != nil {
		return managedCertificateRenewalResult{}, err
	}
	user.registration = registrationResource
	obtained, err := client.Certificate.Obtain(certificate.ObtainRequest{
		Domains: []string{cert.Domain},
		Bundle:  true,
	})
	if err != nil {
		return managedCertificateRenewalResult{}, err
	}

	leaf, err := parseManagedCertificateLeaf(obtained.Certificate)
	if err != nil {
		return managedCertificateRenewalResult{}, err
	}
	certPEM := strings.TrimSpace(string(obtained.Certificate))
	keyPEM := strings.TrimSpace(string(obtained.PrivateKey))
	now := time.Now().UTC()
	return managedCertificateRenewalResult{
		Changed:      true,
		LastIssueAt:  now.Format(time.RFC3339),
		MaterialHash: hashManagedCertificateMaterial(certPEM, keyPEM),
		Material: storage.ManagedCertificateBundle{
			Domain:  cert.Domain,
			CertPEM: certPEM,
			KeyPEM:  keyPEM,
		},
		ACMEInfo: ManagedCertificateACMEInfo{
			MainDomain: cert.Domain,
			KeyLength:  managedCertificateKeyLength(leaf),
			SANDomains: strings.Join(leaf.DNSNames, ","),
			Profile:    "",
			CA:         strings.TrimSpace(leaf.Issuer.CommonName),
			Created:    now.Format(time.RFC3339),
			Renew:      leaf.NotAfter.Add(-30 * 24 * time.Hour).UTC().Format(time.RFC3339),
		},
	}, nil
}

type managedCertificateLegoUser struct {
	email        string
	registration *registration.Resource
	privateKey   crypto.PrivateKey
}

func (u *managedCertificateLegoUser) GetEmail() string {
	return u.email
}

func (u *managedCertificateLegoUser) GetRegistration() *registration.Resource {
	return u.registration
}

func (u *managedCertificateLegoUser) GetPrivateKey() crypto.PrivateKey {
	return u.privateKey
}

func loadOrCreateManagedCertificateAccountKey(existingPEM []byte) (crypto.PrivateKey, []byte, error) {
	if len(existingPEM) > 0 {
		privateKey, err := certcrypto.ParsePEMPrivateKey(existingPEM)
		if err != nil {
			return nil, nil, err
		}
		return privateKey, existingPEM, nil
	}
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	return privateKey, certcrypto.PEMEncode(privateKey), nil
}

func parseManagedCertificateLeaf(raw []byte) (*x509.Certificate, error) {
	rest := raw
	for {
		rest = []byte(strings.TrimSpace(string(rest)))
		if len(rest) == 0 {
			break
		}
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
	return nil, fmt.Errorf("invalid certificate PEM")
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

func firstNonEmptyCertificateEnv(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}
