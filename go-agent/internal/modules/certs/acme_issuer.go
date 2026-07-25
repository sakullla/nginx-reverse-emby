package certs

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/acmeflow"
	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/acmeflow/cloudflare"
)

type acmeflowIssueEngine interface {
	Issue(context.Context, acmeflow.IssueRequest) (acmeflow.IssueResult, error)
}

type acmeflowACMEIssuer struct {
	engine        acmeflowIssueEngine
	solverFactory func(acmeIssueRequest) (acmeflow.ChallengeSolver, error)
}

func defaultACMEIssuerFactory(acmeIssueRequest) (acmeIssuer, error) {
	return acmeflowACMEIssuer{}, nil
}

func (issuer acmeflowACMEIssuer) Issue(ctx context.Context, request acmeIssueRequest) (acmeIssueResult, error) {
	var result acmeIssueResult
	if ctx == nil {
		return result, acmeflow.WrapError(acmeflow.CategoryProtocol, "agent_issue", errors.New("context is nil"))
	}
	if err := ctx.Err(); err != nil {
		return result, acmeflow.WrapError(acmeflow.CategoryCancelled, "agent_issue", err)
	}

	solverFactory := issuer.solverFactory
	if solverFactory == nil {
		solverFactory = newACMEChallengeSolver
	}
	solver, err := solverFactory(request)
	if err != nil {
		return result, err
	}
	if recoverer, ok := solver.(interface{ RecoverPending(context.Context) error }); ok {
		if err := recoverer.RecoverPending(ctx); err != nil {
			return result, err
		}
	}

	identifierType := acmeflow.IdentifierDNS
	if strings.EqualFold(strings.TrimSpace(request.Scope), "ip") || net.ParseIP(strings.TrimSpace(request.Domain)) != nil {
		identifierType = acmeflow.IdentifierIP
	}
	engine := issuer.engine
	if engine == nil {
		engine = acmeflow.Engine{}
	}
	issued, err := engine.Issue(ctx, acmeflow.IssueRequest{
		DirectoryURL:   request.DirectoryURL,
		Email:          request.Email,
		Identifiers:    []acmeflow.Identifier{{Type: identifierType, Value: request.Domain}},
		Profile:        request.Profile,
		ChallengeType:  solver.ChallengeType(),
		Solver:         solver,
		AccountStore:   request.AccountStore,
		ExistingKeyPEM: append([]byte(nil), request.ExistingKeyPEM...),
	})
	result.AccountKeyPEM = append([]byte(nil), issued.AccountKeyPEM...)
	result.Account = issued.Account
	if err != nil {
		return result, err
	}
	result.CertPEM = append([]byte(nil), issued.CertificatePEM...)
	result.KeyPEM = append([]byte(nil), issued.PrivateKeyPEM...)
	return result, nil
}

func newACMEChallengeSolver(request acmeIssueRequest) (acmeflow.ChallengeSolver, error) {
	switch request.ChallengeType {
	case challengeTypeHTTP01:
		return acmeflow.NewHTTP01Solver(request.HTTP01Interface, request.HTTP01Port), nil
	case challengeTypeDNS01Cloudflare:
		intents, ok := request.AccountStore.(cloudflare.ChallengeIntentStore)
		if !ok {
			return nil, acmeflow.WrapError(acmeflow.CategoryProtocol, "agent_dns01", errors.New("challenge intent store is unavailable"))
		}
		client, err := cloudflare.NewClient(cloudflare.ClientConfig{
			DNSAPIToken:  request.CloudflareDNSAPIToken,
			ZoneAPIToken: firstNonEmpty(request.CloudflareZoneAPIToken, request.CloudflareDNSAPIToken),
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
	default:
		return nil, acmeflow.WrapError(acmeflow.CategoryChallenge, "agent_solver", fmt.Errorf("unsupported challenge type %q", request.ChallengeType))
	}
}
