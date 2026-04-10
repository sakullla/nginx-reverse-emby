package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	defaultListenAddr        = "0.0.0.0:8080"
	defaultDataDir           = "/opt/nginx-reverse-emby/panel/data"
	defaultFrontendDistDir   = "/opt/nginx-reverse-emby/panel/frontend/dist"
	defaultPublicAssetsDir   = "/opt/nginx-reverse-emby/panel/public/agent-assets"
	defaultEnableLocalAgent  = true
	defaultLocalAgentID      = "local"
	defaultLocalAgentName    = "local"
	defaultHeartbeatInterval = 30 * time.Second
)

type Config struct {
	ListenAddr           string
	DataDir              string
	PanelToken           string
	RegisterToken        string
	FrontendDistDir      string
	PublicAgentAssetsDir string
	EnableLocalAgent     bool
	LocalAgentID         string
	LocalAgentName       string
	HeartbeatInterval    time.Duration
}

func Default() Config {
	return Config{
		ListenAddr:           defaultListenAddr,
		DataDir:              defaultDataDir,
		FrontendDistDir:      defaultFrontendDistDir,
		PublicAgentAssetsDir: defaultPublicAssetsDir,
		EnableLocalAgent:     defaultEnableLocalAgent,
		LocalAgentID:         defaultLocalAgentID,
		LocalAgentName:       defaultLocalAgentName,
		HeartbeatInterval:    defaultHeartbeatInterval,
	}
}

func LoadFromEnv() (Config, error) {
	cfg := Default()

	if val := strings.TrimSpace(firstEnv("NRE_CONTROL_PLANE_ADDR", "")); val != "" {
		cfg.ListenAddr = val
	} else {
		host := strings.TrimSpace(firstEnv("PANEL_BACKEND_HOST", ""))
		port := strings.TrimSpace(firstEnv("PANEL_BACKEND_PORT", ""))
		if host != "" || port != "" {
			if host == "" {
				host = "127.0.0.1"
			}
			if port == "" {
				port = "8080"
			}
			cfg.ListenAddr = fmt.Sprintf("%s:%s", host, port)
		}
	}
	if val := strings.TrimSpace(firstEnv("NRE_CONTROL_PLANE_DATA_DIR", "PANEL_DATA_ROOT")); val != "" {
		cfg.DataDir = val
	}

	panelToken := strings.TrimSpace(firstEnv("NRE_PANEL_TOKEN", "API_TOKEN"))
	if panelToken == "" {
		return Config{}, errors.New("NRE_PANEL_TOKEN is required")
	}
	cfg.PanelToken = panelToken

	registerToken := strings.TrimSpace(firstEnv("NRE_REGISTER_TOKEN", "MASTER_REGISTER_TOKEN", "PANEL_REGISTER_TOKEN", "API_TOKEN"))
	if registerToken == "" {
		return Config{}, errors.New("NRE_REGISTER_TOKEN is required")
	}
	cfg.RegisterToken = registerToken

	frontendDistDir := strings.TrimSpace(firstEnv("NRE_FRONTEND_DIST_DIR", "PANEL_FRONTEND_DIST_DIR"))
	if frontendDistDir != "" {
		cfg.FrontendDistDir = frontendDistDir
	}

	publicAssetsDir := strings.TrimSpace(firstEnv("NRE_PUBLIC_AGENT_ASSETS_DIR", "PANEL_PUBLIC_AGENT_ASSETS_DIR"))
	if publicAssetsDir != "" {
		cfg.PublicAgentAssetsDir = publicAssetsDir
	}

	if val := strings.TrimSpace(firstEnv("NRE_ENABLE_LOCAL_AGENT", "MASTER_LOCAL_AGENT_ENABLED")); val != "" {
		enabled, err := parseBool(val)
		if err != nil {
			return Config{}, fmt.Errorf("invalid NRE_ENABLE_LOCAL_AGENT: %w", err)
		}
		cfg.EnableLocalAgent = enabled
	}
	if val := strings.TrimSpace(os.Getenv("NRE_LOCAL_AGENT_ID")); val != "" {
		cfg.LocalAgentID = val
	}
	if val := strings.TrimSpace(os.Getenv("NRE_LOCAL_AGENT_NAME")); val != "" {
		cfg.LocalAgentName = val
	}
	if val := strings.TrimSpace(os.Getenv("NRE_HEARTBEAT_INTERVAL")); val != "" {
		dur, err := time.ParseDuration(val)
		if err != nil {
			return Config{}, fmt.Errorf("invalid NRE_HEARTBEAT_INTERVAL: %w", err)
		}
		if dur <= 0 {
			return Config{}, errors.New("NRE_HEARTBEAT_INTERVAL must be positive")
		}
		cfg.HeartbeatInterval = dur
	}

	return cfg, nil
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if value := os.Getenv(key); strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func parseBool(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("unsupported boolean value %q", value)
	}
}
