package pluginhost

import (
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
	ID    string `json:"id"`
	Label string `json:"label"`
	Group string `json:"group"`
	Href  string `json:"href"`
}

type uiMount struct {
	route   UIRoute
	group   ResourceGroup
	handler http.Handler
}

var uiMounts = struct {
	mu   sync.RWMutex
	byID map[string]uiMount
}{byID: map[string]uiMount{}}

func UIHref(routeID string) string {
	routeID = strings.TrimSpace(routeID)
	if routeID == "" {
		return ""
	}
	return panelAPIPrefix + pluginUIPrefix + routeID + "/"
}

func declarationUIRouteID(decl Declaration) string {
	id := strings.TrimSpace(decl.UIRouteID)
	if id == "" {
		id = strings.TrimSpace(decl.PluginID)
	}
	return id
}

func Register(decl Declaration, handler http.Handler) {
	id := declarationUIRouteID(decl)
	if handler == nil || id == "" {
		if id != "" {
			unregisterMount(id)
		}
		return
	}

	mount := uiMount{}
	if hasExtension(decl.ExtensionPoints, extensionUIRoute) {
		label := metadataValue(decl.Metadata, "ui.nav.label")
		if label == "" {
			label = id
		}
		mount.route = UIRoute{
			ID:    id,
			Label: label,
			Group: metadataValue(decl.Metadata, "ui.nav.group"),
			Href:  UIHref(id),
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
		return
	}

	uiMounts.mu.Lock()
	defer uiMounts.mu.Unlock()
	uiMounts.byID[id] = mount
}

func Unregister(id string) {
	unregisterMount(strings.TrimSpace(id))
}

func unregisterMount(id string) {
	uiMounts.mu.Lock()
	defer uiMounts.mu.Unlock()
	delete(uiMounts.byID, id)
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
