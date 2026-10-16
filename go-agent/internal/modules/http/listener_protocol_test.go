package http

import (
	"context"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestValidateRulesRejectsMixedProtocolsOnSameAddress(t *testing.T) {
	rules := []model.HTTPRule{
		{ID: 1, Enabled: true, FrontendURL: "http://example.test:8443", Backends: []model.HTTPBackend{{URL: "http://backend.test"}}},
		{ID: 2, Enabled: true, FrontendURL: "https://example.test:8443", Backends: []model.HTTPBackend{{URL: "http://backend.test"}}},
	}
	if err := ValidateRules(context.Background(), rules, nil, Providers{}); err == nil || !strings.Contains(err.Error(), "cannot serve HTTP and HTTPS") {
		t.Fatalf("mixed protocols validation error = %v", err)
	}
}
