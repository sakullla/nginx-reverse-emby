package service

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/acmeflow"
	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/acmeflow/cloudflare"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/sanitize"
)

type pluginDNSPropagation interface {
	ResolveCNAME(context.Context, string) (string, error)
	WaitTXT(context.Context, string, string, string) error
}

type pluginDNS01Config struct {
	Domain      string
	Records     managedDNSRecordProvider
	Propagation pluginDNSPropagation
	TTL         int
}

type pluginDNS01Solver struct {
	domain      string
	records     managedDNSRecordProvider
	propagation pluginDNSPropagation
	ttl         int

	mu       sync.Mutex
	sessions map[string]pluginDNS01Session
}

type pluginDNS01Session struct {
	name string
	zone string
}

type domesticDNSError struct {
	detail string
}

func (e *domesticDNSError) Error() string {
	if e == nil || e.detail == "" {
		return "DNS provider record operation failed"
	}
	return e.detail
}

func newPluginDNS01Solver(config pluginDNS01Config) (*pluginDNS01Solver, error) {
	domain, err := normalizePluginDNSDomain(config.Domain)
	if err != nil {
		return nil, err
	}
	if config.Records == nil {
		return nil, &domesticDNSError{detail: "DNS provider record client is unavailable"}
	}
	ttl := config.TTL
	if ttl < 1 {
		ttl = cloudflare.DefaultRecordTTL
	}
	return &pluginDNS01Solver{
		domain:      domain,
		records:     config.Records,
		propagation: config.Propagation,
		ttl:         ttl,
		sessions:    make(map[string]pluginDNS01Session),
	}, nil
}

func (s *pluginDNS01Solver) ChallengeType() string { return acmeflow.ChallengeDNS01 }

func (s *pluginDNS01Solver) Present(ctx context.Context, challenge acmeflow.Challenge) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	name, err := pluginDNSChallengeName(challenge)
	if err != nil {
		return err
	}
	if s.propagation != nil {
		target, resolveErr := s.propagation.ResolveCNAME(ctx, name)
		if resolveErr != nil {
			return domesticProviderError(resolveErr)
		}
		if trimmed := strings.TrimSpace(target); trimmed != "" {
			name = trimmed
		}
	}
	if err := s.records.EnsureRecord(ctx, s.domain, "TXT", name, challenge.DNSValue, s.ttl); err != nil {
		return domesticProviderError(err)
	}
	s.mu.Lock()
	s.sessions[pluginDNSChallengeKey(challenge)] = pluginDNS01Session{name: name, zone: s.domain}
	s.mu.Unlock()
	return nil
}

func (s *pluginDNS01Solver) Wait(ctx context.Context, challenge acmeflow.Challenge) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.propagation == nil {
		return &domesticDNSError{detail: "DNS propagation resolver is unavailable"}
	}
	session, ok := s.session(challenge)
	if !ok {
		return &domesticDNSError{detail: "DNS challenge was not presented"}
	}
	zone := session.zone
	if discoverer, ok := s.propagation.(interface {
		DiscoverAuthority(context.Context, string) (string, []string, error)
	}); ok {
		discovered, _, err := discoverer.DiscoverAuthority(ctx, session.name)
		if err != nil {
			return domesticProviderError(err)
		}
		if strings.TrimSpace(discovered) != "" {
			zone = discovered
		}
	}
	if err := s.propagation.WaitTXT(ctx, session.name, challenge.DNSValue, zone); err != nil {
		return domesticProviderError(err)
	}
	return nil
}

func (s *pluginDNS01Solver) Cleanup(ctx context.Context, challenge acmeflow.Challenge) error {
	session, ok := s.session(challenge)
	if !ok {
		return nil
	}
	if err := s.records.DeleteRecord(ctx, s.domain, "TXT", session.name, challenge.DNSValue); err != nil {
		return domesticProviderError(err)
	}
	s.mu.Lock()
	delete(s.sessions, pluginDNSChallengeKey(challenge))
	s.mu.Unlock()
	return nil
}

func (s *pluginDNS01Solver) session(challenge acmeflow.Challenge) (pluginDNS01Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[pluginDNSChallengeKey(challenge)]
	return session, ok
}

func pluginDNSChallengeName(challenge acmeflow.Challenge) (string, error) {
	if challenge.Type != acmeflow.ChallengeDNS01 || challenge.Identifier.Type != acmeflow.IdentifierDNS || strings.TrimSpace(challenge.DNSValue) == "" {
		return "", &domesticDNSError{detail: "DNS-01 challenge is invalid"}
	}
	domain, err := normalizePluginDNSDomain(challenge.Identifier.Value)
	if err != nil {
		return "", err
	}
	return "_acme-challenge." + domain, nil
}

func pluginDNSChallengeKey(challenge acmeflow.Challenge) string {
	return challenge.Identifier.Value + "\x00" + challenge.DNSValue
}

func normalizePluginDNSDomain(domain string) (string, error) {
	domain = strings.ToLower(strings.TrimRight(strings.TrimSpace(domain), "."))
	domain = strings.TrimPrefix(domain, "*.")
	if domain == "" || len(domain) > 253 || strings.ContainsAny(domain, " \t\r\n\x00/") {
		return "", &domesticDNSError{detail: "DNS challenge domain is invalid"}
	}
	return domain, nil
}

func domesticProviderError(err error) error {
	if err == nil {
		return nil
	}
	var domestic *domesticDNSError
	if errors.As(err, &domestic) {
		return domestic
	}
	detail := strings.TrimSpace(sanitize.Text(err.Error(), nil))
	if len(detail) > 300 {
		detail = detail[:300]
	}
	if detail == "" {
		detail = "DNS provider record operation failed"
	}
	return &domesticDNSError{detail: detail}
}
