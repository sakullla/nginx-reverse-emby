package rpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

const ProviderHTTPBackendProviders module.ProviderRef = "plugins.http.backend-provider"

type HTTPBackendProviderIdentity struct {
	InstanceID string
	ProviderID string
	Generation string
}

type httpBackendProviderSecurity struct {
	identity   HTTPBackendProviderIdentity
	endpoint   string
	credential string
}

type httpBackendProviderAttempt struct {
	identity   HTTPBackendProviderIdentity
	endpoint   string
	credential string
	transport  *http.Transport
	peer       providerPeerIdentity
	peerMu     sync.Mutex
}

type providerPeerIdentity struct {
	PID int
	UID int
	GID int
}

func (identity providerPeerIdentity) valid() bool { return identity.PID > 0 }

type HTTPBackendProviderSet struct {
	handles map[string]*HTTPBackendProviderHandle
}

func NewHTTPBackendProviderSet(handles []*HTTPBackendProviderHandle) *HTTPBackendProviderSet {
	set := &HTTPBackendProviderSet{handles: make(map[string]*HTTPBackendProviderHandle, len(handles))}
	for _, handle := range handles {
		if handle != nil {
			set.handles[handle.InstanceID()+"\x00"+handle.ProviderID()] = handle
		}
	}
	return set
}

func (set *HTTPBackendProviderSet) Resolve(instanceID, providerID string) (*HTTPBackendProviderHandle, bool) {
	if set == nil {
		return nil, false
	}
	handle, found := set.handles[instanceID+"\x00"+providerID]
	return handle, found
}

func (set *HTTPBackendProviderSet) ProgressiveDrain() bool { return set != nil && len(set.handles) > 0 }

type HTTPBackendProviderHandle struct {
	instance   *HostedInstance
	providerID string

	mu       sync.Mutex
	draining bool
	leases   int
	empty    chan struct{}
}

func newHTTPBackendProviderHandle(instance *HostedInstance, providerID string) *HTTPBackendProviderHandle {
	return &HTTPBackendProviderHandle{instance: instance, providerID: providerID}
}

func (handle *HTTPBackendProviderHandle) InstanceID() string {
	if handle == nil || handle.instance == nil {
		return ""
	}
	return handle.instance.candidate.InstanceID
}

func (handle *HTTPBackendProviderHandle) ProviderID() string {
	if handle == nil {
		return ""
	}
	return handle.providerID
}

func (handle *HTTPBackendProviderHandle) Generation() string {
	if handle == nil || handle.instance == nil {
		return ""
	}
	return handle.instance.candidate.Generation
}

type HTTPBackendProviderLease struct {
	once   sync.Once
	handle *HTTPBackendProviderHandle
}

func (handle *HTTPBackendProviderHandle) Acquire() (*HTTPBackendProviderLease, error) {
	if handle == nil || handle.instance == nil {
		return nil, errors.New("HTTP backend provider handle is unavailable")
	}
	handle.mu.Lock()
	defer handle.mu.Unlock()
	if handle.draining {
		return nil, errors.New("HTTP backend provider generation is draining")
	}
	handle.leases++
	return &HTTPBackendProviderLease{handle: handle}, nil
}

func (lease *HTTPBackendProviderLease) Close() error {
	if lease == nil {
		return nil
	}
	lease.once.Do(func() {
		handle := lease.handle
		if handle == nil {
			return
		}
		handle.mu.Lock()
		if handle.leases > 0 {
			handle.leases--
		}
		if handle.draining && handle.leases == 0 && handle.empty != nil {
			close(handle.empty)
			handle.empty = nil
		}
		handle.mu.Unlock()
	})
	return nil
}

