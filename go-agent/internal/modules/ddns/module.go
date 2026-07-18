package ddns

import (
	"context"
	"log"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

const (
	// defaultMinExtractInterval bounds how often extraction may hit the network.
	// The runtime apply runs every heartbeat; this throttle prevents hammering
	// the public echo endpoints while still detecting IP rotation promptly.
	defaultMinExtractInterval = 5 * time.Minute
	// defaultIPv4/IPv6PublicAPIURL list independent public echo endpoints, tried
	// in order until one returns a valid address for the family. Three distinct
	// providers give default single-point resilience without any env override.
	// Each endpoint must return a bare IP (<=64-byte body); extract trims and
	// validates the family. Override per deployment via NRE_DDNS_*_PUBLIC_API_URL.
	defaultIPv4PublicAPIURL = "https://api.ipify.org,https://ipv4.icanhazip.com,https://v4.ident.me"
	defaultIPv6PublicAPIURL = "https://api6.ipify.org,https://ipv6.icanhazip.com,https://v6.ident.me"
)

// Config configures the DDNS extraction module. All fields are optional; zero
// values select production defaults (redundant echo endpoints, a bounded HTTP
// client, and a 5-minute extract throttle).
type Config struct {
	// Client is used for public_api probes. If nil, a client with a bounded
	// timeout is constructed. Tests inject an httptest-backed client.
	Client *http.Client
	// IPv4PublicAPIURL / IPv6PublicAPIURL override the default public echo
	// endpoints. Each may be a single URL or a comma-separated list (tried in
	// order; the first to return a valid IP wins) so a single hung upstream
	// can't black-hole extraction. Empty selects the redundant default endpoint
	// set. Set via the NRE_DDNS_IPV4_PUBLIC_API_URL /
	// NRE_DDNS_IPV6_PUBLIC_API_URL agent env vars.
	IPv4PublicAPIURL string
	IPv6PublicAPIURL string
	// MinExtractInterval bounds the public-probe cadence. Config changes always
	// force an immediate re-extract regardless of this interval.
	MinExtractInterval time.Duration
	// GenerationSelector resolves the reporter state from the atomically
	// published runtime generation. Leave nil for the legacy direct-Apply path.
	GenerationSelector interface{ ActiveGeneration() *module.GenerationView }
	// now is the clock used for throttle decisions; injected for deterministic
	// tests. Defaults to time.Now.
	now func() time.Time
}

// Module extracts the agent's IPv4/IPv6 addresses per the dispatched DDNSConfig
// and caches them for the heartbeat reporter. It mirrors the certs module
// shape (plain Module.Apply) — extraction is best-effort caching, not a
// provider/transaction, so no Prepare/Commit plumbing is required.
type Module struct {
	mu       sync.RWMutex
	cfg      Config
	state    ddnsState
	selector interface{ ActiveGeneration() *module.GenerationView }
}

type ddnsState struct {
	ipv4        string
	ipv6        string
	lastConfig  *model.DDNSExtractConfig
	lastExtract time.Time
}

const providerDDNSReporter module.ProviderRef = "ddns.reporter"

// NewModule constructs a DDNS extraction module, applying production defaults
// for any zero config field.
func NewModule(cfg Config) *Module {
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: defaultExtractTimeout}
	}
	if strings.TrimSpace(cfg.IPv4PublicAPIURL) == "" {
		cfg.IPv4PublicAPIURL = defaultIPv4PublicAPIURL
	}
	if strings.TrimSpace(cfg.IPv6PublicAPIURL) == "" {
		cfg.IPv6PublicAPIURL = defaultIPv6PublicAPIURL
	}
	if cfg.MinExtractInterval <= 0 {
		cfg.MinExtractInterval = defaultMinExtractInterval
	}
	if cfg.now == nil {
		cfg.now = time.Now
	}
	return &Module{cfg: cfg, selector: cfg.GenerationSelector}
}

func (m *Module) Name() string { return "ddns" }

func (m *Module) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{Name: m.Name(), Provides: []module.ProviderRef{providerDDNSReporter}}
}

func (m *Module) RegisterProviders(reg module.ProviderRegistry) error {
	return reg.Provide(providerDDNSReporter, m)
}

func (m *Module) Capabilities(_ module.SnapshotView) []module.Capability {
	return []module.Capability{{Name: "ddns_extract", Enabled: true}}
}

func (m *Module) Stop(_ context.Context) error { return nil }

// Apply receives the latest desired snapshot. It records the DDNS config and,
// subject to the minimum-interval throttle (or immediately when the config
// changed), re-extracts the v4/v6 addresses into the module cache consumed by
// the heartbeat reporter. Extraction failures are logged and cached as empty —
// Apply never returns an error, so DDNS can never fail the runtime apply chain
// or roll back an otherwise-healthy revision.
func (m *Module) Apply(ctx context.Context, req module.ApplyRequest) error {
	tx, err := m.Prepare(ctx, req)
	if err != nil || tx == nil {
		return err
	}
	return tx.Commit()
}

