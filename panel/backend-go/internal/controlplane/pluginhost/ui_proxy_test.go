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
		Unregister(declaration.UIRouteID)
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
	oldInstance.closePluginUIIdleConnections()

	newHandler, _, ok := Lookup(declaration.UIRouteID)
	if !ok {
		t.Fatal("new plugin UI route was not published")
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

func testPluginUIInstance(generation string, declaration Declaration, address string) *Instance {
	instance := &Instance{ID: "sample-plugin", Generation: generation}
	instance.candidate = Candidate{
		Identity:    Identity{Generation: generation},
		Declaration: declaration,
		uiEndpoint:  Endpoint{Network: "tcp", Address: address, Cookie: "cookie"},
	}
	return instance
}
