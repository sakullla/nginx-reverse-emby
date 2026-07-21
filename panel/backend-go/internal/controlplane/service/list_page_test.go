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

func TestRuleServiceListPageExcludesInactiveLocalAgentRows(t *testing.T) {
	t.Parallel()
	store := &fakeRuleStore{
		agents: []storage.AgentRow{
			{ID: "old-local", Name: "Old Local", IsLocal: true},
			{ID: "edge", Name: "Edge"},
		},
		rulesByAgent: map[string][]storage.HTTPRuleRow{
			"old-local": {{ID: 1, AgentID: "old-local", FrontendURL: "https://stale.example.com", Enabled: true}},
			"edge":      {{ID: 2, AgentID: "edge", FrontendURL: "https://edge.example.com", Enabled: true}},
		},
	}
	svc := NewRuleService(config.Config{}, store)

	page, meta, err := svc.ListPage(context.Background(), ListQuery{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("ListPage error = %v", err)
	}
	if meta.Total != 1 || len(page) != 1 || page[0].AgentID != "edge" {
		t.Fatalf("page=%v meta=%+v, want only active remote agent", page, meta)
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

func TestRuleServiceListPageTagsRelationAndSyncFilters(t *testing.T) {
	t.Parallel()
	store := &fakeRuleStore{
		agents: []storage.AgentRow{
			{ID: "edge", Name: "Edge", LastApplyRevision: 5},
			{ID: "hub", Name: "Hub", LastApplyRevision: 2},
			{ID: "fresh", Name: "Fresh"}, // registered but never reported an apply
		},
		managedCerts: []storage.ManagedCertificateRow{
			{ID: 42, Domain: "a.example.com"},
		},
		rulesByAgent: map[string][]storage.HTTPRuleRow{
			"local": {
				{ID: 5, AgentID: "local", FrontendURL: "https://e.example.com", Enabled: true, Revision: 1},
			},
			"edge": {
				{ID: 1, AgentID: "edge", FrontendURL: "https://a.example.com", Enabled: true, TagsJSON: `["web","prod"]`, EgressProfileID: intPtrRule(7), RelayLayersJSON: `[[10,11]]`, Revision: 3},
				{ID: 2, AgentID: "edge", FrontendURL: "https://b.example.com", Enabled: false, TagsJSON: `["web"]`, RelayLayersJSON: `[[20]]`, Revision: 6},
			},
			"hub": {
				{ID: 3, AgentID: "hub", FrontendURL: "https://c.example.com", Enabled: true, TagsJSON: `["internal"]`, EgressProfileID: intPtrRule(8), Revision: 2},
			},
			"fresh": {
				{ID: 4, AgentID: "fresh", FrontendURL: "https://d.example.com", Enabled: true, Revision: 1},
			},
		},
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
		LocalAgentName:   "Local",
	}, store)

	ruleIDs := func(query ListQuery) []int {
		t.Helper()
		page, _, err := svc.ListPage(context.Background(), query)
		if err != nil {
			t.Fatalf("ListPage(%+v) error = %v", query, err)
		}
		ids := make([]int, 0, len(page))
		for _, rule := range page {
			ids = append(ids, rule.ID)
		}
		return ids
	}
	assertIDs := func(name string, query ListQuery, want map[int]bool) {
		t.Helper()
		ids := ruleIDs(query)
		if len(ids) != len(want) {
			t.Fatalf("%s ids = %v, want set %v", name, ids, want)
		}
		for _, id := range ids {
			if !want[id] {
				t.Fatalf("%s ids = %v, unexpected id %d", name, ids, id)
			}
		}
	}
	set := func(ids ...int) map[int]bool {
		out := map[int]bool{}
		for _, id := range ids {
			out[id] = true
		}
		return out
	}

	// no new params: result set identical to the pre-filter baseline
	assertIDs("baseline", ListQuery{Page: 1, PageSize: 10}, set(1, 2, 3, 4, 5))

	// tags: OR within the dimension, exact element match
	assertIDs("tags single", ListQuery{Tags: []string{"prod"}, Page: 1, PageSize: 10}, set(1))
	assertIDs("tags or", ListQuery{Tags: []string{"web", "internal"}, Page: 1, PageSize: 10}, set(1, 2, 3))
	assertIDs("tags exact", ListQuery{Tags: []string{"we"}, Page: 1, PageSize: 10}, set())

	// egress profile equality
	assertIDs("egress 7", ListQuery{EgressProfileID: intPtrRule(7), Page: 1, PageSize: 10}, set(1))
	assertIDs("egress 8", ListQuery{EgressProfileID: intPtrRule(8), Page: 1, PageSize: 10}, set(3))

	// relay listener contained in any relay layer
	assertIDs("relay 11", ListQuery{RelayListenerID: intPtrRule(11), Page: 1, PageSize: 10}, set(1))
	assertIDs("relay 20", ListQuery{RelayListenerID: intPtrRule(20), Page: 1, PageSize: 10}, set(2))
	assertIDs("relay missing", ListQuery{RelayListenerID: intPtrRule(99), Page: 1, PageSize: 10}, set())

	// certificate domain approximation: cert 42 (a.example.com) matches rule 1's host
	assertIDs("cert 42", ListQuery{CertificateID: intPtrRule(42), Page: 1, PageSize: 10}, set(1))
	assertIDs("cert missing", ListQuery{CertificateID: intPtrRule(43), Page: 1, PageSize: 10}, set())

	// cross-dimension AND: tags OR'd internally, then ANDed with enabled
	enabled := true
	assertIDs("tags+enabled", ListQuery{Tags: []string{"web"}, Enabled: &enabled, Page: 1, PageSize: 10}, set(1))

	// sync approximation: edge applied through revision 5, hub through 2,
	// fresh/local never reported -> pending
	assertIDs("sync applied", ListQuery{Sync: ListSyncApplied, Page: 1, PageSize: 10}, set(1, 3))
	assertIDs("sync pending", ListQuery{Sync: ListSyncPending, Page: 1, PageSize: 10}, set(2, 4, 5))
}

func TestL4ServiceListPageExtendedFilters(t *testing.T) {
	t.Parallel()
	store := &fakeL4Store{
		agents: []storage.AgentRow{
			{ID: "edge", Name: "Edge", CapabilitiesJSON: `["l4"]`, LastApplyRevision: 4},
		},
		l4RulesByID: map[string][]storage.L4RuleRow{
			"edge": {
				{ID: 1, AgentID: "edge", Name: "tcp-1", Protocol: "tcp", ListenHost: "0.0.0.0", ListenPort: 1001, Enabled: true, TagsJSON: `["tcp","edge"]`, EgressProfileID: intPtrL4(7), RelayLayersJSON: `[[10]]`, Revision: 3},
				{ID: 2, AgentID: "edge", Name: "udp-1", Protocol: "udp", ListenHost: "0.0.0.0", ListenPort: 1002, Enabled: true, TagsJSON: `["udp"]`, Revision: 6},
			},
		},
	}
	svc := NewL4RuleService(config.Config{}, store)

	list := func(query ListQuery) ([]L4Rule, PageMeta) {
		t.Helper()
		page, meta, err := svc.ListPage(context.Background(), query)
		if err != nil {
			t.Fatalf("ListPage(%+v) error = %v", query, err)
		}
		return page, meta
	}

	page, meta := list(ListQuery{Page: 1, PageSize: 10})
	if meta.Total != 2 {
		t.Fatalf("baseline total = %d, want 2", meta.Total)
	}

	page, _ = list(ListQuery{Tags: []string{"tcp"}, Page: 1, PageSize: 10})
	if len(page) != 1 || page[0].ID != 1 {
		t.Fatalf("tags tcp page = %v", page)
	}
	page, _ = list(ListQuery{Tags: []string{"tcp", "udp"}, Page: 1, PageSize: 10})
	if len(page) != 2 {
		t.Fatalf("tags or page = %v", page)
	}

	page, _ = list(ListQuery{EgressProfileID: intPtrL4(7), Page: 1, PageSize: 10})
	if len(page) != 1 || page[0].ID != 1 {
		t.Fatalf("egress page = %v", page)
	}

	page, _ = list(ListQuery{RelayListenerID: intPtrL4(10), Page: 1, PageSize: 10})
	if len(page) != 1 || page[0].ID != 1 {
		t.Fatalf("relay 10 page = %v", page)
	}
	page, _ = list(ListQuery{RelayListenerID: intPtrL4(11), Page: 1, PageSize: 10})
	if len(page) != 0 {
		t.Fatalf("relay 11 page = %v", page)
	}

	page, _ = list(ListQuery{Sync: ListSyncApplied, Page: 1, PageSize: 10})
	if len(page) != 1 || page[0].ID != 1 {
		t.Fatalf("sync applied page = %v", page)
	}
	page, _ = list(ListQuery{Sync: ListSyncPending, Page: 1, PageSize: 10})
	if len(page) != 1 || page[0].ID != 2 {
		t.Fatalf("sync pending page = %v", page)
	}
}

func TestRelayServiceListPageExtendedFilters(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		agents: []storage.AgentRow{
			{ID: "edge", Name: "Edge", LastApplyRevision: 5},
		},
		relayByAgentID: map[string][]storage.RelayListenerRow{
			"local": {
				{ID: 1, AgentID: "local", Name: "relay-local", ListenPort: 7443, Enabled: true, TagsJSON: `["relay"]`, CertificateID: intPtrService(42), Revision: 1},
			},
			"edge": {
				{ID: 2, AgentID: "edge", Name: "relay-edge", ListenPort: 8443, Enabled: true, TagsJSON: `["relay","edge"]`, Revision: 4},
				{ID: 3, AgentID: "edge", Name: "relay-edge-2", ListenPort: 8444, Enabled: true, TagsJSON: `["other"]`, CertificateID: intPtrService(43), Revision: 9},
			},
		},
	}
	svc := NewRelayListenerService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
		LocalAgentName:   "Local",
	}, store)

	list := func(query ListQuery) []RelayListener {
		t.Helper()
		page, _, err := svc.ListPage(context.Background(), query)
		if err != nil {
			t.Fatalf("ListPage(%+v) error = %v", query, err)
		}
		return page
	}

	page := list(ListQuery{Page: 1, PageSize: 10})
	if len(page) != 3 {
		t.Fatalf("baseline len = %d, want 3", len(page))
	}

	page = list(ListQuery{Tags: []string{"relay"}, Page: 1, PageSize: 10})
	if len(page) != 2 {
		t.Fatalf("tags relay page = %v", page)
	}
	page = list(ListQuery{Tags: []string{"relay", "other"}, Page: 1, PageSize: 10})
	if len(page) != 3 {
		t.Fatalf("tags or page = %v", page)
	}

	page = list(ListQuery{CertificateID: intPtrService(42), Page: 1, PageSize: 10})
	if len(page) != 1 || page[0].ID != 1 {
		t.Fatalf("cert 42 page = %v", page)
	}
	page = list(ListQuery{CertificateID: intPtrService(43), Page: 1, PageSize: 10})
	if len(page) != 1 || page[0].ID != 3 {
		t.Fatalf("cert 43 page = %v", page)
	}
	page = list(ListQuery{CertificateID: intPtrService(44), Page: 1, PageSize: 10})
	if len(page) != 0 {
		t.Fatalf("cert 44 page = %v", page)
	}

	// local agent never reported an apply revision -> pending
	page = list(ListQuery{Sync: ListSyncApplied, Page: 1, PageSize: 10})
	if len(page) != 1 || page[0].ID != 2 {
		t.Fatalf("sync applied page = %v", page)
	}
	page = list(ListQuery{Sync: ListSyncPending, Page: 1, PageSize: 10})
	if len(page) != 2 {
		t.Fatalf("sync pending page = %v", page)
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
