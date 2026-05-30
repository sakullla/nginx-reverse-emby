package app

import (
	modulewireguard "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/wireguard"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
	basewireguard "github.com/sakullla/nginx-reverse-emby/go-agent/internal/wireguard"
)

func newSharedWireGuardRuntime() *modulewireguard.Runtime {
	return modulewireguard.NewRuntime(nil)
}

func newSharedWireGuardRuntimeWithFactory(factory basewireguard.Factory) *modulewireguard.Runtime {
	return modulewireguard.NewRuntime(factory)
}

func cloneWireGuardProfiles(profiles []model.WireGuardProfile) []model.WireGuardProfile {
	return modulewireguard.CloneWireGuardProfiles(profiles)
}

type wireGuardTransactionProvider struct {
	transaction *basewireguard.Transaction
	agentID     string
	profiles    []model.WireGuardProfile
}

func (p wireGuardTransactionProvider) WireGuardRuntime(profileID int) (relay.WireGuardRuntime, bool) {
	return modulewireguard.NewTransactionProvider(p.transaction, p.agentID, p.profiles).WireGuardRuntime(profileID)
}

func (p wireGuardTransactionProvider) WireGuardRuntimeForAgent(agentID string, profileID int) (relay.WireGuardRuntime, bool) {
	return modulewireguard.NewTransactionProvider(p.transaction, p.agentID, p.profiles).(relay.AgentWireGuardRuntimeProvider).WireGuardRuntimeForAgent(agentID, profileID)
}

func (p wireGuardTransactionProvider) WireGuardRuntimeForHop(hop relay.Hop) (relay.WireGuardRuntime, bool) {
	return modulewireguard.NewTransactionProvider(p.transaction, p.agentID, p.profiles).(relay.HopWireGuardRuntimeProvider).WireGuardRuntimeForHop(hop)
}
