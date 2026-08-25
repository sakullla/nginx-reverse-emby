//go:build !integration

package relay

import "testing"

func TestRelayRetirementUsesActiveLeaseIdentityAcrossInheritedAlias(t *testing.T) {
	active := &relayIngressBinding{key: "tcp:inherited-address"}
	stale := &relayIngressBinding{key: "tcp:retired-address"}
	manager := &relayIngressManager{bindings: map[string]*relayIngressBinding{
		active.key: active,
		stale.key:  stale,
	}}
	runtime := &Server{
		bindingKeys: []string{"tcp:requested-address"},
		ingressLeases: []*relayIngressLease{{
			binding: active,
		}},
	}
	transaction := &relayGenerationTransaction{
		module: &Module{ingress: manager}, runtime: runtime, ownsRuntime: true,
	}

	transaction.retireInactiveIngressBindings()

	if manager.bindings[active.key] != active {
		t.Fatal("retirement removed the active inherited binding after its key alias changed")
	}
	if manager.bindings[stale.key] != nil {
		t.Fatal("retirement kept an inactive binding")
	}
}
