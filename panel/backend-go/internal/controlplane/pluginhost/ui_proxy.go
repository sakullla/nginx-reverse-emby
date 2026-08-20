package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

const pluginUIBodyLimit = 1 << 20

func waitPluginUIReady(ctx context.Context, endpoint Endpoint, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	readyCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var lastErr error
	for {
		request, _ := http.NewRequestWithContext(readyCtx, http.MethodGet, "http://plugin-ui"+pluginsdk.PluginUIReadyPath, nil)
		request.Header.Set(pluginsdk.HeaderPluginUICredential, endpoint.Cookie)
		response, err := pluginUIClient(endpoint).Do(request)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusNoContent {
				return nil
			}
			lastErr = fmt.Errorf("unexpected status %d", response.StatusCode)
		} else {
			lastErr = err
		}
		select {
		case <-readyCtx.Done():
			return fmt.Errorf("plugin-declared UI endpoint is not ready: %w", errors.Join(lastErr, readyCtx.Err()))
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func pluginUIClient(endpoint Endpoint) *http.Client {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, endpoint.Network, endpoint.Address)
	}}
	return &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (h *Host) publishPluginUI(instance *Instance) {
	if h == nil || instance == nil || !hasExtension(instance.candidate.Declaration.ExtensionPoints, extensionUIRoute) {
		return
	}
	Register(instance.candidate.Declaration, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		h.proxyPluginUI(instance, writer, request)
	}))
}

func (h *Host) unpublishPluginUI(instance *Instance) {
	if instance == nil || strings.TrimSpace(instance.candidate.Declaration.UIRouteID) == "" {
		return
	}
	Unregister(instance.candidate.Declaration.UIRouteID)
}

func (h *Host) proxyPluginUI(instance *Instance, writer http.ResponseWriter, request *http.Request) {
	h.mu.RLock()
	active := h.active[instance.ID] == instance
	h.mu.RUnlock()
	if !active {
		http.Error(writer, "plugin UI generation is unavailable", http.StatusServiceUnavailable)
		return
	}
	forward := request.Clone(request.Context())
	forward.RequestURI = ""
	forward.URL.Scheme = "http"
	forward.URL.Host = "plugin-ui"
	forward.Host = "plugin-ui"
	forward.Header = request.Header.Clone()
	forward.Header.Set(pluginsdk.HeaderPluginUICredential, instance.candidate.uiEndpoint.Cookie)
	forward.Body = http.MaxBytesReader(writer, request.Body, pluginUIBodyLimit)
	response, err := pluginUIClient(instance.candidate.uiEndpoint).Do(forward)
	if err != nil {
		http.Error(writer, "plugin UI is unavailable", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	for name, values := range response.Header {
		if strings.EqualFold(name, "Connection") || strings.EqualFold(name, "Transfer-Encoding") {
			continue
		}
		for _, value := range values {
			writer.Header().Add(name, value)
		}
	}
	writer.WriteHeader(response.StatusCode)
	_, _ = io.Copy(writer, io.LimitReader(response.Body, pluginUIBodyLimit))
}
