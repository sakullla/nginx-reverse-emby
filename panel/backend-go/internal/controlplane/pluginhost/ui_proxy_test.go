//go:build !integration

package pluginhost

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestPluginUIClientReusesAndClosesIdleConnection(t *testing.T) {
	t.Parallel()
	var opened atomic.Int32
	closed := make(chan struct{}, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		switch state {
		case http.StateNew:
			opened.Add(1)
		case http.StateClosed:
			select {
			case closed <- struct{}{}:
			default:
			}
		}
	}
	server.Start()
	defer server.Close()

	pooled := newPluginUIHTTPClient(Endpoint{Network: "tcp", Address: server.Listener.Addr().String()}, 4)
	for range 1000 {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://plugin-ui/", nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := pooled.client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		if err := response.Body.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if got := opened.Load(); got != 1 {
		t.Fatalf("opened connections = %d, want 1 reused connection", got)
	}
	pooled.transport.CloseIdleConnections()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("idle plugin UI connection was not closed")
	}
}

func TestPluginUIRouteCutoverKeepsOldInflightRequest(t *testing.T) {
	oldEntered := make(chan struct{})
	oldRelease := make(chan struct{})
	oldServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		close(oldEntered)
		<-oldRelease
		_, _ = io.WriteString(writer, "old")
	}))
	defer oldServer.Close()
	defer func() {
		select {
		case <-oldRelease:
		default:
			close(oldRelease)
		}
	}()
	newServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, "new")
	}))
	defer newServer.Close()

	declaration := testDeclaration()
	oldInstance := testPluginUIInstance("generation-old", declaration, oldServer.Listener.Addr().String())
	newInstance := testPluginUIInstance("generation-new", declaration, newServer.Listener.Addr().String())
	host := &Host{active: map[string]*Instance{oldInstance.ID: oldInstance}}
	t.Cleanup(func() {
		host.unpublishPluginUI(oldInstance)
		host.unpublishPluginUI(newInstance)
		oldInstance.closePluginUIIdleConnections()
		newInstance.closePluginUIIdleConnections()
	})
	host.publishPluginUI(oldInstance)
	oldHandler, _, ok := Lookup(declaration.UIRouteID)
	if !ok {
		t.Fatal("old plugin UI route was not published")
	}

	oldResponse := httptest.NewRecorder()
	oldDone := make(chan struct{})
	go func() {
		defer close(oldDone)
		oldHandler.ServeHTTP(oldResponse, httptest.NewRequest(http.MethodGet, "http://panel/", nil))
	}()
	select {
	case <-oldEntered:
	case <-time.After(time.Second):
		t.Fatal("old generation request did not enter")
	}

	host.mu.Lock()
	host.active[oldInstance.ID] = newInstance
	host.mu.Unlock()
	host.publishPluginUI(newInstance)
	host.unpublishPluginUI(oldInstance)

	newHandler, _, ok := Lookup(declaration.UIRouteID)
	if !ok {
		t.Fatal("old generation unpublish removed the new generation route")
	}
	newResponse := httptest.NewRecorder()
	newHandler.ServeHTTP(newResponse, httptest.NewRequest(http.MethodGet, "http://panel/", nil))
	if newResponse.Code != http.StatusOK || newResponse.Body.String() != "new" {
		t.Fatalf("new generation response = status %d body %q", newResponse.Code, newResponse.Body.String())
	}

	close(oldRelease)
	select {
	case <-oldDone:
	case <-time.After(time.Second):
		t.Fatal("old in-flight request did not drain")
	}
	if oldResponse.Code != http.StatusOK || oldResponse.Body.String() != "old" {
		t.Fatalf("old generation response = status %d body %q", oldResponse.Code, oldResponse.Body.String())
	}
}

func TestPluginHostStopUnregistersDefaultUIRoute(t *testing.T) {
	declaration := Declaration{PluginID: "default-route-plugin", ExtensionPoints: []string{extensionUIRoute}}
	t.Cleanup(func() { Unregister("default-route-plugin") })
	instance := &Instance{
		ID: "default-route-plugin", Generation: "generation-1", State: "active",
		candidate: Candidate{Identity: Identity{PluginID: declaration.PluginID, Generation: "generation-1"}, Declaration: declaration},
	}
	host := &Host{active: map[string]*Instance{instance.ID: instance}}
	host.publishPluginUI(instance)
	if _, _, ok := Lookup("default-route-plugin"); !ok {
		t.Fatal("default plugin-id UI route was not published")
	}

	if err := host.Stop(t.Context(), instance.ID); err != nil {
		t.Fatalf("stop plugin host: %v", err)
	}
	if _, _, ok := Lookup("default-route-plugin"); ok {
		t.Fatal("stopped plugin retained its default plugin-id UI route")
	}
}

func TestPluginHostStopNonUIPluginKeepsAnotherExplicitRoute(t *testing.T) {
	const routeID = "shared-route"
	t.Cleanup(func() { Unregister("ui-owner", routeID) })
	Register(Declaration{
		PluginID: "ui-owner", UIRouteID: routeID, ExtensionPoints: []string{extensionUIRoute},
	}, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, "owner")
	}))
	if _, _, ok := Lookup(routeID); !ok {
		t.Fatal("explicit owner route was not published")
	}

	nonUI := &Instance{
		ID: routeID, Generation: "generation-1", State: "active",
		candidate: Candidate{Identity: Identity{Generation: "generation-1"}, Declaration: Declaration{PluginID: routeID}},
	}
	host := &Host{active: map[string]*Instance{nonUI.ID: nonUI}}
	if err := host.Stop(t.Context(), nonUI.ID); err != nil {
		t.Fatalf("stop non-UI plugin host: %v", err)
	}
	handler, _, ok := Lookup(routeID)
	if !ok {
		t.Fatal("stopping non-UI plugin removed another plugin's explicit route")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://panel/", nil))
	if response.Body.String() != "owner" {
		t.Fatalf("explicit route owner response = %q", response.Body.String())
	}
}

