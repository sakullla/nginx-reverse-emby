package moduleutil

import (
	"slices"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func ClonePtr[T any](value *T) *T {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func CloneIntLayers(layers [][]int) [][]int {
	if layers == nil {
		return nil
	}
	cloned := make([][]int, len(layers))
	for i, layer := range layers {
		cloned[i] = slices.Clone(layer)
	}
	return cloned
}

func CloneRelayListeners(listeners []model.RelayListener) []model.RelayListener {
	if listeners == nil {
		return nil
	}
	cloned := slices.Clone(listeners)
	for i, listener := range listeners {
		cloned[i].BindHosts = slices.Clone(listener.BindHosts)
		cloned[i].CertificateID = ClonePtr(listener.CertificateID)
		cloned[i].WireGuardProfileID = ClonePtr(listener.WireGuardProfileID)
		cloned[i].PinSet = slices.Clone(listener.PinSet)
		cloned[i].TrustedCACertificateIDs = slices.Clone(listener.TrustedCACertificateIDs)
		cloned[i].Tags = slices.Clone(listener.Tags)
	}
	return cloned
}

func CloneHTTPRules(rules []model.HTTPRule) []model.HTTPRule {
	if rules == nil {
		return nil
	}
	cloned := slices.Clone(rules)
	for i, rule := range rules {
		cloned[i].AgentID = strings.TrimSpace(rule.AgentID)
		cloned[i].Backends = slices.Clone(rule.Backends)
		cloned[i].CustomHeaders = slices.Clone(rule.CustomHeaders)
		cloneRuleSharedFields(&cloned[i].RelayChain, &cloned[i].RelayLayers, &cloned[i].Tags, &cloned[i].WireGuardProfileID, &cloned[i].EgressProfileID, rule.RelayChain, rule.RelayLayers, rule.Tags, rule.WireGuardProfileID, rule.EgressProfileID)
	}
	return cloned
}

func CloneL4Rules(rules []model.L4Rule) []model.L4Rule {
	if rules == nil {
		return nil
	}
	cloned := slices.Clone(rules)
	for i, rule := range rules {
		cloned[i].Backends = slices.Clone(rule.Backends)
		cloneRuleSharedFields(&cloned[i].RelayChain, &cloned[i].RelayLayers, &cloned[i].Tags, &cloned[i].WireGuardProfileID, &cloned[i].EgressProfileID, rule.RelayChain, rule.RelayLayers, rule.Tags, rule.WireGuardProfileID, rule.EgressProfileID)
	}
	return cloned
}

func cloneRuleSharedFields(relayChain *[]int, relayLayers *[][]int, tags *[]string, wireGuardProfileID **int, egressProfileID **int, sourceRelayChain []int, sourceRelayLayers [][]int, sourceTags []string, sourceWireGuardProfileID *int, sourceEgressProfileID *int) {
	*relayChain = slices.Clone(sourceRelayChain)
	*relayLayers = CloneIntLayers(sourceRelayLayers)
	*tags = slices.Clone(sourceTags)
	*wireGuardProfileID = ClonePtr(sourceWireGuardProfileID)
	*egressProfileID = ClonePtr(sourceEgressProfileID)
}
