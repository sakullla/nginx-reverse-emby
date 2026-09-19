package pluginsdk

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

const (
	ExtensionHTTPBackendProvider    = "http.backend-provider"
	RPCFeatureHTTPBackendProviderV1 = "rpc.http-backend-provider.v1"
	PermissionHTTPOutbound          = "http.outbound"

	HTTPBackendKindURL            = "url"
	HTTPBackendKindPluginProvider = "plugin_provider"
	ProviderIDMaxBytes            = 64

	HTTPBackendCatalogKindPluginProvider = HTTPBackendKindPluginProvider
	HTTPBackendCatalogKindPublishedPort  = "published_port"
	HTTPBackendOfferMaxEntries           = 256
)

var httpBackendProviderIDPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)

// HTTPBackendProviderDescriptor is the stable provider identity declared by
// an Agent-hosted RPC plugin. DisplayName is presentation-only; rules persist
// only the provider ID and plugin instance ID.
type HTTPBackendProviderDescriptor struct {
	ID          string `yaml:"id" json:"id"`
	DisplayName string `yaml:"display_name" json:"display_name"`
}

func (descriptor HTTPBackendProviderDescriptor) Validate() error {
	if err := validateHTTPBackendProviderID(descriptor.ID); err != nil {
		return fmt.Errorf("HTTP backend provider id: %w", err)
	}
	if descriptor.DisplayName != strings.TrimSpace(descriptor.DisplayName) || descriptor.DisplayName == "" || len(descriptor.DisplayName) > 128 || strings.ContainsAny(descriptor.DisplayName, "\r\n\x00") {
		return errors.New("HTTP backend provider display_name is invalid")
	}
	return nil
}

// HTTPPluginProviderRef is a durable rule reference. Runtime endpoint and
// credential material are deliberately absent from the control-plane wire.
type HTTPPluginProviderRef struct {
	InstanceID string `json:"instance_id"`
	ProviderID string `json:"provider_id"`
}

// HTTPBackend extends the established URL backend wire shape with a tagged
// plugin provider variant. URL backends intentionally keep the historical
// {"url": ...} representation; plugin providers must always carry kind.
type HTTPBackend struct {
	Kind           string                 `json:"kind,omitempty"`
	URL            string                 `json:"url,omitempty"`
	PluginProvider *HTTPPluginProviderRef `json:"plugin_provider,omitempty"`
}

func (backend HTTPBackend) Validate() error {
	if backend.Kind != strings.TrimSpace(backend.Kind) {
		return errors.New("HTTP backend kind is not canonical")
	}
	switch backend.Kind {
	case "", HTTPBackendKindURL:
		if backend.PluginProvider != nil || backend.URL != strings.TrimSpace(backend.URL) || !validHTTPBackendURL(backend.URL) {
			return errors.New("URL backend requires exactly one canonical http/https URL")
		}
	case HTTPBackendKindPluginProvider:
		if backend.URL != "" || backend.PluginProvider == nil {
			return errors.New("kind:plugin_provider requires exactly one plugin_provider reference")
		}
		if err := ValidatePolicyIdentity(backend.PluginProvider.InstanceID); err != nil {
			return fmt.Errorf("plugin provider instance_id: %w", err)
		}
		if err := validateHTTPBackendProviderID(backend.PluginProvider.ProviderID); err != nil {
			return fmt.Errorf("plugin provider provider_id: %w", err)
		}
	default:
		return fmt.Errorf("unsupported HTTP backend kind %q", backend.Kind)
	}
	return nil
}

func validateHTTPBackendProviderID(id string) error {
	if id != strings.TrimSpace(id) || !httpBackendProviderIDPattern.MatchString(id) {
		return errors.New("is not canonical")
	}
	if len(id) > ProviderIDMaxBytes {
		return fmt.Errorf("exceeds %d UTF-8 bytes", ProviderIDMaxBytes)
	}
	return nil
}

func ValidateHTTPBackends(backends []HTTPBackend) error {
	if len(backends) == 0 {
		return errors.New("HTTP backends must not be empty")
	}
	seenProviders := make(map[string]struct{}, len(backends))
	for index, backend := range backends {
		if err := backend.Validate(); err != nil {
			return fmt.Errorf("HTTP backend %d: %w", index, err)
		}
		if backend.Kind != HTTPBackendKindPluginProvider {
			continue
		}
		key := backend.PluginProvider.InstanceID + "\x00" + backend.PluginProvider.ProviderID
		if _, duplicate := seenProviders[key]; duplicate {
			return fmt.Errorf("HTTP backend %d is duplicated", index)
		}
		seenProviders[key] = struct{}{}
	}
	return nil
}

