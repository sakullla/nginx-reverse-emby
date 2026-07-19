package moduleutil

import (
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestCloneHTTPRulesDeepCopiesMutableFields(t *testing.T) {
	wgProfileID := 7
	egressProfileID := 8
	rules := []model.HTTPRule{{
		AgentID:            " agent-a ",
		Backends:           []model.HTTPBackend{{URL: "https://backend.example"}},
		CustomHeaders:      []model.HTTPHeader{{Name: "X-Test", Value: "a"}},
		RelayChain:         []int{1},
		RelayLayers:        [][]int{{2, 3}},
		Tags:               []string{"blue"},
		WireGuardProfileID: &wgProfileID,
		EgressProfileID:    &egressProfileID,
	}}

	cloned := CloneHTTPRules(rules)
	rules[0].Backends[0].URL = "https://changed.example"
	rules[0].CustomHeaders[0].Value = "changed"
	rules[0].RelayChain[0] = 9
	rules[0].RelayLayers[0][0] = 9
	rules[0].Tags[0] = "changed"
	*rules[0].WireGuardProfileID = 9
	*rules[0].EgressProfileID = 10

	if cloned[0].AgentID != "agent-a" {
		t.Fatalf("AgentID = %q", cloned[0].AgentID)
	}
	if cloned[0].Backends[0].URL != "https://backend.example" {
		t.Fatalf("Backends = %+v", cloned[0].Backends)
	}
	if cloned[0].CustomHeaders[0].Value != "a" {
		t.Fatalf("CustomHeaders = %+v", cloned[0].CustomHeaders)
	}
	if cloned[0].RelayChain[0] != 1 || cloned[0].RelayLayers[0][0] != 2 {
		t.Fatalf("relay fields = %+v / %+v", cloned[0].RelayChain, cloned[0].RelayLayers)
	}
	if cloned[0].Tags[0] != "blue" {
		t.Fatalf("Tags = %+v", cloned[0].Tags)
	}
	if *cloned[0].WireGuardProfileID != 7 || *cloned[0].EgressProfileID != 8 {
		t.Fatalf("profile ids = %v / %v", *cloned[0].WireGuardProfileID, *cloned[0].EgressProfileID)
	}
}

func TestCloneL4RulesDeepCopiesMutableFields(t *testing.T) {
	wgProfileID := 11
	egressProfileID := 12
	rules := []model.L4Rule{{
		Backends:           []model.L4Backend{{Host: "backend.example", Port: 443}},
		RelayChain:         []int{1},
		RelayLayers:        [][]int{{2, 3}},
		Tags:               []string{"green"},
		WireGuardProfileID: &wgProfileID,
		EgressProfileID:    &egressProfileID,
	}}

	cloned := CloneL4Rules(rules)
	rules[0].Backends[0].Host = "changed.example"
	rules[0].RelayChain[0] = 9
	rules[0].RelayLayers[0][0] = 9
	rules[0].Tags[0] = "changed"
	*rules[0].WireGuardProfileID = 13
	*rules[0].EgressProfileID = 14

	if cloned[0].Backends[0].Host != "backend.example" {
		t.Fatalf("Backends = %+v", cloned[0].Backends)
	}
	if cloned[0].RelayChain[0] != 1 || cloned[0].RelayLayers[0][0] != 2 {
		t.Fatalf("relay fields = %+v / %+v", cloned[0].RelayChain, cloned[0].RelayLayers)
	}
	if cloned[0].Tags[0] != "green" {
		t.Fatalf("Tags = %+v", cloned[0].Tags)
	}
	if *cloned[0].WireGuardProfileID != 11 || *cloned[0].EgressProfileID != 12 {
		t.Fatalf("profile ids = %v / %v", *cloned[0].WireGuardProfileID, *cloned[0].EgressProfileID)
	}
}
