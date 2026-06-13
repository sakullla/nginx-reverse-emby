package http

import (
	"context"
	"crypto/tls"
	"fmt"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay/relayplan"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay/relayroute"
	"net"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync"
)

type relayPathDialer struct {
	provider RelayMaterialProvider
}

func (d relayPathDialer) DialPath(ctx context.Context, req relayplan.Request, path relayplan.Path) (net.Conn, relay.DialResult, error) {
	options := relay.DialOptions{}
	if len(req.Options) > 0 {
		options = req.Options[0]
	}
	if options.OverlayProvider == nil {
		options.OverlayProvider = relay.DefaultOverlayRuntimeProvider()
	}
	return relay.DialWithResult(ctx, req.Network, req.Target, path.Hops, d.provider, options)
}

func newRelayTransports(
	rule model.HTTPRule,
	relayListenersByID map[int]model.RelayListener,
	provider RelayMaterialProvider,
	finalHopDialer relay.FinalHopDialer,
	base *http.Transport,
	cache *model.Cache,
) (*http.Transport, *http.Transport, *http.Transport, error) {
	if provider == nil {
		return nil, nil, nil, fmt.Errorf("http rule %q: relay_layers requires relay tls material provider", rule.FrontendURL)
	}
	paths, err := resolveRelayPaths(rule, mapValues(relayListenersByID), "")
	if err != nil {
		return nil, nil, nil, err
	}
	paths = relayroute.ClonePathsWithoutKeys(paths)
	_ = finalHopDialer
	racer := newRelayPathRacer(provider, cache)
	dial := func(ctx context.Context, network, addr string, class model.TrafficClass) (net.Conn, error) {
		target := dialAddressFromContext(ctx, addr)
		result, err := racer.Race(ctx, relayplan.Request{
			Network: network,
			Target:  target,
			Paths:   paths,
			Options: []relay.DialOptions{{
				TrafficClass:    class,
				EgressProfileID: rule.EgressProfileID,
			}},
		})
		if result.DialResult.SelectedAddress != "" {
			setSelectedRelaySelection(ctx, result.DialResult.SelectedAddress, result.Selected.IDs)
		}
		return result.Conn, err
	}
	transport := NewRelayTransport(base, dial)
	interactive, bulk := NewClassedRelayTransports(base, dial)
	return transport, interactive, bulk, nil
}

func newRelayPathRacer(provider RelayMaterialProvider, cache *model.Cache) relayplan.Racer {
	return relayplan.Racer{Dialer: relayPathDialer{provider: provider}, Cache: cache, Concurrency: 3, MaxPaths: 32}
}

func resolveRelayHops(rule model.HTTPRule, relayListeners []model.RelayListener) ([]relay.Hop, error) {
	paths, err := resolveRelayPaths(rule, relayListeners, "")
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, nil
	}
	return paths[0].Hops, nil
}

func ruleUsesRelay(rule model.HTTPRule) bool {
	return relayroute.UsesRelay(nil, rule.RelayLayers)
}

func resolveRelayPaths(rule model.HTTPRule, relayListeners []model.RelayListener, target string) ([]relayplan.Path, error) {
	return relayroute.ResolvePaths(fmt.Sprintf("http rule %q", rule.FrontendURL), nil, rule.RelayLayers, relayListeners, target)
}

func cloneRelayPlanPaths(paths []relayplan.Path) []relayplan.Path {
	return relayroute.ClonePaths(paths)
}

type dialAddressContextKey struct{}
type selectedRelayAddressContextKey struct{}

type selectedRelayAddressHolder struct {
	mu      sync.Mutex
	address string
	path    []int
}

type selectedRelayConn struct {
	net.Conn
	address string
	path    []int
}

func newSelectedRelayConn(conn net.Conn, address string, path []int) net.Conn {
	address = strings.TrimSpace(address)
	if conn == nil || address == "" {
		return conn
	}
	return &selectedRelayConn{
		Conn:    conn,
		address: address,
		path:    append([]int(nil), path...),
	}
}

func (c *selectedRelayConn) selectedRelaySelection() (string, []int) {
	if c == nil || strings.TrimSpace(c.address) == "" {
		return "", nil
	}
	return strings.TrimSpace(c.address), append([]int(nil), c.path...)
}

func (c *selectedRelayConn) ConnectionState() tls.ConnectionState {
	if c != nil {
		if stateConn, ok := c.Conn.(interface{ ConnectionState() tls.ConnectionState }); ok {
			return stateConn.ConnectionState()
		}
	}
	return tls.ConnectionState{}
}

func withDialAddress(ctx context.Context, address string) context.Context {
	address = strings.TrimSpace(address)
	if ctx == nil || address == "" {
		return ctx
	}
	return context.WithValue(ctx, dialAddressContextKey{}, address)
}

func dialAddressFromContext(ctx context.Context, fallback string) string {
	if ctx != nil {
		if address, ok := ctx.Value(dialAddressContextKey{}).(string); ok && strings.TrimSpace(address) != "" {
			return strings.TrimSpace(address)
		}
	}
	return strings.TrimSpace(fallback)
}

func withSelectedRelayAddressHolder(ctx context.Context, holder *selectedRelayAddressHolder) context.Context {
	if ctx == nil || holder == nil {
		return ctx
	}
	return context.WithValue(ctx, selectedRelayAddressContextKey{}, holder)
}

func withSelectedRelayConnTrace(ctx context.Context, holder *selectedRelayAddressHolder) context.Context {
	if ctx == nil || holder == nil {
		return ctx
	}
	trace := &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) {
			if selectedAddress, selectedPath := selectedRelaySelectionFromConn(info.Conn); selectedAddress != "" {
				holder.set(selectedAddress, selectedPath)
			}
		},
	}
	return httptrace.WithClientTrace(ctx, trace)
}

func setSelectedRelaySelection(ctx context.Context, address string, path []int) {
	if ctx == nil {
		return
	}
	holder, ok := ctx.Value(selectedRelayAddressContextKey{}).(*selectedRelayAddressHolder)
	if !ok || holder == nil {
		return
	}
	holder.set(address, path)
}

func selectedRelaySelectionFromContext(ctx context.Context) (string, []int) {
	if ctx != nil {
		if holder, ok := ctx.Value(selectedRelayAddressContextKey{}).(*selectedRelayAddressHolder); ok && holder != nil {
			return holder.get()
		}
	}
	return "", nil
}

func selectedRelaySelectionFromConn(conn net.Conn) (string, []int) {
	if tlsConn, ok := conn.(*tls.Conn); ok {
		return selectedRelaySelectionFromConn(tlsConn.NetConn())
	}
	if selected, ok := conn.(interface{ selectedRelaySelection() (string, []int) }); ok {
		return selected.selectedRelaySelection()
	}
	return "", nil
}

func (h *selectedRelayAddressHolder) set(address string, path []int) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.address = strings.TrimSpace(address)
	h.path = append([]int(nil), path...)
}

func (h *selectedRelayAddressHolder) get() (string, []int) {
	if h == nil {
		return "", nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if strings.TrimSpace(h.address) == "" {
		return "", nil
	}
	return strings.TrimSpace(h.address), append([]int(nil), h.path...)
}

func requestContext(req *http.Request) context.Context {
	if req == nil {
		return nil
	}
	return req.Context()
}

func mapValues(values map[int]model.RelayListener) []model.RelayListener {
	out := make([]model.RelayListener, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}
