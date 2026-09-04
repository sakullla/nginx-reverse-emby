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
	owner   uiRouteOwner
	route   UIRoute
	group   ResourceGroup
	handler http.Handler
}

type uiRouteOwner struct {
	PluginID   string
	InstanceID string
	Generation string
}

func (owner uiRouteOwner) valid() bool {
	return owner.PluginID != "" && owner.PluginID == strings.TrimSpace(owner.PluginID) &&
		owner.InstanceID == strings.TrimSpace(owner.InstanceID) && owner.Generation == strings.TrimSpace(owner.Generation) &&
		(owner.InstanceID == "") == (owner.Generation == "")
}

func sameUIRouteRuntime(left, right uiRouteOwner) bool {
	if left.InstanceID == "" || right.InstanceID == "" {
		return left == right
	}
	return left.PluginID == right.PluginID && left.InstanceID == right.InstanceID
}

type uiRoutePublication struct {
	previousOwner   uiRouteOwner
	previousRouteID string
	nextOwner       uiRouteOwner
	nextRouteID     string
	nextMount       *uiMount
}

type uiRouteReservation struct {
	token        uint64
	routes       []string
	publications []uiRoutePublication
}

var uiMounts = struct {
	mu              sync.RWMutex
	byID            map[string]uiMount
	reserved        map[string]uint64
	nextReservation uint64
}{byID: map[string]uiMount{}, reserved: map[string]uint64{}}

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

func Register(decl Declaration, handler http.Handler) error {
	owner := uiRouteOwner{PluginID: strings.TrimSpace(decl.PluginID)}
	id := declarationUIRouteID(decl)
	if handler == nil {
		Unregister(owner.PluginID, id)
		return nil
	}
	if !owner.valid() || id == "" {
		return errors.New("plugin UI route owner and identity are required")
	}
	mount := buildUIMount(owner, decl, handler)
	if mount == nil {
		return nil
	}
	return registerUIMount(id, *mount)
}

func registerUIMount(id string, mount uiMount) error {
	uiMounts.mu.Lock()
	defer uiMounts.mu.Unlock()
	if _, reserved := uiMounts.reserved[id]; reserved {
		return fmt.Errorf("%w: route %q has a pending publication", ErrUIRouteConflict, id)
	}
	if existing, found := uiMounts.byID[id]; found && !sameUIRouteRuntime(existing.owner, mount.owner) {
		return uiRouteOwnershipConflict(id, existing.owner, mount.owner)
	}
	for routeID, existing := range uiMounts.byID {
		if sameUIRouteRuntime(existing.owner, mount.owner) && routeID != id {
			if _, reserved := uiMounts.reserved[routeID]; reserved {
				return fmt.Errorf("%w: previous route %q has a pending publication", ErrUIRouteConflict, routeID)
			}
			delete(uiMounts.byID, routeID)
		}
	}
	uiMounts.byID[id] = mount
	return nil
}

func buildUIMount(owner uiRouteOwner, decl Declaration, handler http.Handler) *uiMount {
	id := declarationUIRouteID(decl)
	mount := &uiMount{owner: owner}
	if hasExtension(decl.ExtensionPoints, extensionUIRoute) && handler != nil {
		label := metadataValue(decl.Metadata, "ui.nav.label")
		if label == "" {
			label = id
		}
		mount.route = UIRoute{ID: id, PluginID: owner.PluginID, Label: label, Group: metadataValue(decl.Metadata, "ui.nav.group"), Href: UIHref(id)}
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
			mount.group = ResourceGroup{ID: groupID, PluginID: owner.PluginID, Ref: ref, Label: label, Description: metadataValue(decl.Metadata, "resource.group.description"), Status: resourceGroupRegistered, UIRouteID: mount.route.ID, UIHref: mount.route.Href}
		}
	}
	if mount.handler == nil && mount.group.ID == "" {
		return nil
	}
	return mount
}

func uiRouteOwnershipConflict(routeID string, current, requested uiRouteOwner) error {
	return fmt.Errorf("%w: route %q belongs to plugin %q instance %q generation %q, not plugin %q instance %q generation %q", ErrUIRouteConflict, routeID, current.PluginID, current.InstanceID, current.Generation, requested.PluginID, requested.InstanceID, requested.Generation)
}

