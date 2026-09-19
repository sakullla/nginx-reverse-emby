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
		candidate: Candidate{Identity: Identity{PluginID: "prepared-collider", Generation: "generation-1"}, Declaration: Declaration{PluginID: "prepared-collider", UIRouteID: routeID, ExtensionPoints: []string{extensionUIRoute}}},
	}
	host := &Host{prepared: map[*Instance]struct{}{candidate: {}}}
	if _, err := host.PreparePublication([]*Instance{candidate}); !errors.Is(err, ErrUIRouteConflict) {
		t.Fatalf("prepared publication route collision error = %v", err)
	}
}

func TestPreparePublicationRejectsSamePluginMultiInstanceRoute(t *testing.T) {
	const routeID = "same-plugin-route"
	declaration := Declaration{PluginID: "multi-instance-plugin", UIRouteID: routeID, ExtensionPoints: []string{extensionUIRoute}}
	first := &Instance{ID: "instance-a", Generation: "generation-a", candidate: Candidate{Identity: Identity{PluginID: declaration.PluginID, Generation: "generation-a"}, Declaration: declaration}}
	second := &Instance{ID: "instance-b", Generation: "generation-b", candidate: Candidate{Identity: Identity{PluginID: declaration.PluginID, Generation: "generation-b"}, Declaration: declaration}}
	host := &Host{prepared: map[*Instance]struct{}{first: {}, second: {}}}
	if _, err := host.PreparePublication([]*Instance{first, second}); !errors.Is(err, ErrUIRouteConflict) {
		t.Fatalf("same-plugin multi-instance route error = %v", err)
	}
	if len(host.active) != 0 {
		t.Fatalf("route conflict changed active view: %+v", host.active)
	}
}

func TestConcurrentHostsReserveRouteForOnlyOneInstance(t *testing.T) {
	const routeID = "concurrent-plugin-route"
	declaration := Declaration{PluginID: "concurrent-plugin", UIRouteID: routeID, ExtensionPoints: []string{extensionUIRoute}}
	first := &Instance{ID: "instance-a", Generation: "generation-a", candidate: Candidate{Identity: Identity{PluginID: declaration.PluginID, Generation: "generation-a"}, Declaration: declaration}}
	second := &Instance{ID: "instance-b", Generation: "generation-b", candidate: Candidate{Identity: Identity{PluginID: declaration.PluginID, Generation: "generation-b"}, Declaration: declaration}}
	firstHost := &Host{prepared: map[*Instance]struct{}{first: {}}}
	secondHost := &Host{prepared: map[*Instance]struct{}{second: {}}}
	type result struct {
		publication *PreparedPublication
		err         error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	prepare := func(host *Host, instance *Instance) {
		<-start
		publication, err := host.PreparePublication([]*Instance{instance})
		results <- result{publication: publication, err: err}
	}
	go prepare(firstHost, first)
	go prepare(secondHost, second)
	close(start)

	firstResult, secondResult := <-results, <-results
	winners, conflicts := 0, 0
	for _, candidate := range []result{firstResult, secondResult} {
		switch {
		case candidate.err == nil && candidate.publication != nil:
			winners++
			candidate.publication.Abort()
		case errors.Is(candidate.err, ErrUIRouteConflict):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent publication result: publication=%p err=%v", candidate.publication, candidate.err)
		}
	}
	if winners != 1 || conflicts != 1 {
		t.Fatalf("concurrent route reservations winners=%d conflicts=%d", winners, conflicts)
	}
	if len(firstHost.active) != 0 || len(secondHost.active) != 0 {
		t.Fatal("route reservation changed an active view before publish")
	}
	third := &Instance{ID: "instance-c", Generation: "generation-c", candidate: Candidate{Identity: Identity{PluginID: declaration.PluginID, Generation: "generation-c"}, Declaration: declaration}}
	thirdHost := &Host{prepared: map[*Instance]struct{}{third: {}}}
	publication, err := thirdHost.PreparePublication([]*Instance{third})
	if err != nil {
		t.Fatalf("aborted route reservation was not released: %v", err)
	}
	publication.Abort()
}

func TestReservedRoutePublishesWithActiveView(t *testing.T) {
	const routeID = "atomic-active-route"
	declaration := Declaration{PluginID: "atomic-plugin", UIRouteID: routeID, ExtensionPoints: []string{extensionUIRoute}}
	instance := &Instance{
		ID: "atomic-instance", Generation: "generation-a", State: "active",
		candidate: Candidate{Identity: Identity{PluginID: declaration.PluginID, Generation: "generation-a"}, Declaration: declaration},
	}
	host := &Host{active: make(map[string]*Instance), prepared: map[*Instance]struct{}{instance: {}}}
	publication, err := host.PreparePublication([]*Instance{instance})
	if err != nil {
		t.Fatal(err)
	}
	if host.active[instance.ID] != nil {
		publication.Abort()
		t.Fatal("route reservation changed active view before publish")
	}
	if _, _, ok := Lookup(routeID); ok {
		publication.Abort()
		t.Fatal("reserved route became visible before publish")
	}
	publication.Publish()
	if host.active[instance.ID] != instance {
		t.Fatal("publish did not advance active view")
	}
	if _, _, ok := Lookup(routeID); !ok {
		t.Fatal("publish did not commit reserved route")
	}
	if err := host.Stop(t.Context(), instance.ID); err != nil {
		t.Fatal(err)
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
