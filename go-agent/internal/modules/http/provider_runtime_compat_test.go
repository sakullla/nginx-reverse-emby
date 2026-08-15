package http

import (
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestRuntimeRuleSpecPreservesLegacyURLBackendAcceptance(t *testing.T) {
	for name, backends := range map[string][]model.HTTPBackend{
		"trimmed URL":       {{URL: "  http://backend.example.test:8096/path  "}},
		"blank plus valid":  {{URL: ""}, {URL: "http://backend.example.test:8096"}},
		"duplicate URL":     {{URL: "http://backend.example.test:8096"}, {URL: "http://backend.example.test:8096"}},
		"userinfo fragment": {{URL: "http://user:pass@backend.example.test:8096/path#fragment"}},
	} {
		t.Run(name, func(t *testing.T) {
			rule := model.HTTPRule{FrontendURL: "http://public.example.test", Backends: backends}
			if _, err := runtimeRuleSpec(rule); err != nil {
				t.Fatalf("runtimeRuleSpec() broke legacy URL fixture: %v", err)
			}
			parsed, err := parseHTTPBackends(rule, nil, "")
			if err != nil || len(parsed) == 0 {
				t.Fatalf("parseHTTPBackends() = %d, %v", len(parsed), err)
			}
		})
	}
}
