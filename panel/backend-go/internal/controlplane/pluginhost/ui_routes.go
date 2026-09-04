package pluginhost

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
)

const (
	extensionUIRoute       = "ui.route"
	extensionResourceGroup = "resource.group"
	extensionDNSProvider   = "dns.provider"
	pluginUIPrefix         = "/plugins/"
	panelAPIPrefix         = "/panel-api"
)

// Declaration is the host-side view of plugin.yaml ui.route / resource.group.
// Values come from the plugin manifest; the panel does not invent pages.
type Declaration struct {
	PluginID        string
	ExtensionPoints []string
	UIRouteID       string
	ResourceGroupID string
	Metadata        map[string]string
}

// UIRoute is a plugin-declared panel entry.
type UIRoute struct {
	ID       string `json:"id"`
	PluginID string `json:"plugin_id"`
	Label    string `json:"label"`
	Group    string `json:"group"`
	Href     string `json:"href"`
}

type uiMount struct {
	owner   string
	route   UIRoute
	group   ResourceGroup
	handler http.Handler
}

var uiMounts = struct {
	mu   sync.RWMutex
	byID map[string]uiMount
}{byID: map[string]uiMount{}}

var ErrUIRouteConflict = errors.New("plugin UI route is owned by another plugin")

func UIHref(routeID string) string {
	routeID = strings.TrimSpace(routeID)
	if routeID == "" {
		return ""
	}
	return panelAPIPrefix + pluginUIPrefix + routeID + "/"
}

func ResolveUIRouteID(pluginID, declaredRouteID string) string {
	id := strings.TrimSpace(declaredRouteID)
	if id == "" {
		id = strings.TrimSpace(pluginID)
	}
	return id
}

func declarationUIRouteID(decl Declaration) string {
	return ResolveUIRouteID(decl.PluginID, decl.UIRouteID)
}

func ClaimUIRouteOwnership(owners map[string]string, ownerPluginID, routeID string) error {
	ownerPluginID = strings.TrimSpace(ownerPluginID)
	routeID = strings.TrimSpace(routeID)
	if owners == nil || ownerPluginID == "" || routeID == "" {
		return errors.New("plugin UI route owner and identity are required")
	}
	if owner, exists := owners[routeID]; exists && owner != ownerPluginID {
		return fmt.Errorf("%w: route %q belongs to plugin %q, not %q", ErrUIRouteConflict, routeID, owner, ownerPluginID)
	}
	owners[routeID] = ownerPluginID
	return nil
}

func currentUIRouteOwners() map[string]string {
	uiMounts.mu.RLock()
	defer uiMounts.mu.RUnlock()
	owners := make(map[string]string, len(uiMounts.byID))
	for routeID, mount := range uiMounts.byID {
		owners[routeID] = mount.owner
	}
	return owners
}

func Register(decl Declaration, handler http.Handler) error {
	owner := strings.TrimSpace(decl.PluginID)
	id := declarationUIRouteID(decl)
	if handler == nil {
		Unregister(owner, id)
		return nil
	}
	if owner == "" || id == "" {
		return errors.New("plugin UI route owner and identity are required")
	}

	mount := uiMount{owner: owner}
	if hasExtension(decl.ExtensionPoints, extensionUIRoute) {
		label := metadataValue(decl.Metadata, "ui.nav.label")
		if label == "" {
			label = id
		}
		mount.route = UIRoute{
			ID:       id,
			PluginID: owner,
			Label:    label,
			Group:    metadataValue(decl.Metadata, "ui.nav.group"),
			Href:     UIHref(id),
		}
		mount.handler = handler
	}
	if hasExtension(decl.ExtensionPoints, extensionResourceGroup) {
		groupID := strings.TrimSpace(decl.ResourceGroupID)
		ref := metadataValue(decl.Metadata, "resource.group.ref")
		if groupID != "" && ref != "" {
			label := metadataValue(decl.Metadata, "resource.group.label")
			if label == "" {
				label = groupID
			}
			mount.group = ResourceGroup{
				ID:          groupID,
				PluginID:    strings.TrimSpace(decl.PluginID),
				Ref:         ref,
				Label:       label,
				Description: metadataValue(decl.Metadata, "resource.group.description"),
				Status:      resourceGroupRegistered,
				UIRouteID:   mount.route.ID,
				UIHref:      mount.route.Href,
			}
		}
	}
	if mount.handler == nil && mount.group.ID == "" {
		return nil
	}

	uiMounts.mu.Lock()
	defer uiMounts.mu.Unlock()
	owners := make(map[string]string, 1)
	if existing, found := uiMounts.byID[id]; found {
		owners[id] = existing.owner
	}
	if err := ClaimUIRouteOwnership(owners, owner, id); err != nil {
		return err
	}
	for routeID, existing := range uiMounts.byID {
		if existing.owner == owner && routeID != id {
			delete(uiMounts.byID, routeID)
		}
	}
	uiMounts.byID[id] = mount
	return nil
}

// Unregister removes only the named owner's route. Omitting routeID retains
// compatibility for plugin-id routes while remaining owner checked.
func Unregister(ownerPluginID string, routeID ...string) bool {
	ownerPluginID = strings.TrimSpace(ownerPluginID)
	id := ResolveUIRouteID(ownerPluginID, "")
	if len(routeID) > 0 {
		id = strings.TrimSpace(routeID[0])
	}
	return unregisterMount(ownerPluginID, id)
}

func unregisterMount(ownerPluginID, id string) bool {
	uiMounts.mu.Lock()
	defer uiMounts.mu.Unlock()
	mount, found := uiMounts.byID[id]
	if !found || mount.owner != ownerPluginID {
		return false
	}
	delete(uiMounts.byID, id)
	return true
}

func Lookup(routeID string) (handler http.Handler, group ResourceGroup, ok bool) {
	uiMounts.mu.RLock()
	defer uiMounts.mu.RUnlock()
	mount, found := uiMounts.byID[strings.TrimSpace(routeID)]
	if !found || mount.handler == nil {
		return nil, ResourceGroup{}, false
	}
	return mount.handler, mount.group, true
}

func ListUIRoutes() []UIRoute {
	uiMounts.mu.RLock()
	defer uiMounts.mu.RUnlock()
	routes := make([]UIRoute, 0, len(uiMounts.byID))
	for _, mount := range uiMounts.byID {
		if mount.route.ID == "" || mount.route.Href == "" {
			continue
		}
		routes = append(routes, mount.route)
	}
	sort.Slice(routes, func(i, j int) bool { return routes[i].ID < routes[j].ID })
	return routes
}

func SplitPluginUIPath(path string) (routeID, suffix string) {
	for _, prefix := range []string{panelAPIPrefix + pluginUIPrefix, "/api" + pluginUIPrefix} {
		rest, found := strings.CutPrefix(path, prefix)
		if !found {
			continue
		}
		routeID, after, _ := strings.Cut(rest, "/")
		if routeID == "" {
			return "", ""
		}
		if after == "" && !strings.HasSuffix(path, "/") {
			return routeID, ""
		}
		if after == "" {
			return routeID, "/"
		}
		return routeID, "/" + after
	}
	return "", ""
}

func hasExtension(points []string, want string) bool {
	for _, point := range points {
		if point == want {
			return true
		}
	}
	return false
}

func metadataValue(metadata map[string]string, key string) string {
	if metadata == nil {
		return ""
	}
	return strings.TrimSpace(metadata[key])
}