func ValidateHTTPBackendProviderDescriptors(descriptors []HTTPBackendProviderDescriptor) error {
	if len(descriptors) == 0 || len(descriptors) > 16 {
		return errors.New("HTTP backend provider descriptors must contain 1..16 entries")
	}
	seen := make(map[string]struct{}, len(descriptors))
	for index, descriptor := range descriptors {
		if err := descriptor.Validate(); err != nil {
			return fmt.Errorf("HTTP backend provider descriptor %d: %w", index, err)
		}
		if _, duplicate := seen[descriptor.ID]; duplicate {
			return fmt.Errorf("HTTP backend provider descriptor %q is duplicated", descriptor.ID)
		}
		seen[descriptor.ID] = struct{}{}
	}
	return nil
}

// ValidateHTTPBackendProviderManifest enforces the indivisible provider
// contract. A provider cannot be admitted with only a descriptor, extension,
// permission, or RPC feature projection present.
func ValidateHTTPBackendProviderManifest(manifest Manifest) error {
	hasExtension := slices.Contains(manifest.ExtensionPoints, ExtensionHTTPBackendProvider)
	hasDescriptors := len(manifest.HTTPBackendProviders) > 0
	hasUnscopedOutbound := false
	hasScopedOutbound := false
	for _, permission := range manifest.Permissions {
		if permission.Name == PermissionHTTPOutbound {
			if permission.Resource == "" {
				hasUnscopedOutbound = true
			} else {
				hasScopedOutbound = true
			}
		}
	}
	if !hasExtension && !hasDescriptors {
		return nil
	}
	if !hasExtension || !hasDescriptors || manifest.Runtime.Kind != RuntimeRPCService || manifest.Runtime.ABI != RPCABIV1 || manifest.Runtime.HostScope != HostScopeAgent {
		return errors.New("HTTP backend providers require an Agent-hosted nre:rpc/v1 service, extension, and descriptors")
	}
	if err := ValidateHTTPBackendProviderDescriptors(manifest.HTTPBackendProviders); err != nil {
		return err
	}
	if !hasUnscopedOutbound || hasScopedOutbound {
		return errors.New("HTTP backend providers require the internal http.outbound permission")
	}
	if !slices.Contains(RequiredRPCFeaturesForExtensions([]string{PermissionHTTPOutbound}, manifest.ExtensionPoints), RPCFeatureHTTPBackendProviderV1) {
		return errors.New("HTTP backend provider RPC feature projection is unavailable")
	}
	return nil
}

func validHTTPBackendURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed == nil || parsed.Host == "" {
		return false
	}
	return strings.EqualFold(parsed.Scheme, "http") || strings.EqualFold(parsed.Scheme, "https")
}

// HTTPBackendOffer is a published-port catalog candidate reported by a
// control-plane plugin. Identity is the resource (app or equivalent), target
// Agent, and port. Runtime endpoints and credentials are absent.
type HTTPBackendOffer struct {
	ResourceID  string `json:"resource_id"`
	AgentID     string `json:"agent_id"`
	Port        int    `json:"port"`
	DisplayName string `json:"display_name"`
	Available   bool   `json:"available"`
}

func (offer HTTPBackendOffer) Validate() error {
	if err := ValidatePolicyIdentity(offer.ResourceID); err != nil {
		return fmt.Errorf("resource_id: %w", err)
	}
	if err := ValidatePolicyIdentity(offer.AgentID); err != nil {
		return fmt.Errorf("agent_id: %w", err)
	}
	if offer.Port <= 0 || offer.Port > 65535 {
		return errors.New("port is invalid")
	}
	if offer.DisplayName != strings.TrimSpace(offer.DisplayName) || offer.DisplayName == "" || len(offer.DisplayName) > 128 || strings.ContainsAny(offer.DisplayName, "\r\n\x00") {
		return errors.New("display_name is invalid")
	}
	return nil
}

// HTTPBackendOfferReplaceRequest replaces one plugin instance's published-port
// catalog. An empty list clears previously reported offers.
type HTTPBackendOfferReplaceRequest struct {
	Offers []HTTPBackendOffer `json:"offers"`
}

func (request HTTPBackendOfferReplaceRequest) Validate() error {
	if len(request.Offers) > HTTPBackendOfferMaxEntries {
		return fmt.Errorf("http.backend-offer exceeds %d entries", HTTPBackendOfferMaxEntries)
	}
	seen := make(map[string]struct{}, len(request.Offers))
	for index, offer := range request.Offers {
		if err := offer.Validate(); err != nil {
			return fmt.Errorf("http.backend-offer %d: %w", index, err)
		}
		key := offer.AgentID + "\x00" + offer.ResourceID + "\x00" + fmt.Sprintf("%d", offer.Port)
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("http.backend-offer %d is duplicated", index)
		}
		seen[key] = struct{}{}
	}
	return nil
}
