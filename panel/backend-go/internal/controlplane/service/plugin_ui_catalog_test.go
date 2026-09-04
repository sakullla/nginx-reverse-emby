//go:build !integration

package service

import (
	"errors"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestPluginUIAssetCandidatesIncludePackagedAssetsPrefix(t *testing.T) {
	t.Parallel()
	got := pluginUIAssetCandidates("ui/index.html")
	if len(got) != 2 || got[0] != "ui/index.html" || got[1] != "assets/ui/index.html" {
		t.Fatalf("candidates = %#v", got)
	}
}

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

func TestPluginUICatalogRejectsCrossPluginRouteCollision(t *testing.T) {
	t.Parallel()
	owners := make(map[string]string)
	manifestA := plugins.Manifest{ID: "owner-a", UIRouteID: "shared-route", ExtensionPoints: []string{pluginsdk.ExtensionUIRoute}}
	manifestB := plugins.Manifest{ID: "owner-b", UIRouteID: "shared-route", ExtensionPoints: []string{pluginsdk.ExtensionUIRoute}}
	if routeID, declared, err := claimPluginManifestUIRoute(owners, manifestA.ID, manifestA); err != nil || !declared || routeID != "shared-route" {
		t.Fatalf("first declared route = %q declared=%v err=%v", routeID, declared, err)
	}
	if _, _, err := claimPluginManifestUIRoute(owners, manifestB.ID, manifestB); !errors.Is(err, pluginhost.ErrUIRouteConflict) {
		t.Fatalf("declared/static route collision error = %v", err)
	}
}
