package service

import "testing"

func TestNormalizeListQueryDefaultsAndClamps(t *testing.T) {
	t.Parallel()
	got := NormalizeListQuery(ListQuery{})
	if got.Page != 1 {
		t.Fatalf("page = %d, want 1", got.Page)
	}
	if got.PageSize != DefaultListPageSize {
		t.Fatalf("page_size = %d, want %d", got.PageSize, DefaultListPageSize)
	}

	got = NormalizeListQuery(ListQuery{Page: 0, PageSize: 0, AgentID: "  edge  ", Q: "  foo  "})
	if got.Page != 1 || got.PageSize != DefaultListPageSize {
		t.Fatalf("got page=%d page_size=%d", got.Page, got.PageSize)
	}
	if got.AgentID != "edge" || got.Q != "foo" {
		t.Fatalf("trim failed: %+v", got)
	}

	got = NormalizeListQuery(ListQuery{Page: -3, PageSize: 500})
	if got.Page != 1 {
		t.Fatalf("negative page = %d, want 1", got.Page)
	}
	if got.PageSize != MaxListPageSize {
		t.Fatalf("page_size = %d, want max %d", got.PageSize, MaxListPageSize)
	}
}

func TestApplyPageBounds(t *testing.T) {
	t.Parallel()
	items := []int{1, 2, 3, 4, 5}
	page, meta := ApplyPage(items, ListQuery{Page: 2, PageSize: 2})
	if meta.Total != 5 || meta.Page != 2 || meta.PageSize != 2 {
		t.Fatalf("meta = %+v", meta)
	}
	if len(page) != 2 || page[0] != 3 || page[1] != 4 {
		t.Fatalf("page = %v", page)
	}

	page, meta = ApplyPage(items, ListQuery{Page: 10, PageSize: 2})
	if meta.Total != 5 {
		t.Fatalf("total = %d", meta.Total)
	}
	if len(page) != 0 {
		t.Fatalf("beyond last page should be empty, got %v", page)
	}

	page, meta = ApplyPage([]int(nil), ListQuery{Page: 1, PageSize: 20})
	if meta.Total != 0 || len(page) != 0 {
		t.Fatalf("empty list page=%v meta=%+v", page, meta)
	}
}

func TestMatchesListQuery(t *testing.T) {
	t.Parallel()
	if !matchesListQuery("", "anything") {
		t.Fatal("empty q should match")
	}
	if !matchesListQuery("EX", "prefix", "Example.com") {
		t.Fatal("expected case-insensitive match")
	}
	if matchesListQuery("zzz", "alpha", "beta") {
		t.Fatal("expected no match")
	}
}

func TestMatchesEnabledFilter(t *testing.T) {
	t.Parallel()
	if !matchesEnabledFilter(nil, true) || !matchesEnabledFilter(nil, false) {
		t.Fatal("nil enabled should match any value")
	}
	yes := true
	no := false
	if !matchesEnabledFilter(&yes, true) {
		t.Fatal("true filter should match true")
	}
	if matchesEnabledFilter(&yes, false) {
		t.Fatal("true filter should reject false")
	}
	if !matchesEnabledFilter(&no, false) {
		t.Fatal("false filter should match false")
	}
	if matchesEnabledFilter(&no, true) {
		t.Fatal("false filter should reject true")
	}
}

func TestMatchesStatusFilter(t *testing.T) {
	t.Parallel()
	if !matchesStatusFilter("", "active") {
		t.Fatal("empty status should match")
	}
	if !matchesStatusFilter("  Active  ", "active") {
		t.Fatal("status match should be case-insensitive and trimmed")
	}
	if matchesStatusFilter("pending", "active") {
		t.Fatal("status mismatch should reject")
	}
}

func TestNormalizeListQueryTrimsStatus(t *testing.T) {
	t.Parallel()
	got := NormalizeListQuery(ListQuery{Status: "  active  "})
	if got.Status != "active" {
		t.Fatalf("status = %q", got.Status)
	}
}

