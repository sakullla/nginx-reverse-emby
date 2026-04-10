package http

import (
	"context"
	"errors"
	"net/http"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type SystemService interface {
	Info(context.Context) service.SystemInfo
}

type AgentService interface {
	List(context.Context) ([]service.AgentSummary, error)
	ListHTTPRules(context.Context, string) ([]service.HTTPRule, error)
}

type Dependencies struct {
	Config        config.Config
	SystemService SystemService
	AgentService  AgentService
}

func NewRouter(deps Dependencies) (http.Handler, error) {
	resolved, err := deps.withDefaults()
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	for _, prefix := range []string{"/panel-api", "/api"} {
		mux.Handle(prefix+"/health", http.HandlerFunc(resolved.handleHealth))
		mux.Handle(prefix+"/auth/verify", http.HandlerFunc(resolved.handleVerify))
		mux.Handle(prefix+"/info", http.HandlerFunc(resolved.handleInfo))
		mux.Handle(prefix+"/agents", resolved.requirePanelToken(http.HandlerFunc(resolved.handleAgents)))
		mux.Handle(prefix+"/agents/{agentID}/rules", resolved.requirePanelToken(http.HandlerFunc(resolved.handleAgentRules)))
	}

	return mux, nil
}

func (d Dependencies) withDefaults() (Dependencies, error) {
	if d.SystemService != nil && d.AgentService != nil {
		return d, nil
	}

	store, err := storage.NewSQLiteStore(d.Config.DataDir, d.Config.LocalAgentID)
	if err != nil {
		return Dependencies{}, err
	}

	if d.SystemService == nil {
		d.SystemService = service.NewSystemService(d.Config)
	}
	if d.AgentService == nil {
		d.AgentService = service.NewAgentService(d.Config, store)
	}

	return d, nil
}

func mapServiceError(err error) (int, map[string]any) {
	switch {
	case errors.Is(err, service.ErrAgentNotFound):
		return http.StatusNotFound, errorPayload("agent not found")
	default:
		return http.StatusInternalServerError, errorPayload("internal server error")
	}
}
