package service

import (
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
)

func TestPluginUIAssetNameMapsMountedPaths(t *testing.T) {
	t.Parallel()
	name, err := PluginUIAssetName("/")
	if err != nil || name != "ui/index.html" {
		t.Fatalf("index = %q err=%v", name, err)
	}
	name, err = PluginUIAssetName("/app.js")
	if err != nil || name != "ui/app.js" {
		t.Fatalf("js = %q err=%v", name, err)
	}
	if _, err := PluginUIAssetName("/api/apps"); err == nil {
		t.Fatal("api path must not map to a UI asset")
	}
	if _, err := PluginUIAssetName("/../secret.txt"); err == nil {
		t.Fatal("escaped path must be rejected")
	}
}

func TestMergePluginUIRoutesPrefersLiveMounts(t *testing.T) {
	t.Parallel()
	live := []pluginhost.UIRoute{{ID: "cloudflare-dns", Label: "live", Href: "/panel-api/plugins/cloudflare-dns/"}}
	declared := []pluginhost.UIRoute{
		{ID: "cloudflare-dns", Label: "declared", Href: "/panel-api/plugins/cloudflare-dns/"},
		{ID: "docker-app", Label: "Docker 应用", Href: "/panel-api/plugins/docker-app/"},
	}
	got := MergePluginUIRoutes(live, declared)
	if len(got) != 2 || got[0].ID != "cloudflare-dns" || got[0].Label != "live" || got[1].ID != "docker-app" {
		t.Fatalf("merged = %+v", got)
	}
}
