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

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/sanitize"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

const pluginUIBodyLimit = 1 << 20
const pluginUITransportIdleTimeout = 60 * time.Second

var pluginUIHopByHopHeaders = []string{
	"Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Te",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

var pluginUISessionRequestHeaders = []string{
	"Authorization",
	"Cookie",
	"X-Panel-Session",
	"X-Panel-Token",
	"X-Register-Token",
}

const (
	internalDNSResolvePath           = "/.nre/providers/dns/token"
	internalDNSEnsurePath            = "/.nre/providers/dns/records/ensure"
	internalDNSDeletePath            = "/.nre/providers/dns/records/delete"
	internalDNSProviderVersionHeader = "X-NRE-DNS-Provider-Version"
)

const (
	DNSProviderCloudflare = "cloudflare"
	DNSProviderAliyun     = "aliyun"
	DNSProviderDNSPodCN   = "dnspod-cn"
	DNSProviderDNSPodCom  = "dnspod-com"
	DNSProviderTencentDNS = "tencent-dns"
)

var (
	ErrDNSProviderUnavailable   = errors.New("DNS provider is unavailable")
	ErrDNSTokenNotMapped        = errors.New("DNS provider has no token mapping for domain")
	ErrDNSProviderNotCloudflare = errors.New("DNS provider mapping is not a Cloudflare token")
)

// DNSProviderResolution is the winning dns.provider mapping for one domain.
// Domestic providers leave Token empty; that response must not be given to a
// Cloudflare client.
type DNSProviderResolution struct {
	Provider string
	Token    string
}

// IsDomesticDNSProvider reports the four providers whose records are written
// by the plugin instead of the control-plane Cloudflare client.
func IsDomesticDNSProvider(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case DNSProviderAliyun, DNSProviderDNSPodCN, DNSProviderDNSPodCom, DNSProviderTencentDNS:
		return true
	default:
		return false
	}
}

type pluginUIHTTPClient struct {
	client    *http.Client
	transport *http.Transport
}

func waitPluginUIReady(ctx context.Context, client *http.Client, cookie string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	if client == nil {
		return errors.New("plugin UI client is required")
	}
	readyCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var lastErr error
	for {
		request, _ := http.NewRequestWithContext(readyCtx, http.MethodGet, "http://plugin-ui"+pluginsdk.PluginUIReadyPath, nil)
		request.Header.Set(pluginsdk.HeaderPluginUICredential, cookie)
		response, err := client.Do(request)
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

func newPluginUIHTTPClient(endpoint Endpoint, maxConnections int) *pluginUIHTTPClient {
	if maxConnections < 1 {
		maxConnections = 1
	}
	maxIdleConnections := min(maxConnections, 4)
	transport := &http.Transport{
		MaxIdleConns:        maxIdleConnections,
		MaxIdleConnsPerHost: maxIdleConnections,
		MaxConnsPerHost:     maxConnections,
		IdleConnTimeout:     pluginUITransportIdleTimeout,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, endpoint.Network, endpoint.Address)
		},
	}
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &pluginUIHTTPClient{client: client, transport: transport}
}

func (instance *Instance) pluginUIClient() *http.Client {
	instance.uiMu.Lock()
	defer instance.uiMu.Unlock()
	if instance.uiHTTP == nil {
		maxConnections := instance.candidate.Requirement.Budget().Processes - 4
		if maxConnections < 1 {
			maxConnections = 4
		}
		instance.uiHTTP = newPluginUIHTTPClient(instance.candidate.uiEndpoint, maxConnections)
	}
	return instance.uiHTTP.client
}

func (instance *Instance) closePluginUIIdleConnections() {
	if instance == nil {
		return
	}
	instance.uiMu.Lock()
	client := instance.uiHTTP
	instance.uiMu.Unlock()
	if client != nil {
		client.transport.CloseIdleConnections()
	}
}

func (h *Host) publishPluginUI(instance *Instance) {
	if h == nil || instance == nil || !hasExtension(instance.candidate.Declaration.ExtensionPoints, extensionUIRoute) {
		return
	}
	owner, err := runtimeUIRouteOwner(instance)
	if err != nil {
		return
	}
	mount := buildUIMount(owner, instance.candidate.Declaration, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		h.proxyPluginUI(instance, writer, request)
	}))
	if mount != nil {
		_ = registerUIMount(declarationUIRouteID(instance.candidate.Declaration), *mount)
	}
}

