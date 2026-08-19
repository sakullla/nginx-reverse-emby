package pluginsdk

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// RPCPluginDeclaration is the small, declarative surface a guest supplies for
// the canonical v1 handshake. Package/artifact digests and the generation are
// Host-attested request values and therefore do not belong in plugin startup
// configuration.
type RPCPluginDeclaration struct {
	PluginID             string
	PluginVersion        string
	RequiredCapabilities []string
	SupportedFeatures    []string
}

// NegotiateRPCHandshake performs the canonical fail-closed v1 negotiation.
// It verifies Host identity, acknowledges only requested features the plugin
// declared as supported, and returns only capabilities that the Host granted.
// Plugin authors should not manually mirror RequiredFeatures or GrantedScopes.
func NegotiateRPCHandshake(declaration RPCPluginDeclaration, request RPCHandshakeRequest) (RPCHandshakeResponse, error) {
	pluginID := strings.TrimSpace(declaration.PluginID)
	pluginVersion := strings.TrimSpace(declaration.PluginVersion)
	if pluginID == "" || pluginVersion == "" || pluginID != declaration.PluginID || pluginVersion != declaration.PluginVersion {
		return RPCHandshakeResponse{}, errors.New("RPC plugin declaration identity is required and must be canonical")
	}
	if request.ABI != RPCABIV1 {
		return RPCHandshakeResponse{}, fmt.Errorf("unsupported RPC ABI %q", request.ABI)
	}
	if request.PluginID != pluginID || request.PluginVersion != pluginVersion {
		return RPCHandshakeResponse{}, errors.New("RPC plugin identity mismatch")
	}
	if strings.TrimSpace(request.PackageDigest) == "" || strings.TrimSpace(request.ArtifactDigest) == "" || strings.TrimSpace(request.Generation) == "" {
		return RPCHandshakeResponse{}, errors.New("RPC Host attestation is incomplete")
	}

	capabilities, err := canonicalRPCNegotiationNames("required capability", declaration.RequiredCapabilities)
	if err != nil {
		return RPCHandshakeResponse{}, err
	}
	granted, err := canonicalRPCNegotiationSet("granted scope", request.GrantedScopes)
	if err != nil {
		return RPCHandshakeResponse{}, err
	}
	for _, capability := range capabilities {
		if _, ok := granted[capability]; !ok {
			return RPCHandshakeResponse{}, fmt.Errorf("required capability %q was not granted", capability)
		}
	}

	supported, err := canonicalRPCNegotiationSet("supported feature", declaration.SupportedFeatures)
	if err != nil {
		return RPCHandshakeResponse{}, err
	}
	features := make([]string, 0, len(request.RequiredFeatures))
	for _, feature := range request.RequiredFeatures {
		if _, ok := supported[feature]; ok {
			features = append(features, feature)
		}
	}
	if err := ValidateRPCFeatures(request.RequiredFeatures, features); err != nil {
		return RPCHandshakeResponse{}, err
	}
	return RPCHandshakeResponse{ABI: RPCABIV1, Capabilities: capabilities, Features: features}, nil
}

func canonicalRPCNegotiationNames(kind string, values []string) ([]string, error) {
	result := append([]string(nil), values...)
	sort.Strings(result)
	for index, value := range result {
		if value == "" || value != strings.TrimSpace(value) {
			return nil, fmt.Errorf("%s must be non-empty and canonical", kind)
		}
		if index > 0 && result[index-1] == value {
			return nil, fmt.Errorf("%s %q is duplicated", kind, value)
		}
	}
	return result, nil
}

func canonicalRPCNegotiationSet(kind string, values []string) (map[string]struct{}, error) {
	canonical, err := canonicalRPCNegotiationNames(kind, values)
	if err != nil {
		return nil, err
	}
	result := make(map[string]struct{}, len(canonical))
	for _, value := range canonical {
		result[value] = struct{}{}
	}
	return result, nil
}