func (handle *HTTPBackendProviderHandle) drain(ctx context.Context) error {
	if handle == nil {
		return nil
	}
	handle.mu.Lock()
	handle.draining = true
	if handle.leases == 0 {
		handle.mu.Unlock()
		return nil
	}
	if handle.empty == nil {
		handle.empty = make(chan struct{})
	}
	empty := handle.empty
	handle.mu.Unlock()
	select {
	case <-empty:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type HTTPBackendProviderAuthority struct {
	Scheme        string
	Host          string
	ClientAddress string
}

func (authority HTTPBackendProviderAuthority) Validate() error {
	if authority.Scheme != "http" && authority.Scheme != "https" {
		return errors.New("trusted external provider scheme is invalid")
	}
	if !validProviderAuthorityHost(authority.Host) {
		return errors.New("trusted external provider host is invalid")
	}
	if authority.ClientAddress != "" && providerClientIP(authority.ClientAddress) == "" {
		return errors.New("trusted external provider client authority is invalid")
	}
	return nil
}

func (handle *HTTPBackendProviderHandle) RoundTrip(request *http.Request, authority HTTPBackendProviderAuthority) (*http.Response, error) {
	if request == nil {
		return nil, errors.New("HTTP backend provider request is required")
	}
	if err := authority.Validate(); err != nil {
		return nil, err
	}
	binding, err := handle.currentAttempt()
	if err != nil {
		return nil, err
	}
	out := request.Clone(request.Context())
	out.URL = cloneProviderURL(request.URL)
	out.URL.Scheme = "http"
	out.URL.Host = "provider.nre.internal"
	out.Host = authority.Host
	upgrade := out.Header.Get("Upgrade")
	wantsUpgrade := headerHasToken(out.Header, "Connection", "upgrade") && upgrade != ""
	stripUntrustedProviderHeaders(out.Header)
	if wantsUpgrade {
		out.Header.Set("Connection", "Upgrade")
		out.Header.Set("Upgrade", upgrade)
	}
	clientHost := providerClientIP(authority.ClientAddress)
	out.Header.Set("X-Forwarded-Proto", authority.Scheme)
	out.Header.Set("X-Forwarded-Host", authority.Host)
	if port := externalAuthorityPort(authority.Scheme, authority.Host); port != "" {
		out.Header.Set("X-Forwarded-Port", port)
	}
	if clientHost != "" {
		out.Header.Set("X-Forwarded-For", clientHost)
	}
	out.Header.Set("Forwarded", forwardedHeader(authority.Scheme, authority.Host, clientHost))
	setProviderCapabilityHeaders(out.Header, binding, false)
	response, err := binding.transport.RoundTrip(out)
	if err != nil {
		if contextErr := request.Context().Err(); contextErr != nil {
			return nil, contextErr
		}
		return nil, errors.New("HTTP backend provider is unavailable")
	}
	if response != nil {
		stripProviderResponseHeaders(response.Header)
	}
	return response, nil
}

func (handle *HTTPBackendProviderHandle) currentAttempt() (*httpBackendProviderAttempt, error) {
	if handle == nil || handle.instance == nil {
		return nil, errors.New("HTTP backend provider is unavailable")
	}
	handle.instance.mu.RLock()
	attempt := handle.instance.attempt
	state := handle.instance.status.State
	handle.instance.mu.RUnlock()
	if attempt == nil || (state != "ready" && state != "healthy") {
		return nil, errors.New("HTTP backend provider attempt is unavailable")
	}
	binding := attempt.providers[handle.providerID]
	if binding == nil || binding.identity.InstanceID != handle.InstanceID() || binding.identity.Generation != handle.Generation() {
		return nil, errors.New("HTTP backend provider attempt identity mismatch")
	}
	return binding, nil
}

func httpBackendProviderIdentities(instanceID, generation string, descriptors []pluginsdk.HTTPBackendProviderDescriptor) []HTTPBackendProviderIdentity {
	identities := make([]HTTPBackendProviderIdentity, 0, len(descriptors))
	for _, descriptor := range descriptors {
		identities = append(identities, HTTPBackendProviderIdentity{InstanceID: instanceID, ProviderID: descriptor.ID, Generation: generation})
	}
	return identities
}

func newHTTPBackendProviderAttempt(security httpBackendProviderSecurity, processGroup, sandboxUID int) *httpBackendProviderAttempt {
	binding := &httpBackendProviderAttempt{identity: security.identity, endpoint: security.endpoint, credential: security.credential}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	binding.transport = &http.Transport{
		MaxIdleConns: 16, MaxIdleConnsPerHost: 16, MaxConnsPerHost: 64,
		IdleConnTimeout: 90 * time.Second, ResponseHeaderTimeout: 30 * time.Second,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			connection, err := dialer.DialContext(ctx, "unix", security.endpoint)
			if err != nil {
				return nil, err
			}
			peer, err := validateProviderPeer(connection, processGroup, sandboxUID)
			if err != nil {
				_ = connection.Close()
				return nil, err
			}
			binding.peerMu.Lock()
			defer binding.peerMu.Unlock()
			if binding.peer.valid() && binding.peer != peer {
				_ = connection.Close()
				return nil, errors.New("HTTP backend provider peer changed within an attempt")
			}
			binding.peer = peer
			return &providerIdleConn{Conn: connection, timeout: 2 * time.Minute}, nil
		},
	}
	return binding
}

type providerIdleConn struct {
	net.Conn
	timeout time.Duration
}

func (connection *providerIdleConn) Read(payload []byte) (int, error) {
	if connection.timeout > 0 {
		_ = connection.Conn.SetReadDeadline(time.Now().Add(connection.timeout))
	}
	return connection.Conn.Read(payload)
}

func (connection *providerIdleConn) Write(payload []byte) (int, error) {
	if connection.timeout > 0 {
		_ = connection.Conn.SetWriteDeadline(time.Now().Add(connection.timeout))
	}
	return connection.Conn.Write(payload)
}

func (binding *httpBackendProviderAttempt) ready(ctx context.Context) error {
	if binding == nil || binding.transport == nil {
		return errors.New("HTTP backend provider binding is unavailable")
	}
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://provider.nre.internal"+pluginsdk.HTTPBackendProviderReadyPath, nil)
	setProviderCapabilityHeaders(request.Header, binding, true)
	response, err := binding.transport.RoundTrip(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent || response.Header.Get(pluginsdk.HeaderHTTPBackendProviderInstance) != binding.identity.InstanceID ||
		response.Header.Get(pluginsdk.HeaderHTTPBackendProviderID) != binding.identity.ProviderID ||
		response.Header.Get(pluginsdk.HeaderHTTPBackendProviderGeneration) != binding.identity.Generation {
		return errors.New("HTTP backend provider readiness identity mismatch")
	}
	return nil
}

func setProviderCapabilityHeaders(header http.Header, binding *httpBackendProviderAttempt, probe bool) {
	stripProviderCapabilityHeaders(header)
	header.Set(pluginsdk.HeaderHTTPBackendProviderCredential, binding.credential)
	header.Set(pluginsdk.HeaderHTTPBackendProviderInstance, binding.identity.InstanceID)
	header.Set(pluginsdk.HeaderHTTPBackendProviderID, binding.identity.ProviderID)
	header.Set(pluginsdk.HeaderHTTPBackendProviderGeneration, binding.identity.Generation)
	if probe {
		header.Set(pluginsdk.HeaderHTTPBackendProviderProbe, "ready-v1")
	}
}

func stripProviderCapabilityHeaders(header http.Header) {
	for _, name := range []string{
		pluginsdk.HeaderHTTPBackendProviderCredential,
		pluginsdk.HeaderHTTPBackendProviderInstance,
		pluginsdk.HeaderHTTPBackendProviderID,
		pluginsdk.HeaderHTTPBackendProviderGeneration,
		pluginsdk.HeaderHTTPBackendProviderProbe,
	} {
		header.Del(name)
	}
}

func stripUntrustedProviderHeaders(header http.Header) {
	for _, connection := range header.Values("Connection") {
		for _, name := range strings.Split(connection, ",") {
			if name = strings.TrimSpace(name); name != "" {
				header.Del(name)
			}
		}
	}
	for key := range header {
		canonical := http.CanonicalHeaderKey(key)
		if canonical == "Forwarded" || strings.HasPrefix(canonical, "X-Forwarded-") || strings.HasPrefix(canonical, "X-Nre-Provider-") || isProviderHopHeader(canonical) {
			header.Del(key)
		}
	}
}

func isProviderHopHeader(name string) bool {
	switch name {
	case "Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade":
		return true
	default:
		return false
	}
}

func headerHasToken(header http.Header, key, token string) bool {
	for _, value := range header.Values(key) {
		for _, item := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(item), token) {
				return true
			}
		}
	}
	return false
}