func (h *Host) unpublishPluginUI(instance *Instance) {
	if h == nil || instance == nil || !hasExtension(instance.candidate.Declaration.ExtensionPoints, extensionUIRoute) {
		return
	}
	routeID := declarationUIRouteID(instance.candidate.Declaration)
	if routeID == "" {
		return
	}
	owner, err := runtimeUIRouteOwner(instance)
	if err != nil {
		return
	}
	unregisterRuntimeMount(owner, routeID)
	instance.closePluginUIIdleConnections()
}

func runtimeUIRouteOwner(instance *Instance) (uiRouteOwner, error) {
	if instance == nil {
		return uiRouteOwner{}, errors.New("plugin UI runtime instance is required")
	}
	owner := uiRouteOwner{
		PluginID: strings.TrimSpace(instance.candidate.Identity.PluginID), InstanceID: strings.TrimSpace(instance.ID), Generation: strings.TrimSpace(instance.Generation),
	}
	if !owner.valid() || owner.InstanceID == "" || strings.TrimSpace(instance.candidate.Declaration.PluginID) != owner.PluginID || strings.TrimSpace(instance.candidate.Identity.Generation) != owner.Generation {
		return uiRouteOwner{}, errors.New("plugin UI runtime owner is invalid")
	}
	return owner, nil
}

