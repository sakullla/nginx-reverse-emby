//go:build !integration

package http

import "testing"

func TestHTTPRetirementUsesActiveLeaseIdentityAcrossInheritedAlias(t *testing.T) {
	active := &httpIngressBinding{key: "tcp:inherited-address"}
	stale := &httpIngressBinding{key: "tcp:retired-address"}
	manager := &httpIngressManager{bindings: map[string]*httpIngressBinding{
		active.key: active,
		stale.key:  stale,
	}}
	runtime := &Runtime{
		bindings: []string{"tcp:requested-address"},
		ingressLeases: []*httpIngressLease{{
			binding: active,
		}},
	}
	transaction := &httpGenerationTransaction{
		module: &Module{ingress: manager}, runtime: runtime,
	}

	transaction.retireInactiveIngressBindings()

	if manager.bindings[active.key] != active {
		t.Fatal("retirement removed the active inherited binding after its key alias changed")
	}
	if manager.bindings[stale.key] != nil {
		t.Fatal("retirement kept an inactive binding")
	}
}
