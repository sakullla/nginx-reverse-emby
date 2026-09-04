package pluginhost

import (
	"errors"
	"net/http"
	"testing"
)

func testDeclaration() Declaration {
	return Declaration{
		PluginID:        "sample-plugin",
		ExtensionPoints: []string{extensionUIRoute, extensionResourceGroup},
		UIRouteID:       "sample-plugin",
		ResourceGroupID: "sample-plugin",
		Metadata: map[string]string{
			"ui.nav.group":               "基础设施",
			"ui.nav.label":               "域名 Token",
			"resource.group.ref":         "resource-group/sample-plugin",
			"resource.group.label":       "示例资源",
			"resource.group.description": "用于验证插件声明的资源组",
		},
	}
}

func TestRegisterPublishesDeclaredUIRouteAndResourceGroup(t *testing.T) {
	t.Cleanup(func() { Unregister("sample-plugin") })
	Unregister("sample-plugin")
	if got := ListUIRoutes(); len(got) != 0 {
		t.Fatalf("routes = %#v, want empty before plugin install", got)
	}
	if got := ListResourceGroups(); len(got) != 0 {
		t.Fatalf("groups = %#v, want empty before plugin install", got)
	}

	Register(testDeclaration(), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	routes := ListUIRoutes()
	if len(routes) != 1 || routes[0].ID != "sample-plugin" || routes[0].Href != "/panel-api/plugins/sample-plugin/" || routes[0].Label != "域名 Token" {
		t.Fatalf("routes = %#v", routes)
	}
	groups := ListResourceGroups()
	if len(groups) != 1 || groups[0].Ref != "resource-group/sample-plugin" || groups[0].UIHref != routes[0].Href {
		t.Fatalf("groups = %#v", groups)
	}

	Unregister("sample-plugin")
	if got := ListUIRoutes(); len(got) != 0 {
		t.Fatalf("routes = %#v, want empty after uninstall", got)
	}
	if got := ListResourceGroups(); len(got) != 0 {
		t.Fatalf("groups = %#v, want empty after uninstall", got)
	}
}

func TestRegisterSecondPluginDoesNotNeedHostCode(t *testing.T) {
	t.Cleanup(func() {
		Unregister("sample-plugin")
		Unregister("other-plugin")
	})
	Register(testDeclaration(), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	Register(Declaration{
		PluginID:        "other-plugin",
		ExtensionPoints: []string{extensionUIRoute, extensionResourceGroup},
		UIRouteID:       "other-plugin",
		ResourceGroupID: "other-plugin",
		Metadata: map[string]string{
			"ui.nav.label":       "其他",
			"ui.nav.group":       "基础设施",
			"resource.group.ref": "resource-group/other-plugin",
		},
	}, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	if got := ListUIRoutes(); len(got) != 2 {
		t.Fatalf("routes = %#v, want 2 declared plugins", got)
	}
	if got := ListResourceGroups(); len(got) != 2 {
		t.Fatalf("groups = %#v, want 2 declared groups", got)
	}
	if _, _, ok := Lookup("other-plugin"); !ok {
		t.Fatal("second plugin UI was not mounted")
	}
}

func TestRegisterDefaultsOptionalUIRouteIDToPluginID(t *testing.T) {
	t.Cleanup(func() { Unregister("default-route-plugin") })
	Register(Declaration{
		PluginID:        "default-route-plugin",
		ExtensionPoints: []string{extensionUIRoute},
		Metadata:        map[string]string{"ui.nav.label": "Default route"},
	}, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	handler, _, ok := Lookup("default-route-plugin")
	if !ok || handler == nil {
		t.Fatal("ui.route without ui_route_id did not use the manifest plugin id")
	}
	routes := ListUIRoutes()
	found := false
	for _, route := range routes {
		if route.ID == "default-route-plugin" && route.Href == "/panel-api/plugins/default-route-plugin/" {
			found = true
		}
	}
	if !found {
		t.Fatalf("default route is missing from catalog: %#v", routes)
	}
}

func TestRegisterRejectsRouteOwnedByAnotherPlugin(t *testing.T) {
	const routeID = "shared-owner-route"
	t.Cleanup(func() {
		Unregister("owner-a", routeID)
		Unregister("owner-b", routeID)
	})
	ownerA := Declaration{PluginID: "owner-a", UIRouteID: routeID, ExtensionPoints: []string{extensionUIRoute}, Metadata: map[string]string{"ui.nav.label": "Owner A"}}
	ownerB := Declaration{PluginID: "owner-b", UIRouteID: routeID, ExtensionPoints: []string{extensionUIRoute}, Metadata: map[string]string{"ui.nav.label": "Owner B"}}
	if err := Register(ownerA, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})); err != nil {
		t.Fatal(err)
	}
	if err := Register(ownerB, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})); !errors.Is(err, ErrUIRouteConflict) {
		t.Fatalf("cross-plugin route registration error = %v", err)
	}
	routes := ListUIRoutes()
	for _, route := range routes {
		if route.ID == routeID {
			if route.PluginID != ownerA.PluginID || route.Label != "Owner A" {
				t.Fatalf("route owner changed after rejected registration: %+v", route)
			}
			return
		}
	}
	t.Fatal("original owner route disappeared after rejected registration")
}

