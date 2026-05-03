package service

import (
	"context"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type SystemInfo struct {
	Role                         string
	LocalApplyRuntime            string
	DefaultAgentID               string
	LocalAgentEnabled            bool
	ProxyHeadersGloballyDisabled bool
	AppVersion                   string
	BuildTime                    string
	GoVersion                    string
	ProjectURL                   string
	DataDir                      string
	StartedAt                    time.Time
	OnlineAgents                 int
	TotalAgents                  int
	TrafficStatsEnabled          bool
}

type systemStore interface {
	ListAgents(ctx context.Context) ([]storage.AgentRow, error)
}

type systemService struct {
	cfg       config.Config
	store     systemStore
	startedAt time.Time
}

func NewSystemService(cfg config.Config, store ...systemStore) systemService {
	svc := systemService{
		cfg:       cfg,
		startedAt: time.Now(),
	}
	if len(store) > 0 {
		svc.store = store[0]
	}
	return svc
}

func (s systemService) Info(ctx context.Context) SystemInfo {
	defaultAgentID := ""
	if s.cfg.EnableLocalAgent {
		defaultAgentID = s.cfg.LocalAgentID
	}

	info := SystemInfo{
		Role:                         "master",
		LocalApplyRuntime:            "go-agent",
		DefaultAgentID:               defaultAgentID,
		LocalAgentEnabled:            s.cfg.EnableLocalAgent,
		ProxyHeadersGloballyDisabled: false,
		AppVersion:                   s.cfg.AppVersion,
		BuildTime:                    s.cfg.BuildTime,
		GoVersion:                    s.cfg.GoVersion,
		ProjectURL:                   s.cfg.ProjectURL,
		DataDir:                      s.cfg.DataDir,
		StartedAt:                    s.startedAt,
		TrafficStatsEnabled:          s.cfg.TrafficStatsEnabled,
	}

	if s.store != nil {
		agents, err := s.store.ListAgents(ctx)
		if err == nil {
			info.TotalAgents = len(agents)
			onlineThreshold := time.Now().Add(-s.cfg.HeartbeatInterval * 2)
			for _, a := range agents {
				if a.LastSeenAt != "" {
					lastSeen, err := time.Parse(time.RFC3339, a.LastSeenAt)
					if err == nil && lastSeen.After(onlineThreshold) {
						info.OnlineAgents++
					}
				}
			}
		}
	}

	return info
}
