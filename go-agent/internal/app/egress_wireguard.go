package app

import (
	"fmt"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	moduleegress "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/egress"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/wireguard"
)

type egressWireGuardRuntime = moduleegress.WireGuardRuntime

func newEgressWireGuardRuntime(factory wireguard.Factory) *egressWireGuardRuntime {
	return moduleegress.NewWireGuardRuntime(factory)
}

func egressWireGuardProfiles(profiles []model.EgressProfile) []model.WireGuardProfile {
	return moduleegress.WireGuardProfiles(profiles)
}

func egressWireGuardProfile(profile model.EgressProfile) model.WireGuardProfile {
	return moduleegress.WireGuardProfile(profile)
}

func cloneEgressProfiles(profiles []model.EgressProfile) []model.EgressProfile {
	return moduleegress.CloneProfiles(profiles)
}

func validateEgressWireGuardReferences(rules []model.L4Rule, egressProfiles []model.EgressProfile, provider module.OverlayRuntime) error {
	resolver := egressProfileByID(egressProfiles)
	for _, rule := range rules {
		if len(rule.RelayLayers) > 0 || rule.EgressProfileID == nil || *rule.EgressProfileID <= 0 {
			continue
		}
		profile, ok := resolver[*rule.EgressProfileID]
		if !ok || !profile.Enabled || !strings.EqualFold(strings.TrimSpace(profile.Type), "wireguard") {
			continue
		}
		if provider == nil {
			return fmt.Errorf("wireguard runtime provider is required for egress profile %d", profile.ID)
		}
	}
	return nil
}

func egressProfileByID(profiles []model.EgressProfile) map[int]model.EgressProfile {
	byID := make(map[int]model.EgressProfile, len(profiles))
	for _, profile := range profiles {
		byID[profile.ID] = profile
	}
	return byID
}
