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
		AgentID:  q.Get("agent_id"),
		Page:     page,
		PageSize: pageSize,
		Q:        q.Get("q"),
	})
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
