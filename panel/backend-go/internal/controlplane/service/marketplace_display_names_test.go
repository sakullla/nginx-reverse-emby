//go:build !integration

package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/marketplace"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestProjectCatalogDisplayNamesUsesSignedPackageManifest(t *testing.T) {
	digest := "4a68f6e4aecce4adb1e23991cdd4a03a90d52a2974c4c0b367db9a4b2e1aa336"
	snapshot := marketplace.Snapshot{
		Entries: []plugins.MarketEntry{
			{ID: "cloudflare-dns", Version: "0.1.5", PackageSHA256: digest, Description: "按域名后缀解析 Cloudflare DNS Token"},
			{ID: "waf", Version: "0.1.0", PackageSHA256: "6052b7aeea5f5142921f1398b6931f21e533edd70fcb9a0f7a1e36563b341c81"},
			{ID: "named", Version: "1.0.0", Name: "Already Named", PackageSHA256: "aaa"},
		},
		DirectPlugin: &plugins.DirectPluginSnapshot{ID: "direct-waf", Version: "2.0.0", PackageSHA256: "bbb"},
	}
	projectCatalogDisplayNames(&snapshot, []storage.PluginPackageRow{
		{PluginID: "cloudflare-dns", Version: "0.1.5", Digest: digest, ManifestJSON: `{"name":"Cloudflare DNS"}`},
		{PluginID: "direct-waf", Version: "2.0.0", Digest: "bbb", ManifestJSON: `{"name":"Direct WAF"}`},
	}, "")
	if snapshot.Entries[0].Name != "Cloudflare DNS" {
		t.Fatalf("catalog entry name = %q", snapshot.Entries[0].Name)
	}
	if snapshot.Entries[1].Name != "" {
		t.Fatalf("unknown catalog entry name = %q, want empty until a signed manifest is stored", snapshot.Entries[1].Name)
	}
	if snapshot.Entries[2].Name != "Already Named" {
		t.Fatalf("existing catalog entry name = %q", snapshot.Entries[2].Name)
	}
	if snapshot.DirectPlugin.Name != "Direct WAF" {
		t.Fatalf("direct plugin name = %q", snapshot.DirectPlugin.Name)
	}
}

func TestProjectCatalogDisplayNamesReadsCachedPluginYAML(t *testing.T) {
	digest := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	cacheRoot := t.TempDir()
	packageRoot := filepath.Join(cacheRoot, digest)
	if err := os.MkdirAll(packageRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageRoot, "plugin.yaml"), []byte("name: 资源加速\nid: accelerator-sources\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot := marketplace.Snapshot{
		Entries: []plugins.MarketEntry{{ID: "accelerator-sources", Version: "0.1.0", PackageSHA256: digest}},
	}
	projectCatalogDisplayNames(&snapshot, nil, cacheRoot)
	if snapshot.Entries[0].Name != "资源加速" {
		t.Fatalf("cached plugin.yaml name = %q", snapshot.Entries[0].Name)
	}
}
