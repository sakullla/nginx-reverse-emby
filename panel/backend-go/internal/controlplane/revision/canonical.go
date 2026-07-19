package revision

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func RequestFingerprint(request any) (string, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return "", NewError(ErrorCodeInvalidRequest, "request cannot be canonicalized", err)
	}
	return payloadDigest(payload), nil
}

func SemanticSnapshotDigest(snapshot storage.Snapshot) (string, error) {
	canonical, err := canonicalSnapshot(snapshot, true)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(canonical)
	if err != nil {
		return "", NewError(ErrorCodeUnprocessable, "snapshot cannot be canonicalized", err)
	}
	return payloadDigest(payload), nil
}

func CanonicalSnapshotPayload(snapshot storage.Snapshot) ([]byte, string, error) {
	canonical, err := canonicalSnapshot(snapshot, false)
	if err != nil {
		return nil, "", err
	}
	payload, err := json.Marshal(canonical)
	if err != nil {
		return nil, "", NewError(ErrorCodeUnprocessable, "snapshot cannot be canonicalized", err)
	}
	return payload, payloadDigest(payload), nil
}

func canonicalSnapshot(snapshot storage.Snapshot, stripRevision bool) (storage.Snapshot, error) {
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return storage.Snapshot{}, NewError(ErrorCodeUnprocessable, "snapshot cannot be copied", err)
	}
	var result storage.Snapshot
	if err := json.Unmarshal(payload, &result); err != nil {
		return storage.Snapshot{}, NewError(ErrorCodeUnprocessable, "snapshot cannot be copied", err)
	}
	if stripRevision {
		result.Revision = 0
	}

	result.Rules = nonNil(result.Rules)
	for i := range result.Rules {
		if stripRevision {
			result.Rules[i].Revision = 0
		}
		result.Rules[i].Backends = nonNil(result.Rules[i].Backends)
		result.Rules[i].CustomHeaders = nonNil(result.Rules[i].CustomHeaders)
		sort.Slice(result.Rules[i].CustomHeaders, func(a, b int) bool {
			left, right := result.Rules[i].CustomHeaders[a], result.Rules[i].CustomHeaders[b]
			if left.Name != right.Name {
				return left.Name < right.Name
			}
			return left.Value < right.Value
		})
		result.Rules[i].RelayLayers = canonicalRelayLayers(result.Rules[i].RelayLayers)
	}
	sort.Slice(result.Rules, func(i, j int) bool {
		left, right := result.Rules[i], result.Rules[j]
		if left.AgentID != right.AgentID {
			return left.AgentID < right.AgentID
		}
		if left.ID != right.ID {
			return left.ID < right.ID
		}
		return left.FrontendURL < right.FrontendURL
	})

	result.L4Rules = nonNil(result.L4Rules)
	for i := range result.L4Rules {
		if stripRevision {
			result.L4Rules[i].Revision = 0
		}
		result.L4Rules[i].Backends = nonNil(result.L4Rules[i].Backends)
		result.L4Rules[i].RelayLayers = canonicalRelayLayers(result.L4Rules[i].RelayLayers)
	}
	sort.Slice(result.L4Rules, func(i, j int) bool {
		left, right := result.L4Rules[i], result.L4Rules[j]
		if left.AgentID != right.AgentID {
			return left.AgentID < right.AgentID
		}
		return left.ID < right.ID
	})

	result.RelayListeners = nonNil(result.RelayListeners)
	for i := range result.RelayListeners {
		if stripRevision {
			result.RelayListeners[i].Revision = 0
		}
		sort.Strings(result.RelayListeners[i].BindHosts)
		result.RelayListeners[i].BindHosts = nonNil(result.RelayListeners[i].BindHosts)
		sort.Strings(result.RelayListeners[i].Tags)
		result.RelayListeners[i].Tags = nonNil(result.RelayListeners[i].Tags)
		sort.Ints(result.RelayListeners[i].TrustedCACertificateIDs)
		result.RelayListeners[i].TrustedCACertificateIDs = nonNil(result.RelayListeners[i].TrustedCACertificateIDs)
		result.RelayListeners[i].PinSet = nonNil(result.RelayListeners[i].PinSet)
		sort.Slice(result.RelayListeners[i].PinSet, func(a, b int) bool {
			left, right := result.RelayListeners[i].PinSet[a], result.RelayListeners[i].PinSet[b]
			if left.Type != right.Type {
				return left.Type < right.Type
			}
			return left.Value < right.Value
		})
	}
	sort.Slice(result.RelayListeners, func(i, j int) bool {
		left, right := result.RelayListeners[i], result.RelayListeners[j]
		if left.AgentID != right.AgentID {
			return left.AgentID < right.AgentID
		}
		return left.ID < right.ID
	})

	result.WireGuardProfiles = nonNil(result.WireGuardProfiles)
	for i := range result.WireGuardProfiles {
		if stripRevision {
			result.WireGuardProfiles[i].Revision = 0
		}
		canonicalizeWireGuardProfile(&result.WireGuardProfiles[i])
	}
	sort.Slice(result.WireGuardProfiles, func(i, j int) bool {
		left, right := result.WireGuardProfiles[i], result.WireGuardProfiles[j]
		if left.AgentID != right.AgentID {
			return left.AgentID < right.AgentID
		}
		return left.ID < right.ID
	})

	result.EgressProfiles = nonNil(result.EgressProfiles)
	for i := range result.EgressProfiles {
		if stripRevision {
			result.EgressProfiles[i].Revision = 0
		}
		if result.EgressProfiles[i].WireGuardConfig != nil {
			config := result.EgressProfiles[i].WireGuardConfig
			sort.Strings(config.Addresses)
			config.Addresses = nonNil(config.Addresses)
			sort.Strings(config.DNS)
			config.DNS = nonNil(config.DNS)
			canonicalizeWireGuardPeers(config.Peers)
			config.Peers = nonNil(config.Peers)
		}
	}
	sort.Slice(result.EgressProfiles, func(i, j int) bool {
		return result.EgressProfiles[i].ID < result.EgressProfiles[j].ID
	})

	result.Certificates = nonNil(result.Certificates)
	for i := range result.Certificates {
		if stripRevision {
			result.Certificates[i].Revision = 0
		}
	}
	sort.Slice(result.Certificates, func(i, j int) bool {
		left, right := result.Certificates[i], result.Certificates[j]
		if left.ID != right.ID {
			return left.ID < right.ID
		}
		return left.Domain < right.Domain
	})

	result.CertificatePolicies = nonNil(result.CertificatePolicies)
	for i := range result.CertificatePolicies {
		if stripRevision {
			result.CertificatePolicies[i].Revision = 0
		}
		sort.Strings(result.CertificatePolicies[i].Tags)
		result.CertificatePolicies[i].Tags = nonNil(result.CertificatePolicies[i].Tags)
	}
	sort.Slice(result.CertificatePolicies, func(i, j int) bool {
		left, right := result.CertificatePolicies[i], result.CertificatePolicies[j]
		if left.ID != right.ID {
			return left.ID < right.ID
		}
		return left.Domain < right.Domain
	})

	return result, nil
}

