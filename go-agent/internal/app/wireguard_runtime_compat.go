package app

import (
	"context"
	"net"
	"net/netip"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
	modulewireguard "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/wireguard"
)

func newSharedWireGuardRuntime() *modulewireguard.Runtime {
	return modulewireguard.NewRuntime(nil)
}

func newSharedWireGuardRuntimeWithFactory(factory modulewireguard.Factory) *modulewireguard.Runtime {
	return modulewireguard.NewRuntime(factory)
}

func cloneWireGuardProfiles(profiles []model.WireGuardProfile) []model.WireGuardProfile {
	return modulewireguard.CloneWireGuardProfiles(profiles)
}

type relayWireGuardProvider interface {
	WireGuardRuntime(profileID int) (relay.WireGuardRuntime, bool)
}

type wireGuardRuntimeProvider struct {
	runtime *modulewireguard.Runtime
	agentID string
}

func newWireGuardRuntimeProvider(runtime *modulewireguard.Runtime, agentID string) wireGuardRuntimeProvider {
	return wireGuardRuntimeProvider{
		runtime: runtime,
		agentID: strings.TrimSpace(agentID),
	}
}

func (p wireGuardRuntimeProvider) WireGuardRuntime(profileID int) (relay.WireGuardRuntime, bool) {
	if p.runtime == nil {
		return nil, false
	}
	var runtime modulewireguard.RuntimeHandle
	if p.agentID != "" {
		runtime, _ = p.runtime.RuntimeForAgent(p.agentID, profileID)
	} else {
		runtime, _ = p.runtime.Runtime(profileID)
	}
	if runtime == nil {
		return nil, false
	}
	return wireGuardRuntimeHandleAdapter{runtime: runtime}, true
}

func (p wireGuardRuntimeProvider) WireGuardRuntimeForAgent(agentID string, profileID int) (relay.WireGuardRuntime, bool) {
	if p.runtime == nil {
		return nil, false
	}
	runtime, ok := p.runtime.RuntimeForAgent(agentID, profileID)
	if !ok || runtime == nil {
		return nil, false
	}
	return wireGuardRuntimeHandleAdapter{runtime: runtime}, true
}

func (p wireGuardRuntimeProvider) WireGuardRuntimeForHop(hop relay.Hop) (relay.WireGuardRuntime, bool) {
	if p.runtime == nil {
		return nil, false
	}
	if hop.Listener.WireGuardProfileID != nil && *hop.Listener.WireGuardProfileID > 0 {
		if runtime, ok := p.WireGuardRuntimeForAgent(hop.Listener.AgentID, *hop.Listener.WireGuardProfileID); ok {
			return runtime, true
		}
	}
	profile, ok := wireGuardProfileForRelayHop(p.runtime.Profiles(), p.agentID, hop)
	if !ok {
		return nil, false
	}
	runtime, ok := p.runtime.RuntimeForAgent(profile.AgentID, profile.ID)
	if !ok || runtime == nil {
		return nil, false
	}
	return wireGuardRuntimeHandleAdapter{runtime: runtime}, true
}

type wireGuardTransactionProvider struct {
	transaction *modulewireguard.Transaction
	agentID     string
	profiles    []model.WireGuardProfile
}

func (p wireGuardTransactionProvider) WireGuardRuntime(profileID int) (relay.WireGuardRuntime, bool) {
	if p.transaction == nil {
		return nil, false
	}
	var runtime modulewireguard.RuntimeHandle
	if p.agentID != "" {
		runtime, _ = p.transaction.RuntimeForAgent(p.agentID, profileID)
	} else {
		runtime, _ = p.transaction.Runtime(profileID)
	}
	if runtime == nil {
		return nil, false
	}
	return wireGuardRuntimeHandleAdapter{runtime: runtime}, true
}

func (p wireGuardTransactionProvider) WireGuardRuntimeForAgent(agentID string, profileID int) (relay.WireGuardRuntime, bool) {
	if p.transaction == nil {
		return nil, false
	}
	runtime, ok := p.transaction.RuntimeForAgent(agentID, profileID)
	if !ok || runtime == nil {
		return nil, false
	}
	return wireGuardRuntimeHandleAdapter{runtime: runtime}, true
}

