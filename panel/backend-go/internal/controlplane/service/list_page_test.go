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
				{ID: 1, AgentID: "local", FrontendURL: "https://local.example.com", BackendsJSON: `[{"url":"http://upstream.internal:8096/emby"}]`, Enabled: true},
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

	page, meta, err = svc.ListPage(context.Background(), ListQuery{Q: "upstream.internal:8096", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("backend q error = %v", err)
	}
	if meta.Total != 1 || len(page) != 1 || page[0].ID != 1 {
		t.Fatalf("backend q page=%v meta=%+v", page, meta)
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
				{ID: 2, AgentID: "edge", Name: "edge-tcp", Protocol: "tcp", ListenHost: "0.0.0.0", ListenPort: 2002, BackendsJSON: `[{"host":"upstream.internal","port":9000}]`, Enabled: true},
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

	page, meta, err = svc.ListPage(context.Background(), ListQuery{Q: "upstream.internal:9000", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("backend q error = %v", err)
	}
	if meta.Total != 1 || len(page) != 1 || page[0].ID != 2 {
		t.Fatalf("backend q page=%v meta=%+v", page, meta)
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
				{ID: 2, AgentID: "edge", Name: "relay-edge", PublicHost: "edge.example.com", ListenPort: 8443, PublicPort: 9443, Enabled: true},
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

	for _, q := range []string{"9443", "edge.example.com:9443"} {
		page, meta, err = svc.ListPage(context.Background(), ListQuery{AgentID: "edge", Q: q, Page: 1, PageSize: 10})
		if err != nil {
			t.Fatalf("public endpoint q=%q error = %v", q, err)
		}
		if meta.Total != 1 || len(page) != 1 || page[0].ID != 2 {
			t.Fatalf("public endpoint q=%q page=%v meta=%+v", q, page, meta)
		}
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

func TestCertificateServiceListPageTagsAndReferencedFilter(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		agents: []storage.AgentRow{
			{ID: "edge", Name: "Edge"},
		},
		httpRulesByID: map[string][]storage.HTTPRuleRow{
			"edge": {
				{ID: 1, AgentID: "edge", FrontendURL: "https://used.example.com", Enabled: true},
			},
		},
		relayByAgentID: map[string][]storage.RelayListenerRow{
			"edge": {
				{ID: 1, AgentID: "edge", Name: "tls", ListenPort: 7443, Enabled: true, CertificateID: intPtrService(43), TrustedCACertificateIDs: "[44]"},
			},
		},
		managedCerts: []storage.ManagedCertificateRow{
			{ID: 41, Domain: "used.example.com", TargetAgentIDs: `["edge"]`, Enabled: true, Status: "active", TagsJSON: `["prod"]`},
			{ID: 42, Domain: "orphan.example.com", TargetAgentIDs: `["edge"]`, Enabled: true, Status: "active", TagsJSON: `["prod"]`},
			{ID: 43, Domain: "relay.example.com", TargetAgentIDs: `["edge"]`, Enabled: true, Status: "active"},
			{ID: 44, Domain: "ca.example.com", TargetAgentIDs: `["edge"]`, Enabled: true, Status: "active"},
		},
	}
	svc := NewCertificateService(config.Config{}, store)

	certIDs := func(query ListQuery) map[int]bool {
		t.Helper()
		page, _, err := svc.ListPage(context.Background(), query)
		if err != nil {
			t.Fatalf("ListPage(%+v) error = %v", query, err)
		}
		ids := map[int]bool{}
		for _, cert := range page {
			ids[cert.ID] = true
		}
		return ids
	}
	assertSet := func(name string, query ListQuery, want map[int]bool) {
		t.Helper()
		ids := certIDs(query)
		if len(ids) != len(want) {
			t.Fatalf("%s ids = %v, want %v", name, ids, want)
		}
		for id := range want {
			if !ids[id] {
				t.Fatalf("%s ids = %v, missing id %d", name, ids, id)
			}
		}
	}

	assertSet("baseline", ListQuery{Page: 1, PageSize: 10}, map[int]bool{41: true, 42: true, 43: true, 44: true})

	// referenced: 41 via rule domain approximation, 43 via listener certificate_id,
	// 44 via listener trusted_ca_certificate_ids; 42 is unreferenced
	referenced := true
	assertSet("referenced true", ListQuery{Referenced: &referenced, Page: 1, PageSize: 10}, map[int]bool{41: true, 43: true, 44: true})
	notReferenced := false
	assertSet("referenced false", ListQuery{Referenced: &notReferenced, Page: 1, PageSize: 10}, map[int]bool{42: true})

	// tags OR + AND with referenced
	assertSet("tags prod", ListQuery{Tags: []string{"prod"}, Page: 1, PageSize: 10}, map[int]bool{41: true, 42: true})
	assertSet("tags prod + referenced", ListQuery{Tags: []string{"prod"}, Referenced: &referenced, Page: 1, PageSize: 10}, map[int]bool{41: true})
}
