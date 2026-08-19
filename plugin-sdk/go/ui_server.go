package pluginsdk

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
)

const (
	EnvPluginUIEndpoint      = "NRE_PLUGIN_UI_ENDPOINT"
	HeaderPluginUICredential = "X-NRE-Plugin-UI-Credential"
	PluginUIReadyPath        = "/.nre/plugin-ui/ready"
)

// ServePluginUI exposes a plugin-owned HTTP handler on the private,
// attempt-scoped endpoint provisioned by the Host for ui.route.
func ServePluginUI(ctx context.Context, handler http.Handler) error {
	if ctx == nil || handler == nil {
		return errors.New("plugin UI context and handler are required")
	}
	rawEndpoint := strings.TrimSpace(os.Getenv(EnvPluginUIEndpoint))
	network, address, ok := strings.Cut(rawEndpoint, ":")
	if !ok || network != "unix" || strings.TrimSpace(address) == "" {
		return errors.New("plugin UI private unix endpoint is required")
	}
	cookieFile := strings.TrimSpace(os.Getenv(EnvPluginCookieFile))
	credential, err := os.ReadFile(cookieFile)
	if err != nil || strings.TrimSpace(string(credential)) == "" {
		return fmt.Errorf("read plugin UI credential: %w", err)
	}
	listener, err := net.Listen(network, address)
	if err != nil {
		return err
	}
	defer listener.Close()
	server := &http.Server{Handler: authenticatedPluginUI(strings.TrimSpace(string(credential)), handler)}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	select {
	case <-ctx.Done():
		_ = server.Shutdown(context.Background())
		err = <-done
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case err = <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func authenticatedPluginUI(credential string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		provided := request.Header.Get(HeaderPluginUICredential)
		request.Header.Del(HeaderPluginUICredential)
		if provided != credential {
			http.Error(writer, "plugin UI authorization denied", http.StatusForbidden)
			return
		}
		if request.URL.Path == PluginUIReadyPath {
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(writer, request)
	})
}