// Prepare builds an isolated DDNS reporter state. Generation publication reads
// the transaction through the active provider view, while the legacy Apply path
// commits the same state directly onto the module.
func (m *Module) Prepare(ctx context.Context, req module.ApplyRequest) (module.ModuleTransaction, error) {
	if m == nil {
		return nil, nil
	}
	previous := m.currentState()
	next := cloneDDNSState(previous)
	nextConfig := cloneDDNSConfig(req.Next.DDNSConfig)
	changed := !reflect.DeepEqual(previous.lastConfig, nextConfig)
	next.lastConfig = nextConfig

	extractAt := m.cfg.now()
	if changed || extractAt.Sub(previous.lastExtract) >= m.cfg.MinExtractInterval {
		next.ipv4, next.ipv6 = m.extract(ctx, nextConfig)
		next.lastExtract = extractAt
		if next.ipv4 == "" && next.ipv6 == "" && ddnsConfigHasEnabledFamily(nextConfig) {
			log.Printf("[agent] ddns extraction returned no addresses for domain %q", ddnsDomain(nextConfig))
		}
	}
	return &ddnsTransaction{module: m, previous: previous, next: next}, nil
}

// LastSeenIPs returns the extracted addresses for the heartbeat. When the
// configured interval has elapsed it refreshes the currently published
// generation in place, so address rotation does not depend on a new revision.
func (m *Module) LastSeenIPs(ctx context.Context) (string, string) {
	if m == nil {
		return "", ""
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if m.selector != nil {
		active := m.selector.ActiveGeneration()
		if active == nil {
			return "", ""
		}
		provider, _ := active.Resolve(providerDDNSReporter)
		reporter, _ := provider.(interface {
			LastSeenIPs(context.Context) (string, string)
		})
		if reporter == nil {
			return "", ""
		}
		return reporter.LastSeenIPs(ctx)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = m.refreshState(ctx, m.state)
	return m.state.ipv4, m.state.ipv6
}

type ddnsTransaction struct {
	mu        sync.Mutex
	module    *Module
	previous  ddnsState
	next      ddnsState
	published bool
}

func (t *ddnsTransaction) RegisterProviders(reg module.ProviderRegistry) error {
	return reg.Provide(providerDDNSReporter, t)
}

func (*ddnsTransaction) Ready(context.Context) error   { return nil }
func (*ddnsTransaction) Destroy(context.Context) error { return nil }

func (t *ddnsTransaction) Commit() error {
	if t == nil || t.module == nil || t.published {
		return nil
	}
	t.mu.Lock()
	next := cloneDDNSState(t.next)
	t.mu.Unlock()
	t.module.installState(next)
	t.published = true
	return nil
}

func (t *ddnsTransaction) Rollback() error {
	if t == nil || t.module == nil || !t.published {
		return nil
	}
	t.mu.Lock()
	previous := cloneDDNSState(t.previous)
	t.mu.Unlock()
	t.module.installState(previous)
	t.published = false
	return nil
}

func (t *ddnsTransaction) LastSeenIPs(ctx context.Context) (string, string) {
	if t == nil || t.module == nil {
		return "", ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.next = t.module.refreshState(ctx, t.next)
	return t.next.ipv4, t.next.ipv6
}

// extract runs both family extractions. The public_api source hits the network;
// callers serialize refreshes so concurrent heartbeats cannot duplicate probes.
// A nil config or a flipped-off master switch yields empty addresses, which
// also clears the cache so the next heartbeat stops reporting IPs.
func (m *Module) extract(ctx context.Context, cfg *model.DDNSExtractConfig) (string, string) {
	if cfg == nil || !cfg.Enabled {
		return "", ""
	}
	client := m.cfg.Client
	ipv4 := ExtractIPv4(ctx, cfg.IPv4, client, m.cfg.IPv4PublicAPIURL)
	ipv6 := ExtractIPv6(ctx, cfg.IPv6, client, m.cfg.IPv6PublicAPIURL)
	return ipv4, ipv6
}

func cloneDDNSConfig(cfg *model.DDNSExtractConfig) *model.DDNSExtractConfig {
	if cfg == nil {
		return nil
	}
	copy := *cfg
	return &copy
}

func cloneDDNSState(state ddnsState) ddnsState {
	state.lastConfig = cloneDDNSConfig(state.lastConfig)
	return state
}

func (m *Module) currentState() ddnsState {
	if m == nil {
		return ddnsState{}
	}
	if m.selector != nil {
		active := m.selector.ActiveGeneration()
		if active == nil {
			return ddnsState{}
		}
		provider, _ := active.Resolve(providerDDNSReporter)
		if tx, ok := provider.(*ddnsTransaction); ok && tx != nil {
			tx.mu.Lock()
			defer tx.mu.Unlock()
			return cloneDDNSState(tx.next)
		}
		return ddnsState{}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneDDNSState(m.state)
}

func (m *Module) refreshState(ctx context.Context, state ddnsState) ddnsState {
	extractAt := m.cfg.now()
	if extractAt.Sub(state.lastExtract) < m.cfg.MinExtractInterval {
		return state
	}
	state.ipv4, state.ipv6 = m.extract(ctx, state.lastConfig)
	state.lastExtract = extractAt
	if state.ipv4 == "" && state.ipv6 == "" && ddnsConfigHasEnabledFamily(state.lastConfig) {
		log.Printf("[agent] ddns extraction returned no addresses for domain %q", ddnsDomain(state.lastConfig))
	}
	return state
}

func (m *Module) installState(state ddnsState) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.state = cloneDDNSState(state)
	m.mu.Unlock()
}

func ddnsConfigHasEnabledFamily(cfg *model.DDNSExtractConfig) bool {
	if cfg == nil {
		return false
	}
	return cfg.IPv4.Enabled || cfg.IPv6.Enabled
}

func ddnsDomain(cfg *model.DDNSExtractConfig) string {
	if cfg == nil {
		return ""
	}
	return cfg.Domain
}

var _ module.TransactionalModule = (*Module)(nil)
var _ module.GenerationTransaction = (*ddnsTransaction)(nil)