func (p wireGuardTransactionProvider) WireGuardRuntimeForHop(hop relay.Hop) (relay.WireGuardRuntime, bool) {
	if p.transaction == nil {
		return nil, false
	}
	if hop.Listener.WireGuardProfileID != nil && *hop.Listener.WireGuardProfileID > 0 {
		if runtime, ok := p.WireGuardRuntimeForAgent(hop.Listener.AgentID, *hop.Listener.WireGuardProfileID); ok {
			return runtime, true
		}
	}
	profile, ok := wireGuardProfileForRelayHop(p.profiles, p.agentID, hop)
	if !ok {
		return nil, false
	}
	runtime, ok := p.transaction.RuntimeForAgent(profile.AgentID, profile.ID)
	if !ok || runtime == nil {
		return nil, false
	}
	return wireGuardRuntimeHandleAdapter{runtime: runtime}, true
}

type wireGuardRuntimeHandleAdapter struct {
	runtime modulewireguard.RuntimeHandle
}

func (a wireGuardRuntimeHandleAdapter) DialContext(ctx context.Context, network string, address string) (net.Conn, error) {
	return a.runtime.DialContext(ctx, network, address)
}

func (a wireGuardRuntimeHandleAdapter) ListenTCP(ctx context.Context, address string) (net.Listener, error) {
	return a.runtime.ListenTCP(ctx, address)
}

func (a wireGuardRuntimeHandleAdapter) ListenTransparentTCP(ctx context.Context) (net.Listener, error) {
	return a.runtime.ListenTransparentTCP(ctx)
}

func (a wireGuardRuntimeHandleAdapter) ListenUDP(ctx context.Context, address string) (net.PacketConn, error) {
	return a.runtime.ListenUDP(ctx, address)
}

func (a wireGuardRuntimeHandleAdapter) ListenTransparentUDP(ctx context.Context, address string) (module.TransparentUDPConn, error) {
	conn, err := a.runtime.ListenTransparentUDP(ctx, address)
	if err != nil {
		return nil, err
	}
	return wireGuardTransparentUDPConnAdapter{conn: conn}, nil
}

type wireGuardTransparentUDPConnAdapter struct {
	conn modulewireguard.TransparentUDPConn
}

func (a wireGuardTransparentUDPConnAdapter) Close() error {
	return a.conn.Close()
}

func (a wireGuardTransparentUDPConnAdapter) LocalAddr() net.Addr {
	return a.conn.LocalAddr()
}

func (a wireGuardTransparentUDPConnAdapter) ReadPacket() (module.TransparentUDPPacket, error) {
	packet, err := a.conn.ReadPacket()
	if err != nil {
		return module.TransparentUDPPacket{}, err
	}
	return module.TransparentUDPPacket{
		Peer:        packet.Peer,
		OriginalDst: packet.OriginalDst,
		Payload:     packet.Payload,
	}, nil
}

func (a wireGuardTransparentUDPConnAdapter) WritePacket(payload []byte, peer *net.UDPAddr, source string) error {
	return a.conn.WritePacket(payload, peer, source)
}

func wireGuardProfileForRelayHop(profiles []model.WireGuardProfile, localAgentID string, hop relay.Hop) (model.WireGuardProfile, bool) {
	host, _, err := net.SplitHostPort(strings.TrimSpace(hop.Address))
	if err != nil {
		return model.WireGuardProfile{}, false
	}
	addr, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return model.WireGuardProfile{}, false
	}
	localAgentID = strings.TrimSpace(localAgentID)

	var found model.WireGuardProfile
	for _, profile := range profiles {
		if !profile.Enabled {
			continue
		}
		if localAgentID != "" && strings.TrimSpace(profile.AgentID) != localAgentID {
			continue
		}
		if !modulewireguard.WireGuardProfileRoutesRelayHop(profile, addr) {
			continue
		}
		if found.ID != 0 {
			return model.WireGuardProfile{}, false
		}
		found = profile
	}
	return found, found.ID != 0
}
