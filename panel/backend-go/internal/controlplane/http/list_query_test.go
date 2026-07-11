package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

func TestParseListQueryDefaultsAndClamps(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/panel-api/http-rules?agent_id=edge&page=0&page_size=500&q=%20foo%20", nil)
	got := parseListQuery(req)
	if got.AgentID != "edge" {
		t.Fatalf("agent_id = %q", got.AgentID)
	}
	if got.Page != 1 {
		t.Fatalf("page = %d, want 1", got.Page)
	}
	if got.PageSize != service.MaxListPageSize {
		t.Fatalf("page_size = %d, want %d", got.PageSize, service.MaxListPageSize)
	}
	if got.Q != "foo" {
		t.Fatalf("q = %q", got.Q)
	}

	req = httptest.NewRequest(http.MethodGet, "/panel-api/http-rules", nil)
	got = parseListQuery(req)
	if got.Page != 1 || got.PageSize != service.DefaultListPageSize {
		t.Fatalf("defaults = %+v", got)
	}
	if got.AgentID != "" {
		t.Fatalf("empty agent_id expected, got %q", got.AgentID)
	}
}
