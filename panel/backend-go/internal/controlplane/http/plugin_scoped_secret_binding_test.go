//go:build !fast && !integration

package http

import (
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestRouterBindsResolvedProductionSecretService(t *testing.T) {
	var bound AgentPluginSecretService
	called := 0
	root := t.TempDir()
	store, err := storage.NewSQLiteStore(root, "local")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	production := service.NewPluginService(store, root)
	// Marketplace is an unused injected dependency here; this binding test
	// need not launch a refresh scheduler or seal a package cache.
	handler, err := NewRouter(Dependencies{Config: config.Config{DataDir: root, LocalAgentID: "local"}, PluginService: production, MarketplaceService: &service.MarketplaceService{}, BindPluginSecretService: func(source AgentPluginSecretService) error {
		called++
		if _, ok := source.(*service.PluginService); !ok {
			t.Fatal("binding did not receive production service")
		}
		bound = source
		if source != production {
			t.Fatal("router substituted a different secret authority")
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if closer, ok := handler.(interface{ Close() error }); ok {
		defer func() {
			if err := closer.Close(); err != nil {
				t.Errorf("router cleanup: %v", err)
			}
		}()
	}
	if called != 1 || bound == nil {
		t.Fatal("local consumer was not explicitly bound exactly once")
	}
}