func canonicalRelayLayers(layers [][]int) [][]int {
	layers = nonNil(layers)
	for i := range layers {
		sort.Ints(layers[i])
		layers[i] = nonNil(layers[i])
	}
	return layers
}

func canonicalizeWireGuardProfile(profile *storage.WireGuardProfile) {
	sort.Strings(profile.BindAddresses)
	profile.BindAddresses = nonNil(profile.BindAddresses)
	sort.Strings(profile.Addresses)
	profile.Addresses = nonNil(profile.Addresses)
	sort.Strings(profile.DNS)
	profile.DNS = nonNil(profile.DNS)
	sort.Strings(profile.Tags)
	profile.Tags = nonNil(profile.Tags)
	canonicalizeWireGuardPeers(profile.Peers)
	profile.Peers = nonNil(profile.Peers)
}

func canonicalizeWireGuardPeers(peers []storage.WireGuardPeer) {
	for i := range peers {
		sort.Strings(peers[i].AllowedIPs)
		peers[i].AllowedIPs = nonNil(peers[i].AllowedIPs)
	}
	sort.Slice(peers, func(i, j int) bool {
		if peers[i].PublicKey != peers[j].PublicKey {
			return peers[i].PublicKey < peers[j].PublicKey
		}
		return peers[i].Name < peers[j].Name
	})
}

func payloadDigest(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func nonNil[T any](values []T) []T {
	if values == nil {
		return []T{}
	}
	return values
}
