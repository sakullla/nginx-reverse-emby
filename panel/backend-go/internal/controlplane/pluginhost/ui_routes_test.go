package pluginhost

import (
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
