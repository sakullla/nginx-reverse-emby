package core

import "github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"

func MergeSnapshotPayload(next, previous model.Snapshot) model.Snapshot {
	merged := next
	if next.VersionPackage == nil {
		merged.VersionPackage = previous.VersionPackage
	}
	if !next.HasAgentConfig() {
		merged.AgentConfig = previous.AgentConfig
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
	if next.WireGuardProfiles == nil {
		merged.WireGuardProfiles = previous.WireGuardProfiles
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
	return merged
}