func TestRegisterSameOwnerRouteCutoverRemovesPreviousRoute(t *testing.T) {
	const oldRoute, newRoute = "owner-cutover-old", "owner-cutover-new"
	t.Cleanup(func() {
		Unregister("cutover-owner", oldRoute)
		Unregister("cutover-owner", newRoute)
	})
	if err := Register(Declaration{PluginID: "cutover-owner", UIRouteID: oldRoute, ExtensionPoints: []string{extensionUIRoute}}, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})); err != nil {
		t.Fatal(err)
	}
	if err := Register(Declaration{PluginID: "cutover-owner", UIRouteID: newRoute, ExtensionPoints: []string{extensionUIRoute}}, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := Lookup(oldRoute); ok {
		t.Fatal("same-owner route cutover retained the previous route")
	}
	if _, _, ok := Lookup(newRoute); !ok {
		t.Fatal("same-owner route cutover did not publish the replacement route")
	}
}

func TestPreparePublicationRejectsAnotherUIRouteOwner(t *testing.T) {
	const routeID = "prepared-shared-route"
	t.Cleanup(func() { Unregister("prepared-owner", routeID) })
	if err := Register(Declaration{PluginID: "prepared-owner", UIRouteID: routeID, ExtensionPoints: []string{extensionUIRoute}}, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})); err != nil {
		t.Fatal(err)
	}
	candidate := &Instance{
		ID: "prepared-collider", Generation: "generation-1",
		candidate: Candidate{Declaration: Declaration{PluginID: "prepared-collider", UIRouteID: routeID, ExtensionPoints: []string{extensionUIRoute}}},
	}
	if _, err := (&Host{}).PreparePublication([]*Instance{candidate}); !errors.Is(err, ErrUIRouteConflict) {
		t.Fatalf("prepared publication route collision error = %v", err)
	}
}

func TestSplitPluginUIPath(t *testing.T) {
	t.Parallel()
	cases := map[string][2]string{
		"/panel-api/plugins/sample-plugin":              {"sample-plugin", ""},
		"/panel-api/plugins/sample-plugin/":             {"sample-plugin", "/"},
		"/panel-api/plugins/sample-plugin/api/mappings": {"sample-plugin", "/api/mappings"},
		"/api/plugins/other/app.js":                     {"other", "/app.js"},
		"/panel-api/sample-plugin/":                     {"", ""},
	}
	for input, want := range cases {
		gotID, gotSuffix := SplitPluginUIPath(input)
		if gotID != want[0] || gotSuffix != want[1] {
			t.Fatalf("SplitPluginUIPath(%q) = %q %q, want %q %q", input, gotID, gotSuffix, want[0], want[1])
		}
	}
}

func TestPluginUIClientBoundsReusableConnections(t *testing.T) {
	t.Parallel()
	client := newPluginUIHTTPClient(Endpoint{Network: "unix", Address: "/unused"}, 8)
	if client.transport.DisableKeepAlives {
		t.Fatal("plugin UI transport disabled bounded connection reuse")
	}
	if client.transport.MaxConnsPerHost != 8 || client.transport.MaxIdleConns != 4 || client.transport.MaxIdleConnsPerHost != 4 {
		t.Fatalf("transport limits = total:%d idle:%d idle_per_host:%d", client.transport.MaxConnsPerHost, client.transport.MaxIdleConns, client.transport.MaxIdleConnsPerHost)
	}
	if client.transport.IdleConnTimeout != pluginUITransportIdleTimeout {
		t.Fatalf("idle timeout = %s", client.transport.IdleConnTimeout)
	}
}
