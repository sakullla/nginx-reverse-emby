package service

import (
	"context"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestRuleServiceListPageFilterPaginationAndAgentName(t *testing.T) {
	t.Parallel()
	store := &fakeRuleStore{
		agents: []storage.AgentRow{
			{ID: "edge", Name: "Edge Node"},
			{ID: "local", Name: "Local Node"},
		},
		rulesByAgent: map[string][]storage.HTTPRuleRow{
			"local": {
				{ID: 1, AgentID: "local", FrontendURL: "https://local.example.com", Enabled: true},
				{ID: 2, AgentID: "local", FrontendURL: "https://other.example.com", Enabled: true},
			},
			"edge": {
				{ID: 3, AgentID: "edge", FrontendURL: "https://edge.example.com", Enabled: true},
			},
		},
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
		LocalAgentName:   "Local Node",
	}, store)

	page, meta, err := svc.ListPage(context.Background(), ListQuery{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("ListPage all error = %v", err)
	}
	if meta.Total != 3 {
		t.Fatalf("all total = %d, want 3", meta.Total)
	}
	if len(page) != 3 {
		t.Fatalf("all page len = %d", len(page))
	}
	for _, rule := range page {
		if rule.AgentID == "" {
			t.Fatalf("missing agent_id on rule %#v", rule)
		}
		if rule.AgentName == "" {
			t.Fatalf("missing agent_name on rule %#v", rule)
		}
	}

	page, meta, err = svc.ListPage(context.Background(), ListQuery{AgentID: "edge", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("ListPage edge error = %v", err)
	}
	if meta.Total != 1 || len(page) != 1 || page[0].ID != 3 {
		t.Fatalf("edge page=%v meta=%+v", page, meta)
	}
	if page[0].AgentName != "Edge Node" {
		t.Fatalf("agent_name = %q", page[0].AgentName)
	}

	page, meta, err = svc.ListPage(context.Background(), ListQuery{Page: 2, PageSize: 2})
	if err != nil {
		t.Fatalf("ListPage page2 error = %v", err)
	}
	if meta.Total != 3 || meta.Page != 2 || meta.PageSize != 2 || len(page) != 1 {
		t.Fatalf("page2 page=%v meta=%+v", page, meta)
	}

	page, meta, err = svc.ListPage(context.Background(), ListQuery{Q: "edge.example", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("ListPage q error = %v", err)
	}
	if meta.Total != 1 || len(page) != 1 || page[0].ID != 3 {
		t.Fatalf("q page=%v meta=%+v", page, meta)
	}

	_, _, err = svc.ListPage(context.Background(), ListQuery{AgentID: "missing"})
	if err != ErrAgentNotFound {
		t.Fatalf("missing agent error = %v, want ErrAgentNotFound", err)
	}

	page, meta, err = svc.ListPage(context.Background(), ListQuery{AgentID: "edge", Page: 1, PageSize: 20, Q: "nomatch"})
	if err != nil {
		t.Fatalf("empty q error = %v", err)
	}
	if meta.Total != 0 || len(page) != 0 {
		t.Fatalf("empty result page=%v meta=%+v", page, meta)
	}

	// page_size upper clamp
	_, meta, err = svc.ListPage(context.Background(), ListQuery{Page: 1, PageSize: 500})
	if err != nil {
		t.Fatalf("clamp error = %v", err)
	}
	if meta.PageSize != MaxListPageSize {
		t.Fatalf("page_size = %d, want %d", meta.PageSize, MaxListPageSize)
	}
}

func TestRuleServiceListPageEnabledFilterBeforePagination(t *testing.T) {
	t.Parallel()
	store := &fakeRuleStore{
		agents: []storage.AgentRow{
			{ID: "edge", Name: "Edge Node"},
			{ID: "local", Name: "Local Node"},
		},
		rulesByAgent: map[string][]storage.HTTPRuleRow{
			"local": {
				{ID: 1, AgentID: "local", FrontendURL: "https://local-on.example.com", Enabled: true},
				{ID: 2, AgentID: "local", FrontendURL: "https://local-off.example.com", Enabled: false},
			},
			"edge": {
				{ID: 3, AgentID: "edge", FrontendURL: "https://edge-on.example.com", Enabled: true},
				{ID: 4, AgentID: "edge", FrontendURL: "https://edge-off.example.com", Enabled: false},
			},
		},
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
		LocalAgentName:   "Local Node",
	}, store)

	enabled := true
	page, meta, err := svc.ListPage(context.Background(), ListQuery{Enabled: &enabled, Page: 1, PageSize: 1})
	if err != nil {
		t.Fatalf("enabled ListPage error = %v", err)
	}
	if meta.Total != 2 {
		t.Fatalf("enabled total = %d, want 2", meta.Total)
	}
	if len(page) != 1 || !page[0].Enabled {
		t.Fatalf("enabled page = %#v", page)
	}

	disabled := false
	page, meta, err = svc.ListPage(context.Background(), ListQuery{Enabled: &disabled, Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("disabled ListPage error = %v", err)
	}
	if meta.Total != 2 || len(page) != 2 {
		t.Fatalf("disabled page=%v meta=%+v", page, meta)
	}
	for _, rule := range page {
		if rule.Enabled {
			t.Fatalf("expected disabled only, got %#v", rule)
		}
	}

	// omit enabled: no filter, total remains 4
	_, meta, err = svc.ListPage(context.Background(), ListQuery{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("omit enabled error = %v", err)
	}
	if meta.Total != 4 {
		t.Fatalf("omit enabled total = %d, want 4", meta.Total)
	}
}

func TestL4ServiceListPageAcrossAgents(t *testing.T) {
	t.Parallel()
	store := &fakeL4Store{
		agents: []storage.AgentRow{
			{ID: "edge", Name: "Edge", CapabilitiesJSON: `["l4"]`},
		},
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {
				{ID: 1, AgentID: "local", Name: "local-tcp", Protocol: "tcp", ListenHost: "0.0.0.0", ListenPort: 1001, Enabled: true},
			},
			"edge": {
				{ID: 2, AgentID: "edge", Name: "edge-tcp", Protocol: "tcp", ListenHost: "0.0.0.0", ListenPort: 2002, Enabled: true},
			},
		},
	}
	svc := NewL4RuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
		LocalAgentName:   "Local",
	}, store)

	page, meta, err := svc.ListPage(context.Background(), ListQuery{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("ListPage error = %v", err)
	}
	if meta.Total != 2 || len(page) != 2 {
		t.Fatalf("page=%v meta=%+v", page, meta)
	}
	for _, rule := range page {
		if rule.AgentID == "" || rule.AgentName == "" {
			t.Fatalf("missing ownership fields: %#v", rule)
		}
	}

	page, meta, err = svc.ListPage(context.Background(), ListQuery{AgentID: "edge", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("agent filter error = %v", err)
	}
	if meta.Total != 1 || page[0].ID != 2 || page[0].AgentName != "Edge" {
		t.Fatalf("edge page=%v meta=%+v", page, meta)
	}
}

func TestRelayServiceListPage(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		agents: []storage.AgentRow{
			{ID: "edge", Name: "Edge"},
		},
		relayByAgentID: map[string][]storage.RelayListenerRow{
			"local": {
				{ID: 1, AgentID: "local", Name: "relay-local", ListenHost: "0.0.0.0", ListenPort: 7443, Enabled: true},
			},
			"edge": {
				{ID: 2, AgentID: "edge", Name: "relay-edge", PublicHost: "edge.example.com", ListenPort: 8443, Enabled: true},
			},
		},
	}
	svc := NewRelayListenerService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
		LocalAgentName:   "Local",
	}, store)

	page, meta, err := svc.ListPage(context.Background(), ListQuery{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("ListPage error = %v", err)
	}
	if meta.Total != 2 {
		t.Fatalf("total = %d", meta.Total)
	}
	page, meta, err = svc.ListPage(context.Background(), ListQuery{AgentID: "edge", Q: "edge.example", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("filter error = %v", err)
	}
	if meta.Total != 1 || len(page) != 1 || page[0].ID != 2 || page[0].AgentName != "Edge" {
		t.Fatalf("page=%v meta=%+v", page, meta)
	}
}

func TestCertificateServiceListPageAgentFilter(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		agents: []storage.AgentRow{
			{ID: "edge", Name: "Edge"},
		},
		managedCerts: []storage.ManagedCertificateRow{
			{ID: 1, Domain: "a.example.com", TargetAgentIDs: `["local"]`, Enabled: true, Status: "active"},
			{ID: 2, Domain: "b.example.com", TargetAgentIDs: `["edge"]`, Enabled: true, Status: "active"},
			{ID: 3, Domain: "c.example.com", TargetAgentIDs: `["local","edge"]`, Enabled: true, Status: "pending"},
		},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
		LocalAgentName:   "Local",
	}, store)

	page, meta, err := svc.ListPage(context.Background(), ListQuery{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("all error = %v", err)
	}
	if meta.Total != 3 {
		t.Fatalf("all total = %d", meta.Total)
	}

	page, meta, err = svc.ListPage(context.Background(), ListQuery{AgentID: "edge", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("edge error = %v", err)
	}
	if meta.Total != 2 {
		t.Fatalf("edge total = %d, want 2", meta.Total)
	}
	for _, cert := range page {
		if cert.AgentID != "edge" {
			t.Fatalf("agent_id = %q", cert.AgentID)
		}
		if cert.AgentName != "Edge" {
			t.Fatalf("agent_name = %q", cert.AgentName)
		}
	}

	page, meta, err = svc.ListPage(context.Background(), ListQuery{Q: "b.example", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("q error = %v", err)
	}
	if meta.Total != 1 || page[0].ID != 2 {
		t.Fatalf("q page=%v meta=%+v", page, meta)
	}
}

func TestCertificateServiceListPageEnabledAndStatusFilters(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		agents: []storage.AgentRow{
			{ID: "edge", Name: "Edge"},
		},
		managedCerts: []storage.ManagedCertificateRow{
			{ID: 1, Domain: "a.example.com", TargetAgentIDs: `["local"]`, Enabled: true, Status: "active"},
			{ID: 2, Domain: "b.example.com", TargetAgentIDs: `["edge"]`, Enabled: false, Status: "active"},
			{ID: 3, Domain: "c.example.com", TargetAgentIDs: `["local","edge"]`, Enabled: true, Status: "pending"},
			{ID: 4, Domain: "d.example.com", TargetAgentIDs: `["edge"]`, Enabled: true, Status: "issuing"},
		},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
		LocalAgentName:   "Local",
	}, store)

	enabled := true
	page, meta, err := svc.ListPage(context.Background(), ListQuery{Enabled: &enabled, Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("enabled error = %v", err)
	}
	if meta.Total != 3 {
		t.Fatalf("enabled total = %d, want 3", meta.Total)
	}
	for _, cert := range page {
		if !cert.Enabled {
			t.Fatalf("expected enabled only: %#v", cert)
		}
	}

	page, meta, err = svc.ListPage(context.Background(), ListQuery{Status: "pending", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("status error = %v", err)
	}
	if meta.Total != 1 || len(page) != 1 || page[0].ID != 3 {
		t.Fatalf("status page=%v meta=%+v", page, meta)
	}

	// combined filters + pagination total consistency
	page, meta, err = svc.ListPage(context.Background(), ListQuery{
		Enabled:  &enabled,
		Status:   "active",
		Page:     1,
		PageSize: 1,
	})
	if err != nil {
		t.Fatalf("combined error = %v", err)
	}
	if meta.Total != 1 || len(page) != 1 || page[0].ID != 1 {
		t.Fatalf("combined page=%v meta=%+v", page, meta)
	}
}

func TestWireGuardProfileServiceListPage(t *testing.T) {
	t.Parallel()
	store := &fakeL4Store{
		agents: []storage.AgentRow{
			{ID: "edge", Name: "Edge", CapabilitiesJSON: `["wireguard"]`},
		},
		wireGuardByAgent: map[string][]storage.WireGuardProfileRow{
			"local": {
				{ID: 1, AgentID: "local", Name: "wg-local", Enabled: true},
			},
			"edge": {
				{ID: 2, AgentID: "edge", Name: "wg-edge", PublicEndpoint: "edge.example.com:51820", Enabled: true},
			},
		},
	}
	svc := NewWireGuardProfileService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
		LocalAgentName:   "Local",
	}, store)
	page, meta, err := svc.ListPage(context.Background(), ListQuery{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("ListPage error = %v", err)
	}
	if meta.Total != 2 {
		t.Fatalf("total = %d", meta.Total)
	}
	for _, profile := range page {
		if profile.AgentID == "" || profile.AgentName == "" {
			t.Fatalf("missing ownership fields: %#v", profile)
		}
	}
	page, meta, err = svc.ListPage(context.Background(), ListQuery{AgentID: "edge", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("edge error = %v", err)
	}
	if meta.Total != 1 || page[0].ID != 2 || page[0].AgentName != "Edge" {
		t.Fatalf("page=%v meta=%+v", page, meta)
	}
}
