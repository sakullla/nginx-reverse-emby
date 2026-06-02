package http

import (
	"slices"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	moduleegress "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/egress"
)

func cloneHTTPRules(rules []model.HTTPRule) []model.HTTPRule {
	if rules == nil {
		return nil
	}
	cloned := slices.Clone(rules)
	for i, rule := range rules {
		cloned[i].AgentID = strings.TrimSpace(rule.AgentID)
		cloned[i].Backends = slices.Clone(rule.Backends)
		cloned[i].CustomHeaders = slices.Clone(rule.CustomHeaders)
		cloned[i].RelayChain = slices.Clone(rule.RelayChain)
		cloned[i].RelayLayers = cloneIntLayers(rule.RelayLayers)
		cloned[i].Tags = slices.Clone(rule.Tags)
		cloned[i].WireGuardProfileID = clonePtr(rule.WireGuardProfileID)
		cloned[i].EgressProfileID = clonePtr(rule.EgressProfileID)
	}
	return cloned
}

func cloneRelayListeners(listeners []model.RelayListener) []model.RelayListener {
	if listeners == nil {
		return nil
	}
	cloned := slices.Clone(listeners)
	for i, listener := range listeners {
		cloned[i].BindHosts = slices.Clone(listener.BindHosts)
		cloned[i].CertificateID = clonePtr(listener.CertificateID)
		cloned[i].WireGuardProfileID = clonePtr(listener.WireGuardProfileID)
		cloned[i].PinSet = slices.Clone(listener.PinSet)
		cloned[i].TrustedCACertificateIDs = slices.Clone(listener.TrustedCACertificateIDs)
		cloned[i].Tags = slices.Clone(listener.Tags)
	}
	return cloned
}

func cloneIntLayers(layers [][]int) [][]int {
	if layers == nil {
		return nil
	}
	cloned := make([][]int, len(layers))
	for i, layer := range layers {
		cloned[i] = slices.Clone(layer)
	}
	return cloned
}

func clonePtr[T any](value *T) *T {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneEgressProfiles(profiles []model.EgressProfile) []model.EgressProfile {
	return moduleegress.CloneProfiles(profiles)
}

func cloneProviders(providers Providers) Providers {
	providers.EgressProfiles = cloneEgressProfiles(providers.EgressProfiles)
	return providers
}

func snapshotProviders(providers Providers, egressProfiles []model.EgressProfile) Providers {
	providers = cloneProviders(providers)
	profiles := cloneEgressProfiles(egressProfiles)
	providers.EgressProfiles = profiles
	providers.EgressResolver = nil
	providers.FinalHopDialer = moduleegress.NewFinalHopDialer(profiles, providers.EgressOverlay)
	return providers
}
