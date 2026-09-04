package pluginsdk

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	HTTPBackendProviderEndpointConfigVersion = "nre.http-backend-provider.endpoint.v1"
	EnvHTTPBackendProviderConfigFile         = "NRE_PLUGIN_HTTP_BACKEND_PROVIDER_CONFIG_FILE"
	EnvHTTPBackendProviderEndpointDirectory  = "NRE_PLUGIN_HTTP_BACKEND_PROVIDER_ENDPOINT_DIRECTORY"
	HTTPBackendProviderReadyPath             = "/.nre/http-backend-provider/ready"

	HeaderHTTPBackendProviderCredential = "X-NRE-Provider-Credential"
	HeaderHTTPBackendProviderInstance   = "X-NRE-Provider-Instance"
	HeaderHTTPBackendProviderID         = "X-NRE-Provider-ID"
	HeaderHTTPBackendProviderGeneration = "X-NRE-Provider-Generation"
	HeaderHTTPBackendProviderProbe      = "X-NRE-Provider-Probe"
)

// HTTPBackendProviderEndpoint is an attempt-local capability. It is supplied
// by the Host through a protected file and must never be persisted in plugin
// configuration, manifests, responses, or logs.
type HTTPBackendProviderEndpoint struct {
	InstanceID string `json:"instance_id"`
	ProviderID string `json:"provider_id"`
	Generation string `json:"generation"`
	Endpoint   string `json:"endpoint"`
	Credential string `json:"credential"`
}

type HTTPBackendProviderEndpointConfig struct {
	Version   string                        `json:"version"`
	Providers []HTTPBackendProviderEndpoint `json:"providers"`
}

