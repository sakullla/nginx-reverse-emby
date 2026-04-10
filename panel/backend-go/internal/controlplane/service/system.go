package service

import (
	"context"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
)

type SystemInfo struct {
	Role                         string
	LocalApplyRuntime            string
	DefaultAgentID               string
	LocalAgentEnabled            bool
	ProxyHeadersGloballyDisabled bool
}

type systemService struct {
	cfg config.Config
}

func NewSystemService(cfg config.Config) systemService {
	return systemService{cfg: cfg}
}

func (s systemService) Info(context.Context) SystemInfo {
	defaultAgentID := ""
	if s.cfg.EnableLocalAgent {
		defaultAgentID = s.cfg.LocalAgentID
	}

	return SystemInfo{
		Role:                         "master",
		LocalApplyRuntime:            "go-agent",
		DefaultAgentID:               defaultAgentID,
		LocalAgentEnabled:            s.cfg.EnableLocalAgent,
		ProxyHeadersGloballyDisabled: false,
	}
}
