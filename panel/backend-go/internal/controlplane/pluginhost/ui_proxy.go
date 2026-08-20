package pluginhost

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

const pluginUIBodyLimit = 1 << 20

const internalDNSResolvePath = "/.nre/providers/dns/token"
const internalDNSProviderVersionHeader = "X-NRE-DNS-Provider-Version"

var (
	ErrDNSProviderUnavailable = errors.New("DNS provider is unavailable")
	ErrDNSTokenNotMapped      = errors.New("DNS provider has no token mapping for domain")
)

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
	if strings.HasPrefix(request.URL.Path, "/.nre/") {
		http.NotFound(writer, request)
		return
	}
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

// HasActiveDNSProvider reports whether at least one published control-plane
// plugin declares the dns.provider extension and has a private service endpoint.
func (h *Host) HasActiveDNSProvider() bool {
	if h == nil {
		return false
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, instance := range h.active {
		if instance != nil && hasExtension(instance.candidate.Declaration.ExtensionPoints, extensionDNSProvider) && strings.TrimSpace(instance.candidate.uiEndpoint.Address) != "" {
			return true
		}
	}
	return false
}

// ResolveDNSToken asks published dns.provider instances for the longest-suffix
// mapping they own. Provider traffic stays on the authenticated private Unix
// endpoint and the reserved path is never exposed through the panel UI proxy.
func (h *Host) ResolveDNSToken(ctx context.Context, domain string) (string, error) {
	if h == nil || ctx == nil {
		return "", ErrDNSProviderUnavailable
	}
	domain = strings.ToLower(strings.TrimRight(strings.TrimSpace(domain), "."))
	domain = strings.TrimPrefix(domain, "*.")
	if domain == "" {
		return "", errors.New("DNS token domain is required")
	}
	h.mu.RLock()
	instances := make([]*Instance, 0)
	for _, instance := range h.active {
		if instance != nil && hasExtension(instance.candidate.Declaration.ExtensionPoints, extensionDNSProvider) && strings.TrimSpace(instance.candidate.uiEndpoint.Address) != "" {
			instances = append(instances, instance)
		}
	}
	h.mu.RUnlock()
	if len(instances) == 0 {
		return "", ErrDNSProviderUnavailable
	}
	sort.Slice(instances, func(left, right int) bool { return instances[left].ID < instances[right].ID })

	resolved := ""
	for _, instance := range instances {
		token, err := h.resolveDNSTokenFromInstance(ctx, instance, domain)
		if errors.Is(err, ErrDNSTokenNotMapped) {
			continue
		}
		if err != nil {
			return "", err
		}
		if resolved != "" {
			return "", errors.New("multiple DNS providers mapped the same domain")
		}
		resolved = token
	}
	if resolved == "" {
		return "", ErrDNSTokenNotMapped
	}
	return resolved, nil
}

func (h *Host) resolveDNSTokenFromInstance(ctx context.Context, instance *Instance, domain string) (string, error) {
	deadline := instance.candidate.Deadline
	if deadline <= 0 {
		deadline = 5 * time.Second
	}
	callCtx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	body, err := json.Marshal(struct {
		Domain string `json:"domain"`
	}{Domain: domain})
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(callCtx, http.MethodPost, "http://plugin-ui"+internalDNSResolvePath, bytes.NewReader(body))
	if err != nil {
		clear(body)
		return "", err
	}
	defer clear(body)
	request.Header.Set("Content-Type", "application/json")
	request.Close = true
	request.Header.Set(pluginsdk.HeaderPluginUICredential, instance.candidate.uiEndpoint.Cookie)
	request.Header.Set("X-NRE-Actor", "system/dns-provider")
	resourceGroupRef := metadataValue(instance.candidate.Declaration.Metadata, "resource.group.ref")
	if resourceGroupRef == "" {
		resourceGroupRef = instance.candidate.ResourceGroupID
	}
	request.Header.Set("X-NRE-Resource-Group", resourceGroupRef)
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	request.Header.Set("X-NRE-Operation-Key", "operation/dns-resolve/"+hex.EncodeToString(nonce[:]))
	response, err := pluginUIClient(instance.candidate.uiEndpoint).Do(request)
	if err != nil {
		return "", fmt.Errorf("DNS provider request: %w", err)
	}
	defer response.Body.Close()
	if response.Header.Get(internalDNSProviderVersionHeader) != "1" {
		return "", errors.New("DNS provider does not implement private token resolution contract v1")
	}
	if response.StatusCode == http.StatusNotFound {
		return "", ErrDNSTokenNotMapped
	}
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("DNS provider returned status %d", response.StatusCode)
	}
	var result struct {
		Token []byte `json:"token"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 8192))
	if err := decoder.Decode(&result); err != nil {
		return "", fmt.Errorf("decode DNS provider response: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return "", errors.New("DNS provider returned trailing response data")
	}
	defer clear(result.Token)
	if len(result.Token) == 0 || len(result.Token) > 4096 {
		return "", errors.New("DNS provider returned an invalid token")
	}
	h.mu.RLock()
	stillActive := h.active[instance.ID] == instance && instance.Generation == instance.candidate.Identity.Generation
	h.mu.RUnlock()
	if !stillActive {
		return "", ErrDNSProviderUnavailable
	}
	return string(result.Token), nil
}
