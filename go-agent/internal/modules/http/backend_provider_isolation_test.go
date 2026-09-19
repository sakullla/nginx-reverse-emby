package http

import (
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestNewServerOmitsUnavailablePluginProviderRule(t *testing.T) {
	listener := model.HTTPListener{Rules: []model.HTTPRule{
		{ID: 1, FrontendURL: "https://unavailable.example.test", Backends: []model.HTTPBackend{{
			Kind:           pluginsdk.HTTPBackendKindPluginProvider,
			PluginProvider: &pluginsdk.HTTPPluginProviderRef{InstanceID: "docker-app-default", ProviderID: "default"},
		}}},
		{ID: 2, FrontendURL: "https://healthy.example.test", Backends: []model.HTTPBackend{{URL: "http://127.0.0.1:8096"}}},
	}}
	server, err := newServer(listener, nil, Providers{}, model.NewCache(model.BackendCacheConfig{}), NewSharedTransport())
	if err != nil {
		t.Fatalf("newServer() error = %v", err)
	}
	if _, found := server.routes["unavailable.example.test"]; found {
		t.Fatal("unavailable plugin-backed route was published")
	}
	if _, found := server.routes["healthy.example.test"]; !found {
		t.Fatal("healthy unrelated route was omitted")
	}
}