func TestNormalizeListQueryTagsAndSync(t *testing.T) {
	t.Parallel()
	got := NormalizeListQuery(ListQuery{
		Tags: []string{"  web  ", "", "  ", "prod"},
		Sync: "  Applied ",
	})
	if len(got.Tags) != 2 || got.Tags[0] != "web" || got.Tags[1] != "prod" {
		t.Fatalf("tags = %v", got.Tags)
	}
	if got.Sync != ListSyncApplied {
		t.Fatalf("sync = %q, want %q", got.Sync, ListSyncApplied)
	}

	got = NormalizeListQuery(ListQuery{Tags: []string{"", "  "}, Sync: "bogus"})
	if len(got.Tags) != 0 {
		t.Fatalf("empty tags should be dropped, got %v", got.Tags)
	}
	if got.Sync != "" {
		t.Fatalf("invalid sync should be ignored, got %q", got.Sync)
	}
}

func TestMatchesTagsFilter(t *testing.T) {
	t.Parallel()
	if !matchesTagsFilter(nil, []string{"web"}) {
		t.Fatal("nil filter should match")
	}
	if !matchesTagsFilter([]string{"web", "prod"}, []string{"prod"}) {
		t.Fatal("OR semantics: any requested tag should match")
	}
	if !matchesTagsFilter([]string{"web", "prod"}, []string{"internal", "web"}) {
		t.Fatal("OR semantics: overlap should match")
	}
	if matchesTagsFilter([]string{"web"}, []string{"web2", "internal"}) {
		t.Fatal("tag match must be exact, not substring")
	}
	if matchesTagsFilter([]string{"web"}, nil) {
		t.Fatal("resource without tags should not match")
	}
}

func TestMatchesOptionalIntFilter(t *testing.T) {
	t.Parallel()
	one := 1
	if !matchesOptionalIntFilter(nil, nil) || !matchesOptionalIntFilter(nil, &one) {
		t.Fatal("nil filter should match any value")
	}
	if !matchesOptionalIntFilter(&one, &one) {
		t.Fatal("equal values should match")
	}
	if matchesOptionalIntFilter(&one, nil) {
		t.Fatal("nil value should not match non-nil filter")
	}
	two := 2
	if matchesOptionalIntFilter(&one, &two) {
		t.Fatal("different values should not match")
	}
}

func TestMatchesSyncFilter(t *testing.T) {
	t.Parallel()
	if !matchesSyncFilter("", 5, 0, false) {
		t.Fatal("empty sync should not filter")
	}
	if !matchesSyncFilter("bogus", 5, 0, false) {
		t.Fatal("unrecognized sync should not filter")
	}
	if !matchesSyncFilter(ListSyncApplied, 3, 5, true) {
		t.Fatal("revision <= last_apply_revision should be applied")
	}
	if matchesSyncFilter(ListSyncApplied, 6, 5, true) {
		t.Fatal("revision > last_apply_revision should not be applied")
	}
	if matchesSyncFilter(ListSyncApplied, 1, 0, true) {
		t.Fatal("agent without a reported apply revision should not be applied")
	}
	if matchesSyncFilter(ListSyncApplied, 1, 5, false) {
		t.Fatal("unknown agent should not be applied")
	}
	if !matchesSyncFilter(ListSyncPending, 6, 5, true) {
		t.Fatal("revision ahead should be pending")
	}
	if !matchesSyncFilter(ListSyncPending, 1, 0, false) {
		t.Fatal("unknown agent should be pending")
	}
}

func TestMatchesReferencedFilter(t *testing.T) {
	t.Parallel()
	if !matchesReferencedFilter(nil, true) || !matchesReferencedFilter(nil, false) {
		t.Fatal("nil referenced filter should match")
	}
	yes := true
	no := false
	if !matchesReferencedFilter(&yes, true) || matchesReferencedFilter(&yes, false) {
		t.Fatal("referenced=true should only match referenced")
	}
	if !matchesReferencedFilter(&no, false) || matchesReferencedFilter(&no, true) {
		t.Fatal("referenced=false should only match unreferenced")
	}
}