func stripProviderResponseHeaders(header http.Header) {
	for key := range header {
		if strings.HasPrefix(http.CanonicalHeaderKey(key), "X-Nre-Provider-") {
			header.Del(key)
		}
	}
}

func cloneProviderURL(source *url.URL) *url.URL {
	if source == nil {
		return &url.URL{}
	}
	clone := *source
	return &clone
}

func externalAuthorityPort(scheme, authority string) string {
	if _, port, err := net.SplitHostPort(authority); err == nil {
		return port
	}
	if scheme == "https" {
		return "443"
	}
	return "80"
}

func forwardedHeader(scheme, host, client string) string {
	parts := make([]string, 0, 3)
	if client != "" {
		parts = append(parts, "for="+providerQuotedString(client))
	}
	parts = append(parts, "proto="+scheme, "host="+providerQuotedString(host))
	return strings.Join(parts, ";")
}

func validProviderAuthorityHost(authority string) bool {
	if authority == "" || authority != strings.TrimSpace(authority) || strings.ContainsAny(authority, "\r\n\x00;,\\\"@/?#") {
		return false
	}
	parsed, err := url.Parse("//" + authority)
	if err != nil || parsed.Host != authority || parsed.Hostname() == "" || parsed.User != nil || parsed.Path != "" {
		return false
	}
	if port := parsed.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return false
		}
	}
	return true
}

