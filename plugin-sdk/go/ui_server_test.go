package pluginsdk

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPluginUIMiddlewareAuthenticatesAndStripsCredential(t *testing.T) {
	called := false
	handler := authenticatedPluginUI("secret", http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		called = true
		if request.Header.Get(HeaderPluginUICredential) != "" {
			t.Fatal("plugin handler received Host credential")
		}
		writer.WriteHeader(http.StatusOK)
	}))
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, httptest.NewRequest(http.MethodGet, "/", nil))
	if denied.Code != http.StatusForbidden || called {
		t.Fatalf("denied status=%d called=%v", denied.Code, called)
	}
	readyRequest := httptest.NewRequest(http.MethodGet, PluginUIReadyPath, nil)
	readyRequest.Header.Set(HeaderPluginUICredential, "secret")
	ready := httptest.NewRecorder()
	handler.ServeHTTP(ready, readyRequest)
	if ready.Code != http.StatusNoContent || called {
		t.Fatalf("ready status=%d called=%v", ready.Code, called)
	}
	businessRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	businessRequest.Header.Set(HeaderPluginUICredential, "secret")
	business := httptest.NewRecorder()
	handler.ServeHTTP(business, businessRequest)
	if business.Code != http.StatusOK || !called {
		t.Fatalf("business status=%d called=%v", business.Code, called)
	}
}