func reserveUIRoutePublication(publications []uiRoutePublication) (*uiRouteReservation, error) {
	uiMounts.mu.Lock()
	defer uiMounts.mu.Unlock()
	lockedRoutes := make(map[string]struct{})
	desired := make(map[string]uiRouteOwner)
	desiredPublications := make(map[string]uiRoutePublication)
	for _, publication := range publications {
		if publication.previousRouteID != "" {
			lockedRoutes[publication.previousRouteID] = struct{}{}
		}
		if publication.nextMount == nil {
			continue
		}
		if !publication.nextOwner.valid() || publication.nextOwner.InstanceID == "" || publication.nextRouteID == "" {
			return nil, errors.New("plugin UI route runtime owner is invalid")
		}
		if current, exists := desired[publication.nextRouteID]; exists && !sameUIRouteRuntime(current, publication.nextOwner) {
			return nil, uiRouteOwnershipConflict(publication.nextRouteID, current, publication.nextOwner)
		}
		desired[publication.nextRouteID] = publication.nextOwner
		desiredPublications[publication.nextRouteID] = publication
		lockedRoutes[publication.nextRouteID] = struct{}{}
	}
	for routeID := range lockedRoutes {
		if _, reserved := uiMounts.reserved[routeID]; reserved {
			return nil, fmt.Errorf("%w: route %q has a concurrent publication", ErrUIRouteConflict, routeID)
		}
	}
	for _, publication := range publications {
		if publication.previousRouteID == "" {
			continue
		}
		if current, exists := uiMounts.byID[publication.previousRouteID]; exists && current.owner != publication.previousOwner {
			return nil, uiRouteOwnershipConflict(publication.previousRouteID, current.owner, publication.previousOwner)
		}
	}
	for routeID, owner := range desired {
		current, exists := uiMounts.byID[routeID]
		if !exists {
			continue
		}
		publication := desiredPublications[routeID]
		replacesPrevious := publication.previousRouteID == routeID && current.owner == publication.previousOwner
		if current.owner != owner && !replacesPrevious {
			return nil, uiRouteOwnershipConflict(routeID, current.owner, owner)
		}
	}
	routes := make([]string, 0, len(lockedRoutes))
	for routeID := range lockedRoutes {
		routes = append(routes, routeID)
	}
	sort.Strings(routes)
	uiMounts.nextReservation++
	if uiMounts.nextReservation == 0 {
		uiMounts.nextReservation++
	}
	token := uiMounts.nextReservation
	for _, routeID := range routes {
		uiMounts.reserved[routeID] = token
	}
	return &uiRouteReservation{token: token, routes: routes, publications: append([]uiRoutePublication(nil), publications...)}, nil
}

func (reservation *uiRouteReservation) publish(activate func()) bool {
	if reservation == nil || activate == nil {
		return false
	}
	uiMounts.mu.Lock()
	defer uiMounts.mu.Unlock()
	for _, routeID := range reservation.routes {
		if uiMounts.reserved[routeID] != reservation.token {
			return false
		}
	}
	activate()
	for _, publication := range reservation.publications {
		if publication.previousRouteID != "" {
			if current, found := uiMounts.byID[publication.previousRouteID]; found && current.owner == publication.previousOwner {
				delete(uiMounts.byID, publication.previousRouteID)
			}
		}
		if publication.nextMount != nil {
			uiMounts.byID[publication.nextRouteID] = *publication.nextMount
		}
	}
	reservation.releaseLocked()
	return true
}

func (reservation *uiRouteReservation) abort() {
	if reservation == nil {
		return
	}
	uiMounts.mu.Lock()
	defer uiMounts.mu.Unlock()
	reservation.releaseLocked()
}

func (reservation *uiRouteReservation) releaseLocked() {
	for _, routeID := range reservation.routes {
		if uiMounts.reserved[routeID] == reservation.token {
			delete(uiMounts.reserved, routeID)
		}
	}
}

func unregisterRuntimeMount(owner uiRouteOwner, routeID string) bool {
	uiMounts.mu.Lock()
	defer uiMounts.mu.Unlock()
	if _, reserved := uiMounts.reserved[routeID]; reserved {
		return false
	}
	mount, found := uiMounts.byID[routeID]
	if !found || mount.owner != owner {
		return false
	}
	delete(uiMounts.byID, routeID)
	return true
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
	owner := uiRouteOwner{PluginID: strings.TrimSpace(ownerPluginID)}
	uiMounts.mu.Lock()
	defer uiMounts.mu.Unlock()
	if _, reserved := uiMounts.reserved[id]; reserved {
		return false
	}
	mount, found := uiMounts.byID[id]
	if !found || mount.owner != owner {
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
