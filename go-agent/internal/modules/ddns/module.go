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
	// now is the clock used for throttle decisions; injected for deterministic
	// tests. Defaults to time.Now.
	now func() time.Time
}

// Module extracts the agent's IPv4/IPv6 addresses per the dispatched DDNSConfig
// and caches them for the heartbeat reporter. It mirrors the certs module
// shape (plain Module.Apply) — extraction is best-effort caching, not a
// provider/transaction, so no Prepare/Commit plumbing is required.
type Module struct {
	mu          sync.RWMutex
	cfg         Config
	ipv4        string
	ipv6        string
	lastConfig  *model.DDNSExtractConfig
	lastExtract time.Time
}

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
	return &Module{cfg: cfg}
}

func (m *Module) Name() string { return "ddns" }

func (m *Module) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{Name: m.Name()}
}

func (m *Module) RegisterProviders(_ module.ProviderRegistry) error { return nil }

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
	if m == nil {
		return nil
	}
	next := req.Next.DDNSConfig

	m.mu.Lock()
	changed := !reflect.DeepEqual(m.lastConfig, next)
	m.lastConfig = cloneDDNSConfig(next)
	shouldExtract := changed || m.cfg.now().Sub(m.lastExtract) >= m.cfg.MinExtractInterval
	m.mu.Unlock()

	if !shouldExtract {
		return nil
	}

	extractAt := m.cfg.now()
	ipv4, ipv6 := m.extract(ctx, next)
	m.mu.Lock()
	m.ipv4 = ipv4
	m.ipv6 = ipv6
	m.lastExtract = extractAt
	m.mu.Unlock()
	if ipv4 == "" && ipv6 == "" && ddnsConfigHasEnabledFamily(next) {
		log.Printf("[agent] ddns extraction returned no addresses for domain %q", ddnsDomain(next))
	}
	return nil
}

// LastSeenIPs returns the cached extracted addresses for the heartbeat. It is
// a non-blocking cache read; extraction happens on the Apply cadence.
func (m *Module) LastSeenIPs(_ context.Context) (string, string) {
	if m == nil {
		return "", ""
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.ipv4, m.ipv6
}

// extract runs both family extractions. The public_api source hits the network;
// it runs outside the module lock so a slow upstream does not block readers.
func (m *Module) extract(ctx context.Context, cfg *model.DDNSExtractConfig) (string, string) {
	if cfg == nil {
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
