package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/marketplace"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

const pluginUIAssetLimit = 1 << 20

var ErrPluginUIAssetNotFound = errors.New("plugin UI asset is not found")

// DeclaredUIRoutes lists ui.route entries from installed plugin packages.
// Live control-plane mounts remain the runtime proxy; this catalog lets
// agent-scoped plugins appear in the panel without a host-invented page.
func (s *PluginService) DeclaredUIRoutes(ctx context.Context) ([]pluginhost.UIRoute, error) {
	if s == nil {
		return nil, errors.New("plugin service is required")
	}
	installed, err := s.store.ListInstalledPlugins(ctx)
	if err != nil {
		return nil, err
	}
	routes := make([]pluginhost.UIRoute, 0)
	seen := make(map[string]struct{})
	for _, row := range installed {
		packageRow, ok, err := s.storedPackage(ctx, row.ActivePackageIdentity, row.ActivePackageDigest)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		var manifest plugins.Manifest
		if err := json.Unmarshal([]byte(packageRow.ManifestJSON), &manifest); err != nil {
			continue
		}
		if !hasPluginExtension(manifest.ExtensionPoints, pluginsdk.ExtensionUIRoute) {
			continue
		}
		routeID := strings.TrimSpace(manifest.UIRouteID)
		if routeID == "" {
			routeID = strings.TrimSpace(manifest.ID)
		}
		if routeID == "" {
			continue
		}
		if _, exists := seen[routeID]; exists {
			continue
		}
		seen[routeID] = struct{}{}
		label := strings.TrimSpace(manifest.Metadata["ui.nav.label"])
		if label == "" {
			label = routeID
		}
		routes = append(routes, pluginhost.UIRoute{
			ID:    routeID,
			Label: label,
			Group: strings.TrimSpace(manifest.Metadata["ui.nav.group"]),
			Href:  pluginhost.UIHref(routeID),
		})
	}
	sort.Slice(routes, func(i, j int) bool { return routes[i].ID < routes[j].ID })
	return routes, nil
}

// OpenUIAsset reads a declared ui/ frontend file from the installed package.
func (s *PluginService) OpenUIAsset(ctx context.Context, routeID, suffix string) (string, []byte, error) {
	if s == nil {
		return "", nil, errors.New("plugin service is required")
	}
	logical, err := PluginUIAssetName(suffix)
	if err != nil {
		return "", nil, err
	}
	installed, err := s.store.ListInstalledPlugins(ctx)
	if err != nil {
		return "", nil, err
	}
	for _, row := range installed {
		packageRow, ok, err := s.storedPackage(ctx, row.ActivePackageIdentity, row.ActivePackageDigest)
		if err != nil {
			return "", nil, err
		}
		if !ok {
			continue
		}
		var manifest plugins.Manifest
		if err := json.Unmarshal([]byte(packageRow.ManifestJSON), &manifest); err != nil {
			continue
		}
		if !pluginManifestOwnsUIRoute(manifest, routeID) {
			continue
		}
		if !pluginManifestDeclaresAsset(manifest, logical) {
			return "", nil, ErrPluginUIAssetNotFound
		}
		if err := marketplace.ValidateCachePath(s.cacheRoot, packageRow.CachePath, packageRow.Digest, packageRow.SignatureFingerprint); err != nil {
			return "", nil, err
		}
		full, err := joinPluginPackageFile(packageRow.CachePath, logical)
		if err != nil {
			return "", nil, err
		}
		file, err := os.Open(full)
		if err != nil {
			if os.IsNotExist(err) {
				return "", nil, ErrPluginUIAssetNotFound
			}
			return "", nil, err
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, pluginUIAssetLimit+1))
		if err != nil {
			return "", nil, err
		}
		if len(data) > pluginUIAssetLimit {
			return "", nil, errors.New("plugin UI asset exceeds the bounded size")
		}
		return logical, data, nil
	}
	return "", nil, ErrPluginNotInstalled
}

func pluginManifestOwnsUIRoute(manifest plugins.Manifest, routeID string) bool {
	if !hasPluginExtension(manifest.ExtensionPoints, pluginsdk.ExtensionUIRoute) {
		return false
	}
	id := strings.TrimSpace(manifest.UIRouteID)
	if id == "" {
		id = strings.TrimSpace(manifest.ID)
	}
	return id != "" && id == strings.TrimSpace(routeID)
}

func pluginManifestDeclaresAsset(manifest plugins.Manifest, logical string) bool {
	for _, asset := range manifest.Assets {
		if filepath.ToSlash(asset) == logical {
			return true
		}
	}
	return false
}

func hasPluginExtension(points []string, want string) bool {
	for _, point := range points {
		if point == want {
			return true
		}
	}
	return false
}

func joinPluginPackageFile(root, logical string) (string, error) {
	logical = strings.TrimSpace(filepath.ToSlash(logical))
	if logical == "" || path.IsAbs(logical) || !fs.ValidPath(logical) {
		return "", ErrPluginUIAssetNotFound
	}
	resolved := filepath.Join(root, filepath.FromSlash(logical))
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", ErrPluginUIAssetNotFound
	}
	return resolved, nil
}

// PluginUIAssetName maps a mounted UI path onto a package ui/ asset.
func PluginUIAssetName(suffix string) (string, error) {
	cleaned := path.Clean("/" + strings.TrimSpace(suffix))
	if cleaned == "/" || cleaned == "." {
		return "ui/index.html", nil
	}
	if cleaned == "/api" || strings.HasPrefix(cleaned, "/api/") {
		return "", ErrPluginUIAssetNotFound
	}
	logical := path.Join("ui", strings.TrimPrefix(cleaned, "/"))
	if !strings.HasPrefix(logical, "ui/") || strings.Contains(logical, "..") {
		return "", ErrPluginUIAssetNotFound
	}
	switch strings.ToLower(path.Ext(logical)) {
	case ".html", ".css", ".js":
		return logical, nil
	default:
		return "", ErrPluginUIAssetNotFound
	}
}

func MergePluginUIRoutes(live, declared []pluginhost.UIRoute) []pluginhost.UIRoute {
	merged := make([]pluginhost.UIRoute, 0, len(live)+len(declared))
	seen := make(map[string]struct{}, len(live)+len(declared))
	for _, route := range live {
		id := strings.TrimSpace(route.ID)
		if id == "" || strings.TrimSpace(route.Href) == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		merged = append(merged, route)
	}
	for _, route := range declared {
		id := strings.TrimSpace(route.ID)
		if id == "" || strings.TrimSpace(route.Href) == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		merged = append(merged, route)
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].ID < merged[j].ID })
	return merged
}

func PluginUIAssetContentType(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".css":
		return "text/css; charset=utf-8"
	case ".js":
		return "text/javascript; charset=utf-8"
	default:
		return "text/html; charset=utf-8"
	}
}
