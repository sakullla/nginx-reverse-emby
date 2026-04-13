package certs

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"fmt"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/challenge/http01"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/providers/dns/cloudflare"
	"github.com/go-acme/lego/v4/registration"
)

func defaultACMEIssuerFactory(request acmeIssueRequest) (acmeIssuer, error) {
	return legoACMEIssuer{}, nil
}

type legoACMEIssuer struct{}

func (legoACMEIssuer) Issue(ctx context.Context, request acmeIssueRequest) (acmeIssueResult, error) {
	result := acmeIssueResult{}
	if err := ctx.Err(); err != nil {
		return result, err
	}

	accountKey, accountKeyPEM, err := loadOrCreateACMEAccountKey(request.AccountKeyPEM)
	if err != nil {
		return result, err
	}
	result.AccountKeyPEM = accountKeyPEM
	if err := ctx.Err(); err != nil {
		return result, err
	}

	user := &legoUser{
		email:        request.Email,
		registration: request.Registration,
		privateKey:   accountKey,
	}

	config := lego.NewConfig(user)
	if request.DirectoryURL != "" {
		config.CADirURL = request.DirectoryURL
	}

	client, err := lego.NewClient(config)
	if err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}

	switch request.ChallengeType {
	case challengeTypeHTTP01:
		if err := client.Challenge.SetHTTP01Provider(http01.NewProviderServer(request.HTTP01Interface, request.HTTP01Port)); err != nil {
			return result, err
		}
	case challengeTypeDNS01Cloudflare:
		dnsConfig := cloudflare.NewDefaultConfig()
		dnsConfig.AuthToken = request.CloudflareDNSAPIToken
		dnsConfig.ZoneToken = firstNonEmpty(request.CloudflareZoneAPIToken, request.CloudflareDNSAPIToken)
		provider, err := cloudflare.NewDNSProviderConfig(dnsConfig)
		if err != nil {
			return result, err
		}
		if err := client.Challenge.SetDNS01Provider(provider); err != nil {
			return result, err
		}
	default:
		return result, fmt.Errorf("unsupported acme challenge type %q", request.ChallengeType)
	}

	if user.registration == nil || user.registration.URI == "" {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		registrationResource, err := client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
		if err != nil {
			return result, err
		}
		user.registration = registrationResource
		result.Registration = registrationResource
	} else {
		result.Registration = user.registration
	}

	existingKey, err := parseOptionalPrivateKey(request.ExistingKeyPEM)
	if err != nil {
		return result, err
	}

	if err := ctx.Err(); err != nil {
		return result, err
	}
	resource, err := client.Certificate.Obtain(certificate.ObtainRequest{
		Domains:    []string{request.Domain},
		PrivateKey: existingKey,
		Bundle:     true,
		Profile:    request.Profile,
	})
	if err != nil {
		return result, err
	}

	result.CertPEM = resource.Certificate
	result.KeyPEM = resource.PrivateKey
	return result, nil
}

type legoUser struct {
	email        string
	registration *registration.Resource
	privateKey   crypto.PrivateKey
}

func (u *legoUser) GetEmail() string {
	return u.email
}

func (u *legoUser) GetRegistration() *registration.Resource {
	return u.registration
}

func (u *legoUser) GetPrivateKey() crypto.PrivateKey {
	return u.privateKey
}

func loadOrCreateACMEAccountKey(existingPEM []byte) (crypto.PrivateKey, []byte, error) {
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

func parseOptionalPrivateKey(keyPEM []byte) (crypto.PrivateKey, error) {
	if len(keyPEM) == 0 {
		return nil, nil
	}
	return certcrypto.ParsePEMPrivateKey(keyPEM)
}