func (config HTTPBackendProviderEndpointConfig) Validate() error {
	if config.Version != HTTPBackendProviderEndpointConfigVersion || len(config.Providers) == 0 || len(config.Providers) > 16 {
		return errors.New("HTTP backend provider endpoint config is invalid")
	}
	seen := make(map[string]struct{}, len(config.Providers))
	for index, endpoint := range config.Providers {
		if err := ValidatePolicyIdentity(endpoint.InstanceID); err != nil {
			return fmt.Errorf("HTTP backend provider endpoint %d instance: %w", index, err)
		}
		if err := validateHTTPBackendProviderID(endpoint.ProviderID); err != nil {
			return fmt.Errorf("HTTP backend provider endpoint %d provider: %w", index, err)
		}
		if err := ValidatePolicyIdentity(endpoint.Generation); err != nil {
			return fmt.Errorf("HTTP backend provider endpoint %d generation: %w", index, err)
		}
		if strings.TrimSpace(endpoint.Endpoint) == "" || strings.ContainsAny(endpoint.Endpoint, "\r\n\x00") || len(endpoint.Credential) < 32 || strings.ContainsAny(endpoint.Credential, "\r\n\x00") {
			return fmt.Errorf("HTTP backend provider endpoint %d capability is invalid", index)
		}
		key := endpoint.InstanceID + "\x00" + endpoint.ProviderID
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("HTTP backend provider endpoint %d is duplicated", index)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func LoadHTTPBackendProviderEndpointConfig() (HTTPBackendProviderEndpointConfig, error) {
	path := strings.TrimSpace(os.Getenv(EnvHTTPBackendProviderConfigFile))
	if path == "" || !filepath.IsAbs(path) {
		return HTTPBackendProviderEndpointConfig{}, errors.New("HTTP backend provider endpoint config file is required")
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return HTTPBackendProviderEndpointConfig{}, fmt.Errorf("read HTTP backend provider endpoint config: %w", err)
	}
	var config HTTPBackendProviderEndpointConfig
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return HTTPBackendProviderEndpointConfig{}, fmt.Errorf("decode HTTP backend provider endpoint config: %w", err)
	}
	if err := ensureJSONDecoderEOF(decoder); err != nil {
		return HTTPBackendProviderEndpointConfig{}, fmt.Errorf("decode HTTP backend provider endpoint config: %w", err)
	}
	if err := config.Validate(); err != nil {
		return HTTPBackendProviderEndpointConfig{}, err
	}
	endpointDirectory := strings.TrimSpace(os.Getenv(EnvHTTPBackendProviderEndpointDirectory))
	if endpointDirectory == "" || !filepath.IsAbs(endpointDirectory) {
		return HTTPBackendProviderEndpointConfig{}, errors.New("HTTP backend provider endpoint directory is required")
	}
	for index := range config.Providers {
		name := config.Providers[index].Endpoint
		if filepath.IsAbs(name) || filepath.Base(name) != name || name == "." {
			return HTTPBackendProviderEndpointConfig{}, fmt.Errorf("HTTP backend provider endpoint %d name is invalid", index)
		}
		config.Providers[index].Endpoint = filepath.Join(endpointDirectory, name)
	}
	return config, nil
}

// ServeHTTPBackendProviders binds every Host-provisioned private endpoint and
// serves the matching package-local handler until ctx is canceled. HTTP body
// bytes use net/http directly and never pass through the lifecycle RPC ABI.
func ServeHTTPBackendProviders(ctx context.Context, handlers map[string]http.Handler) error {
	config, err := LoadHTTPBackendProviderEndpointConfig()
	if err != nil {
		return err
	}
	return ServeHTTPBackendProviderConfig(ctx, config, handlers)
}

func ServeHTTPBackendProviderConfig(ctx context.Context, config HTTPBackendProviderEndpointConfig, handlers map[string]http.Handler) error {
	if ctx == nil {
		return errors.New("HTTP backend provider context is required")
	}
	if err := config.Validate(); err != nil {
		return err
	}
	for index, endpoint := range config.Providers {
		if !filepath.IsAbs(endpoint.Endpoint) {
			return fmt.Errorf("HTTP backend provider endpoint %d is not resolved", index)
		}
	}
	type runningServer struct {
		server   *http.Server
		listener net.Listener
	}
	running := make([]runningServer, 0, len(config.Providers))
	for _, endpoint := range config.Providers {
		handler := handlers[endpoint.ProviderID]
		if handler == nil {
			for _, item := range running {
				_ = item.listener.Close()
			}
			return fmt.Errorf("HTTP backend provider %q has no handler", endpoint.ProviderID)
		}
		if stat, statErr := os.Lstat(endpoint.Endpoint); statErr == nil {
			if stat.Mode()&os.ModeSocket == 0 {
				for _, item := range running {
					_ = item.listener.Close()
				}
				return fmt.Errorf("HTTP backend provider %q endpoint exists and is not a socket", endpoint.ProviderID)
			}
			if removeErr := os.Remove(endpoint.Endpoint); removeErr != nil {
				return fmt.Errorf("remove stale HTTP backend provider %q socket: %w", endpoint.ProviderID, removeErr)
			}
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return fmt.Errorf("inspect HTTP backend provider %q endpoint: %w", endpoint.ProviderID, statErr)
		}
		listener, listenErr := net.Listen("unix", endpoint.Endpoint)
		if listenErr != nil {
			for _, item := range running {
				_ = item.listener.Close()
			}
			return fmt.Errorf("listen HTTP backend provider %q: %w", endpoint.ProviderID, listenErr)
		}
		if chmodErr := os.Chmod(endpoint.Endpoint, 0o600); chmodErr != nil {
			_ = listener.Close()
			for _, item := range running {
				_ = item.listener.Close()
			}
			return fmt.Errorf("protect HTTP backend provider %q endpoint: %w", endpoint.ProviderID, chmodErr)
		}
		server := &http.Server{
			Handler:           authenticatedHTTPBackendProvider(endpoint, handler),
			ReadHeaderTimeout: 5 * time.Second,
			IdleTimeout:       90 * time.Second,
		}
		running = append(running, runningServer{server: server, listener: listener})
	}

	errCh := make(chan error, len(running))
	var wg sync.WaitGroup
	for _, item := range running {
		wg.Add(1)
		go func(item runningServer) {
			defer wg.Done()
			if serveErr := item.server.Serve(item.listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) && !errors.Is(serveErr, net.ErrClosed) {
				errCh <- serveErr
			}
		}(item)
	}

	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-errCh:
	}
	for _, item := range running {
		_ = item.server.Close()
	}
	wg.Wait()
	for _, endpoint := range config.Providers {
		_ = os.Remove(endpoint.Endpoint)
	}
	return serveErr
}

func authenticatedHTTPBackendProvider(endpoint HTTPBackendProviderEndpoint, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		credential := request.Header.Get(HeaderHTTPBackendProviderCredential)
		probe := request.Header.Get(HeaderHTTPBackendProviderProbe) == "ready-v1"
		valid := subtle.ConstantTimeCompare([]byte(credential), []byte(endpoint.Credential)) == 1 &&
			request.Header.Get(HeaderHTTPBackendProviderInstance) == endpoint.InstanceID &&
			request.Header.Get(HeaderHTTPBackendProviderID) == endpoint.ProviderID &&
			request.Header.Get(HeaderHTTPBackendProviderGeneration) == endpoint.Generation
		stripHTTPBackendProviderHeaders(request.Header)
		if !valid {
			http.Error(w, "provider capability rejected", http.StatusUnauthorized)
			return
		}
		if request.URL.Path == HTTPBackendProviderReadyPath && probe {
			w.Header().Set(HeaderHTTPBackendProviderInstance, endpoint.InstanceID)
			w.Header().Set(HeaderHTTPBackendProviderID, endpoint.ProviderID)
			w.Header().Set(HeaderHTTPBackendProviderGeneration, endpoint.Generation)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, request)
	})
}

func stripHTTPBackendProviderHeaders(header http.Header) {
	header.Del(HeaderHTTPBackendProviderCredential)
	header.Del(HeaderHTTPBackendProviderInstance)
	header.Del(HeaderHTTPBackendProviderID)
	header.Del(HeaderHTTPBackendProviderGeneration)
	header.Del(HeaderHTTPBackendProviderProbe)
}
