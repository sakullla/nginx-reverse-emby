package service

import "testing"

func TestNormalizeListQueryDefaultsAndClamps(t *testing.T) {
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
	got := NormalizeListQuery(ListQuery{Status: "  active  "})
	if got.Status != "active" {
		t.Fatalf("status = %q", got.Status)
	}
}
