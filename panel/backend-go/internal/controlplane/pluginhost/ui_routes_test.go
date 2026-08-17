package pluginhost

import (
	"net/http"
	"testing"
)

func testDeclaration() Declaration {
	return Declaration{
		PluginID:        "cloudflare-dns",
		ExtensionPoints: []string{extensionUIRoute, extensionResourceGroup},
		UIRouteID:       "cloudflare-dns",
		ResourceGroupID: "cloudflare-dns",
		Metadata: map[string]string{
			"ui.nav.group":               "基础设施",
			"ui.nav.label":               "域名 Token",
			"resource.group.ref":         "resource-group/cloudflare-dns",
			"resource.group.label":       "Cloudflare DNS",
			"resource.group.description": "按域名后缀隔离 Token 映射",
		},
	}
}

func TestRegisterPublishesDeclaredUIRouteAndResourceGroup(t *testing.T) {
	t.Cleanup(func() { Unregister("cloudflare-dns") })
	Unregister("cloudflare-dns")
	if got := ListUIRoutes(); len(got) != 0 {
		t.Fatalf("routes = %#v, want empty before plugin install", got)
	}
	if got := ListResourceGroups(); len(got) != 0 {
		t.Fatalf("groups = %#v, want empty before plugin install", got)
	}

	Register(testDeclaration(), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	routes := ListUIRoutes()
	if len(routes) != 1 || routes[0].ID != "cloudflare-dns" || routes[0].Href != "/panel-api/plugins/cloudflare-dns/" || routes[0].Label != "域名 Token" {
		t.Fatalf("routes = %#v", routes)
	}
	groups := ListResourceGroups()
	if len(groups) != 1 || groups[0].Ref != "resource-group/cloudflare-dns" || groups[0].UIHref != routes[0].Href {
		t.Fatalf("groups = %#v", groups)
	}

	Unregister("cloudflare-dns")
	if got := ListUIRoutes(); len(got) != 0 {
		t.Fatalf("routes = %#v, want empty after uninstall", got)
	}
	if got := ListResourceGroups(); len(got) != 0 {
		t.Fatalf("groups = %#v, want empty after uninstall", got)
	}
}

func TestRegisterSecondPluginDoesNotNeedHostCode(t *testing.T) {
	t.Cleanup(func() {
		Unregister("cloudflare-dns")
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

func TestSplitPluginUIPath(t *testing.T) {
	t.Parallel()
	cases := map[string][2]string{
		"/panel-api/plugins/cloudflare-dns":              {"cloudflare-dns", ""},
		"/panel-api/plugins/cloudflare-dns/":             {"cloudflare-dns", "/"},
		"/panel-api/plugins/cloudflare-dns/api/mappings": {"cloudflare-dns", "/api/mappings"},
		"/api/plugins/other/app.js":                      {"other", "/app.js"},
		"/panel-api/cloudflare-dns/":                     {"", ""},
	}
	for input, want := range cases {
		gotID, gotSuffix := SplitPluginUIPath(input)
		if gotID != want[0] || gotSuffix != want[1] {
			t.Fatalf("SplitPluginUIPath(%q) = %q %q, want %q %q", input, gotID, gotSuffix, want[0], want[1])
		}
	}
}
