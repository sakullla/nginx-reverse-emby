package core

import "github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"

func MergeSnapshotPayload(next, previous model.Snapshot) model.Snapshot {
	if next.Datasets == nil {
		next.Datasets = model.CloneDatasetSnapshots(previous.Datasets)
	}
	if next.PluginGenerations == nil {
		next.PluginGenerations = previous.PluginGenerations
	}
	if next.PluginDependencies == nil {
		next.PluginDependencies = previous.PluginDependencies
	}
	merged := next
	if next.VersionPackage == nil {
		merged.VersionPackage = previous.VersionPackage
	}
	if !next.HasAgentConfig() {
		merged.AgentConfig = previous.AgentConfig
	}
	if next.DDNSConfig == nil {
		merged.DDNSConfig = previous.DDNSConfig
	}
	if next.Rules == nil {
		merged.Rules = previous.Rules
	}
	if next.L4Rules == nil {
		merged.L4Rules = previous.L4Rules
	}
	if next.RelayListeners == nil {
		merged.RelayListeners = previous.RelayListeners
	}
	if next.EgressProfiles == nil {
		merged.EgressProfiles = previous.EgressProfiles
	}
	if next.Certificates == nil {
		merged.Certificates = previous.Certificates
	}
	if next.CertificatePolicies == nil {
		merged.CertificatePolicies = previous.CertificatePolicies
	}
	if next.PluginPolicies == nil {
		merged.PluginPolicies = previous.PluginPolicies
	}
	return merged
}
