package config

import (
	"reflect"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/app"
)

func TestDefaultReturnsCanonicalAppConfig(t *testing.T) {
	cfg := Default()

	if reflect.TypeOf(cfg) != reflect.TypeOf(app.Config{}) {
		t.Fatalf("expected Default to return app.Config, got %T", cfg)
	}

	if cfg.AgentID != "bootstrap" {
		t.Fatalf("expected bootstrap agent ID, got %q", cfg.AgentID)
	}
}