func (h *Host) uiRoutePublication(previous, next *Instance) (uiRoutePublication, error) {
	publication := uiRoutePublication{}
	if previous != nil && hasExtension(previous.candidate.Declaration.ExtensionPoints, extensionUIRoute) {
		owner, err := runtimeUIRouteOwner(previous)
		if err != nil {
			return uiRoutePublication{}, err
		}
		publication.previousOwner = owner
		publication.previousRouteID = declarationUIRouteID(previous.candidate.Declaration)
	}
	if next == nil || !hasExtension(next.candidate.Declaration.ExtensionPoints, extensionUIRoute) {
		return publication, nil
	}
	owner, err := runtimeUIRouteOwner(next)
	if err != nil {
		return uiRoutePublication{}, err
	}
	publication.nextOwner = owner
	publication.nextRouteID = declarationUIRouteID(next.candidate.Declaration)
	publication.nextMount = buildUIMount(owner, next.candidate.Declaration, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		h.proxyPluginUI(next, writer, request)
	}))
	if publication.nextMount == nil {
		return uiRoutePublication{}, errors.New("plugin UI runtime mount is unavailable")
	}
	return publication, nil
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
	removePluginUIHopByHopHeaders(forward.Header)
	for _, name := range pluginUISessionRequestHeaders {
		forward.Header.Del(name)
	}
	forward.Header.Set(pluginsdk.HeaderPluginUICredential, instance.candidate.uiEndpoint.Cookie)
	forward.Body = http.MaxBytesReader(writer, request.Body, pluginUIBodyLimit)
	response, err := instance.pluginUIClient().Do(forward)
	if err != nil {
		http.Error(writer, "plugin UI is unavailable", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	responseHeader := response.Header.Clone()
	removePluginUIHopByHopHeaders(responseHeader)
	// A plugin UI shares the panel origin, so it must not create or overwrite
	// panel session cookies through its private runtime response.
	responseHeader.Del("Set-Cookie")
	responseHeader.Del("Clear-Site-Data")
	for name, values := range responseHeader {
		for _, value := range values {
			writer.Header().Add(name, value)
		}
	}
	if writer.Header().Get("Content-Security-Policy") == "" {
		pluginsdk.SetPluginUIResponseHeaders(writer.Header())
	}
	writer.WriteHeader(response.StatusCode)
	_, _ = io.Copy(writer, io.LimitReader(response.Body, pluginUIBodyLimit))
}

func removePluginUIHopByHopHeaders(header http.Header) {
	for _, value := range header.Values("Connection") {
		for _, name := range strings.Split(value, ",") {
			if name = strings.TrimSpace(name); name != "" {
				header.Del(name)
			}
		}
	}
	for _, name := range pluginUIHopByHopHeaders {
		header.Del(name)
	}
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
// Cloudflare mapping they own. A domestic mapping is not a Cloudflare token and
// must not fall through to an environment credential.
func (h *Host) ResolveDNSToken(ctx context.Context, domain string) (string, error) {
	resolved, err := h.ResolveDNSProvider(ctx, domain)
	if err != nil {
		return "", err
	}
	if IsDomesticDNSProvider(resolved.Provider) || resolved.Token == "" {
		return "", ErrDNSProviderNotCloudflare
	}
	return resolved.Token, nil
}

// ResolveDNSProvider returns the single winning mapping. An empty provider with
// a token is the legacy Cloudflare contract. More than one mapping fails closed.
func (h *Host) ResolveDNSProvider(ctx context.Context, domain string) (DNSProviderResolution, error) {
	instance, resolved, err := h.selectDNSProvider(ctx, domain)
	if err != nil {
		return DNSProviderResolution{}, err
	}
	if instance == nil {
		return DNSProviderResolution{}, ErrDNSTokenNotMapped
	}
	return resolved, nil
}

// EnsureDNSRecord asks the winning domestic provider to create or update one
// A, AAAA, or TXT record. Cloudflare and unmapped suffixes are not written.
func (h *Host) EnsureDNSRecord(ctx context.Context, domain, recordType, name, content string, ttl int) error {
	return h.mutateDNSRecord(ctx, internalDNSEnsurePath, "dns-ensure", domain, recordType, name, content, &ttl)
}

// DeleteDNSRecord asks the winning domestic provider to remove one TXT whose
// name and content both match. Other records are left untouched.
func (h *Host) DeleteDNSRecord(ctx context.Context, domain, recordType, name, content string) error {
	return h.mutateDNSRecord(ctx, internalDNSDeletePath, "dns-delete", domain, recordType, name, content, nil)
}

func (h *Host) selectDNSProvider(ctx context.Context, domain string) (*Instance, DNSProviderResolution, error) {
	if h == nil || ctx == nil {
		return nil, DNSProviderResolution{}, ErrDNSProviderUnavailable
	}
	domain = strings.ToLower(strings.TrimRight(strings.TrimSpace(domain), "."))
	domain = strings.TrimPrefix(domain, "*.")
	if domain == "" {
		return nil, DNSProviderResolution{}, errors.New("DNS token domain is required")
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
		return nil, DNSProviderResolution{}, ErrDNSProviderUnavailable
	}
	sort.Slice(instances, func(left, right int) bool { return instances[left].ID < instances[right].ID })

	var selected *Instance
	var resolved DNSProviderResolution
	for _, instance := range instances {
		hit, err := h.resolveDNSProviderFromInstance(ctx, instance, domain)
		if errors.Is(err, ErrDNSTokenNotMapped) {
			continue
		}
		if err != nil {
			return nil, DNSProviderResolution{}, err
		}
		if selected != nil {
			return nil, DNSProviderResolution{}, errors.New("multiple DNS providers mapped the same domain")
		}
		selected = instance
		resolved = hit
	}
	return selected, resolved, nil
}

func (h *Host) mutateDNSRecord(ctx context.Context, path, operation, domain, recordType, name, content string, ttl *int) error {
	instance, resolved, err := h.selectDNSProvider(ctx, domain)
	if err != nil {
		return err
	}
	if instance == nil || !IsDomesticDNSProvider(resolved.Provider) {
		return ErrDNSProviderNotCloudflare
	}
	recordType = strings.ToUpper(strings.TrimSpace(recordType))
	switch recordType {
	case "A", "AAAA", "TXT":
	default:
		return errors.New("DNS record type is not supported")
	}
	if recordType != "TXT" && ttl == nil {
		return errors.New("DNS record deletion only removes matching TXT records")
	}
	name = strings.TrimSpace(name)
	content = strings.TrimSpace(content)
	if name == "" || content == "" {
		return errors.New("DNS record name and content are required")
	}
	payload := struct {
		Domain  string `json:"domain"`
		Type    string `json:"type"`
		Name    string `json:"name"`
		Content string `json:"content"`
		TTL     int    `json:"ttl,omitempty"`
	}{
		Domain:  strings.TrimPrefix(strings.ToLower(strings.TrimRight(strings.TrimSpace(domain), ".")), "*."),
		Type:    recordType,
		Name:    name,
		Content: content,
	}
	if ttl != nil {
		payload.TTL = *ttl
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	defer clear(body)
	response, cancel, err := h.postDNSProvider(ctx, instance, path, "operation/"+operation+"/", body)
	if err != nil {
		return err
	}
	defer cancel()
	defer response.Body.Close()
	if response.Header.Get(internalDNSProviderVersionHeader) != "1" {
		return errors.New("DNS provider does not implement private token resolution contract v1")
	}
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 4096))
	if err != nil {
		return fmt.Errorf("DNS provider request: %w", err)
	}
	if response.StatusCode == http.StatusNotFound {
		return errors.New("DNS provider refused the record operation")
	}
	if response.StatusCode == http.StatusOK || response.StatusCode == http.StatusNoContent {
		h.mu.RLock()
		stillActive := h.active[instance.ID] == instance && instance.Generation == instance.candidate.Identity.Generation
		h.mu.RUnlock()
		if !stillActive {
			return ErrDNSProviderUnavailable
		}
		return nil
	}
	return dnsProviderStatusError(response.StatusCode, responseBody)
}

func (h *Host) resolveDNSProviderFromInstance(ctx context.Context, instance *Instance, domain string) (DNSProviderResolution, error) {
	body, err := json.Marshal(struct {
		Domain string `json:"domain"`
	}{Domain: domain})
	if err != nil {
		return DNSProviderResolution{}, err
	}
	defer clear(body)
	response, cancel, err := h.postDNSProvider(ctx, instance, internalDNSResolvePath, "operation/dns-resolve/", body)
	if err != nil {
		return DNSProviderResolution{}, err
	}
	defer cancel()
	defer response.Body.Close()
	if response.Header.Get(internalDNSProviderVersionHeader) != "1" {
		return DNSProviderResolution{}, errors.New("DNS provider does not implement private token resolution contract v1")
	}
	if response.StatusCode == http.StatusNotFound {
		return DNSProviderResolution{}, ErrDNSTokenNotMapped
	}
	if response.StatusCode != http.StatusOK {
		responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return DNSProviderResolution{}, dnsProviderStatusError(response.StatusCode, responseBody)
	}
	var result struct {
		Token    []byte `json:"token"`
		Provider string `json:"provider"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 8192))
	if err := decoder.Decode(&result); err != nil {
		return DNSProviderResolution{}, fmt.Errorf("decode DNS provider response: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		clear(result.Token)
		return DNSProviderResolution{}, errors.New("DNS provider returned trailing response data")
	}
	defer clear(result.Token)
	provider := strings.ToLower(strings.TrimSpace(result.Provider))
	if provider == "" {
		provider = DNSProviderCloudflare
	}
	h.mu.RLock()
	stillActive := h.active[instance.ID] == instance && instance.Generation == instance.candidate.Identity.Generation
	h.mu.RUnlock()
	if !stillActive {
		return DNSProviderResolution{}, ErrDNSProviderUnavailable
	}
	if IsDomesticDNSProvider(provider) {
		return DNSProviderResolution{Provider: provider}, nil
	}
	if provider != DNSProviderCloudflare {
		return DNSProviderResolution{}, errors.New("DNS provider returned an unknown provider")
	}
	if len(result.Token) == 0 || len(result.Token) > 4096 {
		return DNSProviderResolution{}, errors.New("DNS provider returned an invalid token")
	}
	return DNSProviderResolution{Provider: DNSProviderCloudflare, Token: string(result.Token)}, nil
}

func (h *Host) postDNSProvider(ctx context.Context, instance *Instance, path, operationPrefix string, body []byte) (*http.Response, context.CancelFunc, error) {
	deadline := instance.candidate.Deadline
	if deadline <= 0 {
		deadline = 5 * time.Second
	}
	callCtx, cancel := context.WithTimeout(ctx, deadline)
	request, err := http.NewRequestWithContext(callCtx, http.MethodPost, "http://plugin-ui"+path, bytes.NewReader(body))
	if err != nil {
		cancel()
		return nil, nil, err
	}
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
		cancel()
		return nil, nil, err
	}
	request.Header.Set("X-NRE-Operation-Key", operationPrefix+hex.EncodeToString(nonce[:]))
	response, err := instance.pluginUIClient().Do(request)
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("DNS provider request: %w", err)
	}
	return response, cancel, nil
}

func dnsProviderStatusError(status int, body []byte) error {
	if status == http.StatusNotFound {
		return ErrDNSTokenNotMapped
	}
	message := strings.TrimSpace(sanitize.Text(string(body), nil))
	if len(message) > 300 {
		message = message[:300]
	}
	if message == "" {
		return fmt.Errorf("DNS provider returned status %d", status)
	}
	return fmt.Errorf("DNS provider returned status %d: %s", status, message)
}
