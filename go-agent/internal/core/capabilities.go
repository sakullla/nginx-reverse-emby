package core

import (
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

type CapabilitySource interface {
	Capabilities(module.SnapshotView) []module.Capability
}

func CapabilityNames(source CapabilitySource) []string {
	var capabilities []string
	seen := make(map[string]struct{})
	appendCapability := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		capabilities = append(capabilities, name)
	}

	if source != nil {
		for _, capability := range source.Capabilities(module.SnapshotView{}) {
			if capability.Enabled {
				appendCapability(capability.Name)
			}
		}
	}
	return capabilities
}
