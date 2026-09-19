package pluginhost

import (
	"sort"
)

// ResourceGroup is a plugin-declared isolation boundary from
// extension_points: [resource.group], resource_group_id, and resource.group.* metadata.
type ResourceGroup struct {
	ID          string `json:"id"`
	PluginID    string `json:"plugin_id"`
	Ref         string `json:"ref"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Status      string `json:"status"`
	UIRouteID   string `json:"ui_route_id,omitempty"`
	UIHref      string `json:"ui_href,omitempty"`
}

const resourceGroupRegistered = "registered"

func ListResourceGroups() []ResourceGroup {
	uiMounts.mu.RLock()
	defer uiMounts.mu.RUnlock()
	groups := make([]ResourceGroup, 0, len(uiMounts.byID))
	for _, mount := range uiMounts.byID {
		if mount.group.ID == "" || mount.group.Ref == "" {
			continue
		}
		groups = append(groups, mount.group)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].ID < groups[j].ID })
	return groups
}
