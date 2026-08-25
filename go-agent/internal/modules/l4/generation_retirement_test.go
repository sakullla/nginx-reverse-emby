//go:build !integration

package l4

import "testing"

func TestL4RetirementUsesActiveLeaseIdentityAcrossInheritedAlias(t *testing.T) {
	active := &l4IngressBinding{key: "tcp:inherited-address"}
	stale := &l4IngressBinding{key: "tcp:retired-address"}
	manager := &l4IngressManager{bindings: map[string]*l4IngressBinding{
		active.key: active,
		stale.key:  stale,
	}}
	server := &Server{
		bindingKeys: []string{"tcp:requested-address"},
		ingressLeases: []*l4IngressLease{{
			binding: active,
		}},
	}
	transaction := &l4GenerationTransaction{
		module: &Module{ingress: manager}, server: server,
	}

	transaction.retireInactiveIngressBindings()

	if manager.bindings[active.key] != active {
		t.Fatal("retirement removed the active inherited binding after its key alias changed")
	}
	if manager.bindings[stale.key] != nil {
		t.Fatal("retirement kept an inactive binding")
	}
}
