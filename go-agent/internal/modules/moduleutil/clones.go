package moduleutil

import (
	"slices"

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
