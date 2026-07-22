package http

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

func parseListQuery(r *http.Request) service.ListQuery {
	q := r.URL.Query()
	page, _ := strconv.Atoi(strings.TrimSpace(q.Get("page")))
	pageSize, _ := strconv.Atoi(strings.TrimSpace(q.Get("page_size")))
	return service.NormalizeListQuery(service.ListQuery{
		AgentID:         q.Get("agent_id"),
		Page:            page,
		PageSize:        pageSize,
		Q:               q.Get("q"),
		Enabled:         parseOptionalBoolQuery(q.Get("enabled")),
		Status:          q.Get("status"),
		Tags:            parseCSVQuery(q.Get("tags")),
		CertificateID:   parseOptionalIntQuery(q.Get("certificate_id")),
		EgressProfileID: parseOptionalIntQuery(q.Get("egress_profile_id")),
		RelayListenerID: parseOptionalIntQuery(q.Get("relay_listener_id")),
		Sync:            q.Get("sync"),
		Referenced:      parseOptionalBoolQuery(q.Get("referenced")),
	})
}

// parseCSVQuery splits a comma-separated query value into trimmed non-empty items.
func parseCSVQuery(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			items = append(items, trimmed)
		}
	}
	return items
}

// parseOptionalIntQuery parses a positive integer query value.
// Empty, non-integer, or non-positive values yield nil (no filter).
func parseOptionalIntQuery(raw string) *int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return nil
	}
	return &value
}

// parseOptionalBoolQuery accepts true/false (case-insensitive, also 1/0).
// Empty or unrecognized values yield nil (no filter).
func parseOptionalBoolQuery(raw string) *bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "1":
		v := true
		return &v
	case "false", "0":
		v := false
		return &v
	default:
		return nil
	}
}

func writeListPageJSON(w http.ResponseWriter, collectionKey string, items any, meta service.PageMeta) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		collectionKey: items,
		"total":      meta.Total,
		"page":       meta.Page,
		"page_size":  meta.PageSize,
	})
}