func TestPluginHostStopCannotRemoveAnotherOwnersExplicitRoute(t *testing.T) {
	const routeID = "owned-shared-route"
	t.Cleanup(func() { Unregister("route-owner", routeID) })
	if err := Register(Declaration{PluginID: "route-owner", UIRouteID: routeID, ExtensionPoints: []string{extensionUIRoute}}, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, "owner")
	})); err != nil {
		t.Fatal(err)
	}

	colliding := &Instance{
		ID: "other-plugin", Generation: "generation-1", State: "active",
		candidate: Candidate{Identity: Identity{PluginID: "other-plugin", Generation: "generation-1"}, Declaration: Declaration{PluginID: "other-plugin", UIRouteID: routeID, ExtensionPoints: []string{extensionUIRoute}}},
	}
	host := &Host{active: map[string]*Instance{colliding.ID: colliding}}
	if err := host.Stop(t.Context(), colliding.ID); err != nil {
		t.Fatal(err)
	}
	handler, _, ok := Lookup(routeID)
	if !ok {
		t.Fatal("one plugin removed another owner's route during stop")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://panel/", nil))
	if response.Body.String() != "owner" {
		t.Fatalf("route owner response = %q", response.Body.String())
	}
}

func TestPluginHostStopCannotRemoveSamePluginOtherInstanceRoute(t *testing.T) {
	const routeID = "same-plugin-owned-route"
	declaration := Declaration{PluginID: "shared-plugin", UIRouteID: routeID, ExtensionPoints: []string{extensionUIRoute}}
	owner := &Instance{
		ID: "instance-a", Generation: "generation-a", State: "active",
		candidate: Candidate{Identity: Identity{PluginID: declaration.PluginID, Generation: "generation-a"}, Declaration: declaration},
	}
	ownerHost := &Host{active: map[string]*Instance{owner.ID: owner}}
	ownerHost.publishPluginUI(owner)
	t.Cleanup(func() { ownerHost.unpublishPluginUI(owner) })
	if _, _, ok := Lookup(routeID); !ok {
		t.Fatal("owning instance route was not published")
	}

	other := &Instance{
		ID: "instance-b", Generation: "generation-b", State: "active",
		candidate: Candidate{Identity: Identity{PluginID: declaration.PluginID, Generation: "generation-b"}, Declaration: declaration},
	}
	otherHost := &Host{active: map[string]*Instance{other.ID: other}}
	if err := otherHost.Stop(t.Context(), other.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := Lookup(routeID); !ok {
		t.Fatal("same-plugin non-owning instance removed the live route")
	}
}

func TestPluginUIProxyDoesNotCrossPanelSessionBoundary(t *testing.T) {
	t.Parallel()
	received := make(chan http.Header, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		received <- request.Header.Clone()
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Set-Cookie", "nre_panel_token=stolen; Path=/")
		writer.Header().Set("Clear-Site-Data", `"cookies"`)
		writer.Header().Set("X-Plugin-Response", "kept")
		_, _ = io.WriteString(writer, `{}`)
	}))
	defer server.Close()

	instance := testPluginUIInstance("generation-1", testDeclaration(), server.Listener.Addr().String())
	instance.candidate.uiEndpoint.Cookie = "private-cookie"
	host := &Host{active: map[string]*Instance{instance.ID: instance}}
	request := httptest.NewRequest(http.MethodGet, "http://panel/api/items", nil)
	request.Header.Set("Authorization", "Bearer panel-session")
	request.Header.Set("Cookie", "nre_panel_token=panel-secret")
	request.Header.Set("X-Panel-Session", "panel-session")
	request.Header.Set("X-Panel-Token", "panel-secret")
	request.Header.Set("X-Register-Token", "register-secret")
	request.Header.Set("X-Client-Actor", "kept")
	response := httptest.NewRecorder()
	host.proxyPluginUI(instance, response, request)

	proxied := <-received
	for _, name := range pluginUISessionRequestHeaders {
		if value := proxied.Get(name); value != "" {
			t.Fatalf("plugin received panel credential header %s=%q", name, value)
		}
	}
	if got := proxied.Get(pluginsdk.HeaderPluginUICredential); got != "private-cookie" {
		t.Fatalf("private plugin credential = %q", got)
	}
	if got := proxied.Get("X-Client-Actor"); got != "kept" {
		t.Fatalf("unrelated request header = %q", got)
	}
	if cookies := response.Result().Cookies(); len(cookies) != 0 {
		t.Fatalf("plugin response set panel-origin cookies: %+v", cookies)
	}
	if got := response.Header().Get("Clear-Site-Data"); got != "" {
		t.Fatalf("plugin response retained Clear-Site-Data %q", got)
	}
	if got := response.Header().Get("X-Plugin-Response"); got != "kept" {
		t.Fatalf("ordinary response header = %q", got)
	}
	if got := response.Header().Get("Content-Security-Policy"); got == "" {
		t.Fatal("plugin response omitted host isolation policy")
	}
}

func testPluginUIInstance(generation string, declaration Declaration, address string) *Instance {
	instance := &Instance{ID: "sample-plugin", Generation: generation}
	instance.candidate = Candidate{
		Identity:    Identity{PluginID: declaration.PluginID, Generation: generation},
		Declaration: declaration,
		uiEndpoint:  Endpoint{Network: "tcp", Address: address, Cookie: "cookie"},
	}
	return instance
}