func providerClientIP(authority string) string {
	if authority == "" {
		return ""
	}
	if address := net.ParseIP(authority); address != nil {
		return address.String()
	}
	host, _, err := net.SplitHostPort(authority)
	if err != nil {
		return ""
	}
	address := net.ParseIP(host)
	if address == nil {
		return ""
	}
	return address.String()
}

func providerQuotedString(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\"", "\\\"")
	return "\"" + value + "\""
}

type providerLeaseRelease struct {
	once  sync.Once
	lease io.Closer
	stop  func() bool
}

func (release *providerLeaseRelease) finish() error {
	var err error
	release.once.Do(func() {
		if release.stop != nil {
			release.stop()
		}
		if release.lease != nil {
			err = release.lease.Close()
		}
	})
	return err
}

type providerLeaseReadCloser struct {
	io.ReadCloser
	release *providerLeaseRelease
}

func (body *providerLeaseReadCloser) Read(payload []byte) (int, error) {
	n, err := body.ReadCloser.Read(payload)
	if err != nil {
		_ = body.release.finish()
	}
	return n, err
}

func (body *providerLeaseReadCloser) Close() error {
	return errors.Join(body.ReadCloser.Close(), body.release.finish())
}

type providerLeaseReadWriteCloser struct {
	io.ReadWriteCloser
	release *providerLeaseRelease
}

func (body *providerLeaseReadWriteCloser) Read(payload []byte) (int, error) {
	n, err := body.ReadWriteCloser.Read(payload)
	if err != nil {
		_ = body.release.finish()
	}
	return n, err
}

func (body *providerLeaseReadWriteCloser) Close() error {
	return errors.Join(body.ReadWriteCloser.Close(), body.release.finish())
}

func WrapHTTPBackendProviderResponseLease(ctx context.Context, response *http.Response, lease io.Closer) {
	if response == nil || lease == nil {
		return
	}
	release := &providerLeaseRelease{lease: lease}
	if ctx != nil {
		release.stop = context.AfterFunc(ctx, func() { _ = release.finish() })
	}
	if response.Body == nil {
		_ = release.finish()
		return
	}
	if readWrite, ok := response.Body.(io.ReadWriteCloser); ok {
		response.Body = &providerLeaseReadWriteCloser{ReadWriteCloser: readWrite, release: release}
		return
	}
	response.Body = &providerLeaseReadCloser{ReadCloser: response.Body, release: release}
}

func providerObservationKey(instanceID, providerID string) string {
	return "plugin-provider:" + instanceID + ":" + providerID
}

func ProviderObservationKey(instanceID, providerID string) string {
	return providerObservationKey(instanceID, providerID)
}

func providerSyntheticURL(instanceID, providerID string) *url.URL {
	return &url.URL{Scheme: "http", Host: "provider.nre.internal", Path: "/", RawQuery: "", Fragment: "", User: nil}
}

func ProviderSyntheticURL(instanceID, providerID string) *url.URL {
	return providerSyntheticURL(instanceID, providerID)
}

func closeProviderAttempts(attempt *hostAttempt) {
	if attempt == nil {
		return
	}
	for _, provider := range attempt.providers {
		if provider != nil && provider.transport != nil {
			provider.transport.CloseIdleConnections()
		}
	}
}

func (attempt *hostAttempt) readyHTTPBackendProviders(ctx context.Context) error {
	if attempt == nil {
		return errors.New("HTTP backend provider attempt is unavailable")
	}
	for _, provider := range attempt.providers {
		var err error
		deadline := time.Now().Add(10 * time.Second)
		for {
			err = provider.ready(ctx)
			if err == nil || ctx.Err() != nil || time.Now().After(deadline) {
				break
			}
			timer := time.NewTimer(20 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
			case <-timer.C:
			}
		}
		if err != nil {
			return fmt.Errorf("HTTP backend provider %q endpoint is not ready", provider.identity.ProviderID)
		}
	}
	return nil
}
