package app

import (
	"net"
	"net/netip"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	modulewireguard "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/wireguard"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
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
	if p.agentID != "" {
		return p.runtime.RuntimeForAgent(p.agentID, profileID)
	}
	return p.runtime.Runtime(profileID)
}

func (p wireGuardRuntimeProvider) WireGuardRuntimeForAgent(agentID string, profileID int) (relay.WireGuardRuntime, bool) {
	if p.runtime == nil {
		return nil, false
	}
	return p.runtime.RuntimeForAgent(agentID, profileID)
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
	return p.runtime.RuntimeForAgent(profile.AgentID, profile.ID)
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
	if p.agentID != "" {
		return p.transaction.RuntimeForAgent(p.agentID, profileID)
	}
	return p.transaction.Runtime(profileID)
}

func (p wireGuardTransactionProvider) WireGuardRuntimeForAgent(agentID string, profileID int) (relay.WireGuardRuntime, bool) {
	if p.transaction == nil {
		return nil, false
	}
	return p.transaction.RuntimeForAgent(agentID, profileID)
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
	return p.transaction.RuntimeForAgent(profile.AgentID, profile.ID)
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
