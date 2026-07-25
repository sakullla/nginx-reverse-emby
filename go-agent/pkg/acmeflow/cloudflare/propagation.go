package cloudflare

import (
	"context"
	"errors"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/acmeflow"
)

const (
	DefaultPropagationTimeout = 2 * time.Minute
	DefaultPollInterval       = 2 * time.Second
	MaxCNAMEHops              = 50
)

type DNSPropagation interface {
	ResolveCNAME(context.Context, string) (string, error)
	WaitTXT(context.Context, string, string, string) error
}

type PropagationConfig struct {
	Resolver         DNSQuerier
	RecursiveServers []string
	Timeout          time.Duration
	PollInterval     time.Duration
	Now              func() time.Time
	Wait             func(context.Context, time.Duration) error
	AuthorityAddress func(string) string
}

type Propagation struct {
	resolver         DNSQuerier
	recursiveServers []string
	timeout          time.Duration
	pollInterval     time.Duration
	now              func() time.Time
	wait             func(context.Context, time.Duration) error
	authorityAddress func(string) string
}

func NewPropagation(config PropagationConfig) (*Propagation, error) {
	if config.Resolver == nil {
		return nil, providerError(acmeflow.CategoryProtocol, "dns_propagation_config", errors.New("DNS resolver is required"))
	}
	servers := append([]string(nil), config.RecursiveServers...)
	if len(servers) == 0 {
		if source, ok := config.Resolver.(interface{ RecursiveServers() []string }); ok {
			servers = source.RecursiveServers()
		}
	}
	if len(servers) == 0 {
		return nil, providerError(acmeflow.CategoryProtocol, "dns_propagation_config", errors.New("recursive DNS server is required"))
	}
	for _, server := range servers {
		if strings.TrimSpace(server) == "" || len(server) > 1024 || strings.ContainsAny(server, "\r\n\x00") {
			return nil, providerError(acmeflow.CategoryProtocol, "dns_propagation_config", errors.New("recursive DNS server is invalid"))
		}
	}
	timeout := config.Timeout
	if timeout == 0 {
		timeout = DefaultPropagationTimeout
	}
	pollInterval := config.PollInterval
	if pollInterval == 0 {
		pollInterval = DefaultPollInterval
	}
	if timeout < 0 || timeout > time.Hour || pollInterval <= 0 || pollInterval > timeout {
		return nil, providerError(acmeflow.CategoryProtocol, "dns_propagation_config", errors.New("DNS propagation timing is invalid"))
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	wait := config.Wait
	if wait == nil {
		wait = waitForContext
	}
	authorityAddress := config.AuthorityAddress
	if authorityAddress == nil {
		authorityAddress = func(name string) string { return net.JoinHostPort(name, "53") }
	}
	return &Propagation{
		resolver:         config.Resolver,
		recursiveServers: servers,
		timeout:          timeout,
		pollInterval:     pollInterval,
		now:              now,
		wait:             wait,
		authorityAddress: authorityAddress,
	}, nil
}

func (propagation *Propagation) ResolveCNAME(ctx context.Context, name string) (string, error) {
	const operation = "dns_cname"
	if propagation == nil {
		return "", providerError(acmeflow.CategoryProtocol, operation, errors.New("DNS propagation resolver is nil"))
	}
	if err := contextFailure(ctx, operation); err != nil {
		return "", err
	}
	current, err := normalizeDNSName(name)
	if err != nil {
		return "", providerError(acmeflow.CategoryChallenge, operation, err)
	}
	visited := make(map[string]struct{}, MaxCNAMEHops+1)
	for hops := 0; ; hops++ {
		if _, exists := visited[current]; exists {
			return "", providerError(acmeflow.CategoryChallenge, operation, errors.New("DNS CNAME loop detected"))
		}
		visited[current] = struct{}{}
		message, err := propagation.queryRecursive(ctx, current, TypeCNAME)
		if err != nil {
			return "", err
		}
		targets := make(map[string]struct{})
		for _, record := range append(append([]DNSRecord(nil), message.Answers...), message.Authorities...) {
			if record.Type != TypeCNAME || record.Name != current {
				continue
			}
			target, normalizeErr := normalizeDNSName(record.Value)
			if normalizeErr != nil {
				return "", providerError(acmeflow.CategoryProtocol, operation, errDNSResponse)
			}
			targets[target] = struct{}{}
		}
		if len(targets) == 0 {
			return current, nil
		}
		if len(targets) != 1 {
			return "", providerError(acmeflow.CategoryChallenge, operation, errors.New("DNS CNAME answer is ambiguous"))
		}
		if hops >= MaxCNAMEHops {
			return "", providerError(acmeflow.CategoryChallenge, operation, errors.New("DNS CNAME chain is too long"))
		}
		for target := range targets {
			current = target
		}
	}
}

func (propagation *Propagation) DiscoverAuthority(ctx context.Context, name string) (string, []string, error) {
	const operation = "dns_authority"
	if propagation == nil {
		return "", nil, providerError(acmeflow.CategoryProtocol, operation, errors.New("DNS propagation resolver is nil"))
	}
	if err := contextFailure(ctx, operation); err != nil {
		return "", nil, err
	}
	name, err := normalizeDNSName(name)
	if err != nil {
		return "", nil, providerError(acmeflow.CategoryChallenge, operation, err)
	}
	labels := strings.Split(name, ".")
	var (
		zone string
		soa  *SOAData
	)
	for index := 0; index < len(labels); index++ {
		candidate := strings.Join(labels[index:], ".")
		message, queryErr := propagation.queryRecursive(ctx, candidate, TypeSOA)
		if queryErr != nil {
			if contextFailure(ctx, operation) != nil {
				return "", nil, contextFailure(ctx, operation)
			}
			continue
		}
		for _, record := range append(append([]DNSRecord(nil), message.Answers...), message.Authorities...) {
			if record.Type != TypeSOA || record.SOA == nil {
				continue
			}
			owner, normalizeErr := normalizeDNSName(record.Name)
			if normalizeErr != nil || owner != name && !strings.HasSuffix(name, "."+owner) {
				continue
			}
			if zone == "" || len(owner) > len(zone) {
				clone := *record.SOA
				zone = owner
				soa = &clone
			}
		}
		if zone != "" {
			break
		}
	}
	if zone == "" || soa == nil {
		return "", nil, providerError(acmeflow.CategoryChallenge, operation, errors.New("authoritative DNS zone was not found"))
	}
	message, err := propagation.queryRecursive(ctx, zone, TypeNS)
	if err != nil {
		return "", nil, err
	}
	names := make(map[string]struct{})
	for _, record := range append(append([]DNSRecord(nil), message.Answers...), message.Authorities...) {
		if record.Type != TypeNS || record.Name != zone {
			continue
		}
		serverName, normalizeErr := normalizeDNSName(record.Value)
		if normalizeErr != nil {
			return "", nil, providerError(acmeflow.CategoryProtocol, operation, errDNSResponse)
		}
		names[serverName] = struct{}{}
	}
	if len(names) == 0 && soa.MName != "" {
		serverName, normalizeErr := normalizeDNSName(soa.MName)
		if normalizeErr == nil {
			names[serverName] = struct{}{}
		}
	}
	if len(names) == 0 {
		return "", nil, providerError(acmeflow.CategoryChallenge, operation, errors.New("authoritative DNS server was not found"))
	}
	serverNames := make([]string, 0, len(names))
	for serverName := range names {
		serverNames = append(serverNames, serverName)
	}
	sort.Strings(serverNames)
	servers := make([]string, 0, len(serverNames))
	seenAddresses := make(map[string]struct{})
	for _, serverName := range serverNames {
		address := strings.TrimSpace(propagation.authorityAddress(serverName))
		if address == "" || len(address) > 1024 || strings.ContainsAny(address, "\r\n\x00") {
			return "", nil, providerError(acmeflow.CategoryProtocol, operation, errors.New("authoritative DNS server address is invalid"))
		}
		if _, exists := seenAddresses[address]; exists {
			continue
		}
		seenAddresses[address] = struct{}{}
		servers = append(servers, address)
	}
	return zone, servers, nil
}

func (propagation *Propagation) WaitTXT(ctx context.Context, name, value, zoneHint string) error {
	const operation = "dns_propagation"
	if propagation == nil {
		return providerError(acmeflow.CategoryProtocol, operation, errors.New("DNS propagation resolver is nil"))
	}
	if err := contextFailure(ctx, operation); err != nil {
		return err
	}
	name, err := normalizeDNSName(name)
	if err != nil || value == "" || len(value) > 4096 || strings.ContainsRune(value, '\x00') {
		return providerError(acmeflow.CategoryChallenge, operation, errors.New("DNS propagation target is invalid"))
	}
	zoneHint, err = normalizeDNSName(zoneHint)
	if err != nil {
		return providerError(acmeflow.CategoryChallenge, operation, err)
	}
	zone, servers, err := propagation.DiscoverAuthority(ctx, name)
	if err != nil {
		return err
	}
	if zone != zoneHint && !strings.HasSuffix(zone, "."+zoneHint) {
		return providerError(acmeflow.CategoryChallenge, operation, errors.New("authoritative DNS zone is outside the provider zone"))
	}
	deadline := propagation.now().Add(propagation.timeout)
	for {
		if err := contextFailure(ctx, operation); err != nil {
			return err
		}
		propagated := true
		for _, server := range servers {
			message, queryErr := propagation.resolver.Query(ctx, server, name, TypeTXT)
			if queryErr != nil {
				if err := contextFailure(ctx, operation); err != nil {
					return err
				}
				propagated = false
				continue
			}
			if !messageContainsTXT(message, name, value) {
				propagated = false
			}
		}
		if propagated {
			return nil
		}
		now := propagation.now()
		if !now.Before(deadline) {
			return providerError(acmeflow.CategoryTimeout, operation, context.DeadlineExceeded)
		}
		waitDuration := propagation.pollInterval
		if remaining := deadline.Sub(now); waitDuration > remaining {
			waitDuration = remaining
		}
		if err := propagation.wait(ctx, waitDuration); err != nil {
			return providerError("", operation, err)
		}
	}
}

func (propagation *Propagation) queryRecursive(ctx context.Context, name string, recordType RRType) (DNSMessage, error) {
	var lastError error
	for _, server := range propagation.recursiveServers {
		message, err := propagation.resolver.Query(ctx, server, name, recordType)
		if err == nil {
			return message, nil
		}
		lastError = err
		if ctx != nil && ctx.Err() != nil {
			return DNSMessage{}, providerError("", "dns_query", ctx.Err())
		}
	}
	if lastError == nil {
		lastError = errors.New("recursive DNS query failed")
	}
	return DNSMessage{}, providerError(acmeflow.CategoryNetwork, "dns_query", lastError)
}

func messageContainsTXT(message DNSMessage, name, value string) bool {
	for _, record := range message.Answers {
		if record.Type == TypeTXT && record.Name == name && record.Value == value {
			return true
		}
	}
	return false
}

func waitForContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
