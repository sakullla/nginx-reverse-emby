package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	PluginDependencyConsumerHTTPRule = "http_rule"
	PluginDependencyConsumerL4Rule   = "l4_rule"
)

var ErrPluginDependencyConsumerInUse = errors.New("plugin dependency consumer ownership is in use")

// PluginInstanceBinding is the durable, operator-configured authority for a
// core consumer to require one plugin instance on one target Agent.
type PluginInstanceBinding struct {
	Consumer      PluginDependencyConsumer `json:"consumer"`
	TargetAgentID string                   `json:"target_agent_id"`
}

// PluginInstanceBindingRequest is the caller-owned portion of a binding.
// Resource-group and ownership-version fences are always derived by storage.
type PluginInstanceBindingRequest struct {
	Consumer      PluginInstanceBindingConsumer `json:"consumer"`
	TargetAgentID string                        `json:"target_agent_id"`
}

type PluginInstanceBindingConsumer struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

func CanonicalPluginInstanceBindings(raw string) ([]PluginInstanceBinding, error) {
	if strings.TrimSpace(raw) == "" {
		return []PluginInstanceBinding{}, nil
	}
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	var bindings []PluginInstanceBinding
	if err := decoder.Decode(&bindings); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("plugin bindings contain a trailing JSON value")
	}
	if bindings == nil {
		bindings = []PluginInstanceBinding{}
	}
	for index := range bindings {
		bindings[index].Consumer.Kind = strings.ToLower(strings.TrimSpace(bindings[index].Consumer.Kind))
		bindings[index].Consumer.ID = strings.TrimSpace(bindings[index].Consumer.ID)
		bindings[index].Consumer.ResourceGroupID = strings.TrimSpace(bindings[index].Consumer.ResourceGroupID)
		bindings[index].Consumer.Version = strings.TrimSpace(bindings[index].Consumer.Version)
		bindings[index].TargetAgentID = strings.TrimSpace(bindings[index].TargetAgentID)
		if err := validatePluginInstanceBinding(bindings[index]); err != nil {
			return nil, fmt.Errorf("plugin binding %d: %w", index, err)
		}
	}
	sort.Slice(bindings, func(i, j int) bool {
		if bindings[i].TargetAgentID != bindings[j].TargetAgentID {
			return bindings[i].TargetAgentID < bindings[j].TargetAgentID
		}
		if bindings[i].Consumer.Kind != bindings[j].Consumer.Kind {
			return bindings[i].Consumer.Kind < bindings[j].Consumer.Kind
		}
		return bindings[i].Consumer.ID < bindings[j].Consumer.ID
	})
	for index := 1; index < len(bindings); index++ {
		if bindings[index] == bindings[index-1] {
			return nil, fmt.Errorf("duplicate plugin binding for %s %s on Agent %s", bindings[index].Consumer.Kind, bindings[index].Consumer.ID, bindings[index].TargetAgentID)
		}
	}
	return bindings, nil
}

func CanonicalPluginInstanceBindingRequests(requests []PluginInstanceBindingRequest) ([]PluginInstanceBindingRequest, error) {
	encoded, err := json.Marshal(requests)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var result []PluginInstanceBindingRequest
	if err := decoder.Decode(&result); err != nil {
		return nil, err
	}
	if result == nil {
		result = []PluginInstanceBindingRequest{}
	}
	for index := range result {
		result[index].Consumer.Kind = strings.ToLower(strings.TrimSpace(result[index].Consumer.Kind))
		result[index].Consumer.ID = strings.TrimSpace(result[index].Consumer.ID)
		result[index].TargetAgentID = strings.TrimSpace(result[index].TargetAgentID)
		binding := PluginInstanceBinding{Consumer: PluginDependencyConsumer{Kind: result[index].Consumer.Kind, ID: result[index].Consumer.ID}, TargetAgentID: result[index].TargetAgentID}
		if err := validatePluginInstanceBinding(binding); err != nil {
			return nil, fmt.Errorf("plugin binding %d: %w", index, err)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].TargetAgentID != result[j].TargetAgentID {
			return result[i].TargetAgentID < result[j].TargetAgentID
		}
		if result[i].Consumer.Kind != result[j].Consumer.Kind {
			return result[i].Consumer.Kind < result[j].Consumer.Kind
		}
		return result[i].Consumer.ID < result[j].Consumer.ID
	})
	for index := 1; index < len(result); index++ {
		if result[index] == result[index-1] {
			return nil, fmt.Errorf("duplicate plugin binding for %s %s on Agent %s", result[index].Consumer.Kind, result[index].Consumer.ID, result[index].TargetAgentID)
		}
	}
	return result, nil
}

func PluginInstanceBindingRequests(bindings []PluginInstanceBinding) []PluginInstanceBindingRequest {
	requests := make([]PluginInstanceBindingRequest, 0, len(bindings))
	for _, binding := range bindings {
		requests = append(requests, PluginInstanceBindingRequest{
			Consumer:      PluginInstanceBindingConsumer{Kind: binding.Consumer.Kind, ID: binding.Consumer.ID},
			TargetAgentID: binding.TargetAgentID,
		})
	}
	return requests
}

func EncodePluginInstanceBindings(bindings []PluginInstanceBinding) (string, error) {
	encoded, err := json.Marshal(bindings)
	if err != nil {
		return "", err
	}
	canonical, err := CanonicalPluginInstanceBindings(string(encoded))
	if err != nil {
		return "", err
	}
	encoded, err = json.Marshal(canonical)
	return string(encoded), err
}

func ValidatePluginInstanceBindingScope(bindings []PluginInstanceBinding, targetIDs, extensionPoints []string) error {
	targets := make(map[string]struct{}, len(targetIDs))
	for _, targetID := range targetIDs {
		targets[strings.TrimSpace(targetID)] = struct{}{}
	}
	extensions := make(map[string]struct{}, len(extensionPoints))
	for _, extension := range extensionPoints {
		extensions[strings.TrimSpace(extension)] = struct{}{}
	}
	for _, binding := range bindings {
		if _, ok := targets[binding.TargetAgentID]; !ok {
			return fmt.Errorf("plugin binding target Agent %q is outside the instance targets", binding.TargetAgentID)
		}
		if !pluginBindingExtensionSupported(binding.Consumer.Kind, extensions) {
			return fmt.Errorf("plugin binding consumer kind %q is incompatible with the provider extension points", binding.Consumer.Kind)
		}
	}
	return nil
}

func validatePluginInstanceBinding(binding PluginInstanceBinding) error {
	if binding.TargetAgentID == "" {
		return errors.New("target_agent_id is required")
	}
	switch binding.Consumer.Kind {
	case PluginDependencyConsumerHTTPRule, PluginDependencyConsumerL4Rule:
	default:
		return fmt.Errorf("unsupported consumer kind %q", binding.Consumer.Kind)
	}
	id, err := strconv.Atoi(binding.Consumer.ID)
	if err != nil || id <= 0 || strconv.Itoa(id) != binding.Consumer.ID {
		return errors.New("consumer id must be a canonical positive decimal integer")
	}
	if (binding.Consumer.ResourceGroupID == "") != (binding.Consumer.Version == "") {
		return errors.New("consumer resource-group and ownership version fences must be present together")
	}
	if binding.Consumer.Version != "" && !ValidPluginDependencyConsumerVersion(binding.Consumer.Version) {
		return errors.New("consumer ownership version must be a lowercase SHA-256 digest")
	}
	return nil
}

func ValidPluginDependencyConsumerVersion(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func pluginBindingExtensionSupported(kind string, extensions map[string]struct{}) bool {
	switch kind {
	case PluginDependencyConsumerHTTPRule:
		_, request := extensions["http.request"]
		_, response := extensions["http.response"]
		return request || response
	case PluginDependencyConsumerL4Rule:
		_, ok := extensions["l4.accept"]
		return ok
	default:
		return false
	}
}

func PluginDependencyConsumerSupportsExtensions(kind string, extensionPoints []string) bool {
	kind = strings.TrimSpace(kind)
	extensions := stringSet(extensionPoints)
	if pluginBindingExtensionSupported(kind, extensions) {
		return true
	}
	if kind == PluginDependencyConsumerHTTPRule {
		_, provider := extensions[pluginsdk.ExtensionHTTPBackendProvider]
		return provider
	}
	return false
}

func (s *GormStore) ResolvePluginInstanceBindingRequests(ctx context.Context, requests []PluginInstanceBindingRequest, providerResourceGroupID string) ([]PluginInstanceBinding, error) {
	requests, err := CanonicalPluginInstanceBindingRequests(requests)
	if err != nil {
		return nil, err
	}
	if s.transactionScoped {
		return resolvePluginInstanceBindingRequestsTx(ctx, s.db, requests, providerResourceGroupID, false)
	}
	var bindings []PluginInstanceBinding
	err = s.readSnapshotTransaction(ctx, func(scoped *GormStore) error {
		bindings, err = resolvePluginInstanceBindingRequestsTx(ctx, scoped.db, requests, providerResourceGroupID, false)
		return err
	})
	return bindings, err
}

func resolvePluginInstanceBindingRequestsTx(ctx context.Context, tx *gorm.DB, requests []PluginInstanceBindingRequest, providerResourceGroupID string, lock bool) ([]PluginInstanceBinding, error) {
	providerResourceGroupID = strings.TrimSpace(providerResourceGroupID)
	if providerResourceGroupID == "" && len(requests) != 0 {
		return nil, errors.New("plugin provider resource group is required")
	}
	result := make([]PluginInstanceBinding, 0, len(requests))
	for _, request := range requests {
		id, _ := strconv.Atoi(request.Consumer.ID)
		var coreResult *gorm.DB
		switch request.Consumer.Kind {
		case PluginDependencyConsumerHTTPRule:
			coreResult = tx.WithContext(ctx).Where("agent_id = ? AND id = ?", request.TargetAgentID, id).Limit(1).Find(&HTTPRuleRow{})
		case PluginDependencyConsumerL4Rule:
			coreResult = tx.WithContext(ctx).Where("agent_id = ? AND id = ?", request.TargetAgentID, id).Limit(1).Find(&L4RuleRow{})
		default:
			return nil, fmt.Errorf("unsupported plugin dependency consumer kind %q", request.Consumer.Kind)
		}
		if coreResult.Error != nil {
			return nil, coreResult.Error
		}
		if coreResult.RowsAffected != 1 {
			return nil, fmt.Errorf("plugin binding consumer %s %s does not exist on Agent %s", request.Consumer.Kind, request.Consumer.ID, request.TargetAgentID)
		}
		resourceID := request.TargetAgentID + ":" + request.Consumer.ID
		var owner ResourceBindingRow
		query := tx.WithContext(ctx)
		if lock {
			query = query.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		if err := query.Where("resource_kind = ? AND resource_id = ?", request.Consumer.Kind, resourceID).First(&owner).Error; err != nil {
			return nil, fmt.Errorf("plugin binding consumer %s %s ownership: %w", request.Consumer.Kind, request.Consumer.ID, err)
		}
		if strings.TrimSpace(owner.ResourceGroupID) != providerResourceGroupID {
			return nil, fmt.Errorf("plugin binding consumer %s %s belongs to resource group %s, not provider group %s", request.Consumer.Kind, request.Consumer.ID, owner.ResourceGroupID, providerResourceGroupID)
		}
		result = append(result, PluginInstanceBinding{
			Consumer: PluginDependencyConsumer{
				Kind: request.Consumer.Kind, ID: request.Consumer.ID,
				ResourceGroupID: owner.ResourceGroupID, Version: pluginDependencyConsumerOwnershipVersion(owner),
			},
			TargetAgentID: request.TargetAgentID,
		})
	}
	return result, nil
}

func pluginDependencyConsumerOwnershipVersion(binding ResourceBindingRow) string {
	payload, _ := json.Marshal(struct {
		Domain             string `json:"domain"`
		BindingID          string `json:"binding_id"`
		ResourceKind       string `json:"resource_kind"`
		ResourceID         string `json:"resource_id"`
		ResourceGroupID    string `json:"resource_group_id"`
		ParentResourceKind string `json:"parent_resource_kind"`
		ParentResourceID   string `json:"parent_resource_id"`
	}{
		Domain: "nre.plugin-consumer-ownership.v1", BindingID: strings.TrimSpace(binding.ID),
		ResourceKind: strings.TrimSpace(binding.ResourceKind), ResourceID: strings.TrimSpace(binding.ResourceID),
		ResourceGroupID: strings.TrimSpace(binding.ResourceGroupID), ParentResourceKind: strings.TrimSpace(binding.ParentResourceKind), ParentResourceID: strings.TrimSpace(binding.ParentResourceID),
	})
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func (s *GormStore) loadAgentPluginDependencies(ctx context.Context, agentID string, generations []PluginGeneration, httpRules []HTTPRule, l4Rules []L4Rule) ([]PluginDependencyEdge, error) {
	httpIDs := make(map[string]struct{}, len(httpRules))
	for _, rule := range httpRules {
		httpIDs[strconv.Itoa(rule.ID)] = struct{}{}
	}
	l4IDs := make(map[string]struct{}, len(l4Rules))
	for _, rule := range l4Rules {
		l4IDs[strconv.Itoa(rule.ID)] = struct{}{}
	}
	edges := make([]PluginDependencyEdge, 0)
	seen := make(map[string]struct{})
	generationsByInstance := make(map[string]PluginGeneration, len(generations))
	for _, generation := range generations {
		if generation.Runtime.Kind != "rpc-service" || generation.Runtime.HostScope != "agent" || generation.Target.Kind != "agent" || generation.Target.ID != agentID {
			return nil, fmt.Errorf("plugin generation %q cannot provide an Agent RPC dependency", generation.InstanceID)
		}
		var instance PluginInstanceRow
		result := s.db.WithContext(ctx).Where("id = ? AND plugin_id = ? AND desired_enabled = ?", generation.InstanceID, generation.PluginID, true).Limit(1).Find(&instance)
		if result.Error != nil {
			return nil, result.Error
		}
		if result.RowsAffected != 1 {
			return nil, fmt.Errorf("plugin generation %q has no enabled durable instance", generation.InstanceID)
		}
		generationsByInstance[generation.InstanceID] = generation
		bindingsJSON := instance.BindingsJSON
		if instance.PendingOperationID != "" && instance.PendingOperationID == generation.OperationID {
			bindingsJSON = instance.PendingBindingsJSON
		}
		bindings, err := CanonicalPluginInstanceBindings(bindingsJSON)
		if err != nil {
			return nil, fmt.Errorf("plugin instance %s bindings: %w", instance.ID, err)
		}
		extensions := stringSet(generation.ExtensionPoints)
		for _, binding := range bindings {
			if binding.TargetAgentID != agentID {
				continue
			}
			if !pluginBindingExtensionSupported(binding.Consumer.Kind, extensions) {
				return nil, fmt.Errorf("plugin instance %s binding consumer kind %q is incompatible with its generation", instance.ID, binding.Consumer.Kind)
			}
			authoritative, err := resolvePluginInstanceBindingRequestsTx(ctx, s.db, PluginInstanceBindingRequests([]PluginInstanceBinding{binding}), generation.Target.ResourceGroupID, false)
			if err != nil {
				return nil, fmt.Errorf("plugin instance %s binding authority: %w", instance.ID, err)
			}
			if len(authoritative) != 1 || authoritative[0] != binding {
				return nil, fmt.Errorf("plugin instance %s binding consumer %s %s ownership fence is stale", instance.ID, binding.Consumer.Kind, binding.Consumer.ID)
			}
			consumerExists := false
			switch binding.Consumer.Kind {
			case PluginDependencyConsumerHTTPRule:
				_, consumerExists = httpIDs[binding.Consumer.ID]
			case PluginDependencyConsumerL4Rule:
				_, consumerExists = l4IDs[binding.Consumer.ID]
			}
			if !consumerExists {
				continue
			}
			key := binding.Consumer.Kind + "\x00" + binding.Consumer.ID + "\x00" + generation.InstanceID
			if _, duplicate := seen[key]; duplicate {
				return nil, fmt.Errorf("duplicate plugin dependency %s", key)
			}
			seen[key] = struct{}{}
			edges = append(edges, PluginDependencyEdge{
				Consumer: authoritative[0].Consumer, ProviderInstanceID: generation.InstanceID,
				Target: PluginDependencyTarget{AgentID: agentID, ResourceGroupID: generation.Target.ResourceGroupID, Version: generation.Target.Version},
			})
		}
	}
	for _, rule := range httpRules {
		for _, backend := range rule.Backends {
			if backend.Kind != pluginsdk.HTTPBackendKindPluginProvider || backend.PluginProvider == nil {
				continue
			}
			generation, found := generationsByInstance[backend.PluginProvider.InstanceID]
			if !found {
				return nil, fmt.Errorf("HTTP rule %d references unavailable plugin provider instance %q", rule.ID, backend.PluginProvider.InstanceID)
			}
			if !pluginGenerationDeclaresHTTPProvider(generation, backend.PluginProvider.ProviderID) {
				return nil, fmt.Errorf("HTTP rule %d references undeclared provider %q on instance %q", rule.ID, backend.PluginProvider.ProviderID, generation.InstanceID)
			}
			request := PluginInstanceBindingRequest{Consumer: PluginInstanceBindingConsumer{Kind: PluginDependencyConsumerHTTPRule, ID: strconv.Itoa(rule.ID)}, TargetAgentID: agentID}
			authoritative, err := resolvePluginInstanceBindingRequestsTx(ctx, s.db, []PluginInstanceBindingRequest{request}, generation.Target.ResourceGroupID, false)
			if err != nil {
				return nil, fmt.Errorf("HTTP rule %d provider authority: %w", rule.ID, err)
			}
			key := PluginDependencyConsumerHTTPRule + "\x00" + strconv.Itoa(rule.ID) + "\x00" + generation.InstanceID
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			edges = append(edges, PluginDependencyEdge{
				Consumer: authoritative[0].Consumer, ProviderInstanceID: generation.InstanceID,
				Target: PluginDependencyTarget{AgentID: agentID, ResourceGroupID: generation.Target.ResourceGroupID, Version: generation.Target.Version},
			})
		}
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].Consumer.Kind != edges[j].Consumer.Kind {
			return edges[i].Consumer.Kind < edges[j].Consumer.Kind
		}
		if edges[i].Consumer.ID != edges[j].Consumer.ID {
			return edges[i].Consumer.ID < edges[j].Consumer.ID
		}
		return edges[i].ProviderInstanceID < edges[j].ProviderInstanceID
	})
	return edges, nil
}

func pluginGenerationDeclaresHTTPProvider(generation PluginGeneration, providerID string) bool {
	if !pluginGenerationContainsString(generation.ExtensionPoints, pluginsdk.ExtensionHTTPBackendProvider) ||
		!pluginGenerationContainsString(generation.RequiredFeatures, pluginsdk.RPCFeatureHTTPBackendProviderV1) {
		return false
	}
	for _, descriptor := range generation.HTTPBackendProviders {
		if descriptor.ID == providerID {
			return true
		}
	}
	return false
}

// filterUnavailablePluginProviderRules keeps a broken optional provider from
// making the entire Agent snapshot unavailable. The persisted rule remains
// visible to operators, but it is omitted from runtime publication until its
// declared provider generation is available again.
func filterUnavailablePluginProviderRules(rules []HTTPRule, generations []PluginGeneration) []HTTPRule {
	providers := make(map[string]PluginGeneration, len(generations))
	for _, generation := range generations {
		providers[generation.InstanceID] = generation
	}
	filtered := make([]HTTPRule, 0, len(rules))
	for _, rule := range rules {
		available := true
		for _, backend := range rule.Backends {
			if backend.Kind != pluginsdk.HTTPBackendKindPluginProvider || backend.PluginProvider == nil {
				continue
			}
			generation, found := providers[backend.PluginProvider.InstanceID]
			if !found || !pluginGenerationDeclaresHTTPProvider(generation, backend.PluginProvider.ProviderID) {
				available = false
				break
			}
		}
		if available {
			filtered = append(filtered, rule)
		}
	}
	return filtered
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[strings.TrimSpace(value)] = struct{}{}
	}
	return result
}

func rejectPluginConsumerOwnershipMutationTx(tx *gorm.DB, resources map[string]ResourceBindingRow) error {
	targets := make(map[string]struct{})
	for _, resource := range resources {
		if resource.ResourceKind != PluginDependencyConsumerHTTPRule && resource.ResourceKind != PluginDependencyConsumerL4Rule {
			continue
		}
		agentID, numericID, ok := splitBoundResourceID(resource.ResourceID)
		if ok {
			targets[resource.ResourceKind+"\x00"+agentID+"\x00"+strconv.Itoa(numericID)] = struct{}{}
		}
	}
	if len(targets) == 0 {
		return nil
	}
	var instances []PluginInstanceRow
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Order("id").Find(&instances).Error; err != nil {
		return err
	}
	for _, instance := range instances {
		for _, raw := range []string{instance.BindingsJSON, instance.PendingBindingsJSON, instance.RollbackBindingsJSON} {
			bindings, err := CanonicalPluginInstanceBindings(raw)
			if err != nil {
				return fmt.Errorf("plugin instance %s bindings: %w", instance.ID, err)
			}
			for _, binding := range bindings {
				key := binding.Consumer.Kind + "\x00" + binding.TargetAgentID + "\x00" + binding.Consumer.ID
				if _, found := targets[key]; found {
					return fmt.Errorf("%w: %s %s is required by plugin instance %s", ErrPluginDependencyConsumerInUse, binding.Consumer.Kind, binding.Consumer.ID, instance.ID)
				}
			}
		}
	}
	return nil
}

// rejectAgentPluginConsumerGroupMoveTx checks consumer owners that are not
// necessarily children of the Agent binding. BindResource locks the Agent
// binding class before calling this helper. Exact consumer bindings are then
// locked in canonical order, before addAgentPluginBindingsTx locks plugin
// instances, preserving the resource-binding -> plugin-instance lock order.
func rejectAgentPluginConsumerGroupMoveTx(tx *gorm.DB, agentBinding ResourceBindingRow, defaultTargetID string) error {
	type check struct {
		instanceID      string
		field           string
		binding         PluginInstanceBinding
		providerGroupID string
		ownerKey        string
	}

	var instances []PluginInstanceRow
	if err := tx.Order("id").Find(&instances).Error; err != nil {
		return err
	}
	checks := make([]check, 0)
	for _, instance := range instances {
		activeTargets, err := pluginInstanceTargets(instance.TargetJSON, defaultTargetID)
		if err != nil {
			return fmt.Errorf("plugin instance %s targets: %w", instance.ID, err)
		}
		activeGroupID := strings.TrimSpace(instance.ResourceGroupID)
		if containsPluginTarget(activeTargets, agentBinding.ResourceID) {
			activeGroupID = agentBinding.ResourceGroupID
		}
		pendingGroupID := strings.TrimSpace(instance.PendingResourceGroupID)
		if pendingGroupID == "" {
			pendingGroupID = activeGroupID
		}
		if instance.PendingOperationID != "" && strings.TrimSpace(instance.PendingTargetJSON) != "" {
			pendingTargets, err := pluginInstanceTargets(instance.PendingTargetJSON, defaultTargetID)
			if err != nil {
				return fmt.Errorf("plugin instance %s pending targets: %w", instance.ID, err)
			}
			if containsPluginTarget(pendingTargets, agentBinding.ResourceID) {
				pendingGroupID = agentBinding.ResourceGroupID
			}
		}
		fields := []struct {
			name, raw, providerGroupID string
		}{
			{name: "active", raw: instance.BindingsJSON, providerGroupID: activeGroupID},
			{name: "pending", raw: instance.PendingBindingsJSON, providerGroupID: pendingGroupID},
			{name: "rollback", raw: instance.RollbackBindingsJSON, providerGroupID: activeGroupID},
		}
		for _, field := range fields {
			bindings, err := CanonicalPluginInstanceBindings(field.raw)
			if err != nil {
				return fmt.Errorf("plugin instance %s %s bindings: %w", instance.ID, field.name, err)
			}
			for _, binding := range bindings {
				if binding.TargetAgentID != agentBinding.ResourceID {
					continue
				}
				checks = append(checks, check{
					instanceID: instance.ID, field: field.name, binding: binding,
					providerGroupID: field.providerGroupID,
					ownerKey:        binding.Consumer.Kind + "\x00" + binding.TargetAgentID + ":" + binding.Consumer.ID,
				})
			}
		}
	}
	sort.Slice(checks, func(i, j int) bool {
		if checks[i].ownerKey != checks[j].ownerKey {
			return checks[i].ownerKey < checks[j].ownerKey
		}
		if checks[i].instanceID != checks[j].instanceID {
			return checks[i].instanceID < checks[j].instanceID
		}
		return checks[i].field < checks[j].field
	})
	for _, item := range checks {
		authoritative, err := resolvePluginInstanceBindingRequestsTx(tx.Statement.Context, tx, PluginInstanceBindingRequests([]PluginInstanceBinding{item.binding}), item.providerGroupID, true)
		if err != nil {
			return fmt.Errorf("%w: plugin instance %s %s binding cannot follow Agent %s to resource group %s: %v", ErrPluginDependencyConsumerInUse, item.instanceID, item.field, agentBinding.ResourceID, item.providerGroupID, err)
		}
		if len(authoritative) != 1 || authoritative[0] != item.binding {
			return fmt.Errorf("%w: plugin instance %s %s binding ownership fence is stale", ErrPluginDependencyConsumerInUse, item.instanceID, item.field)
		}
	}
	instancesByID := make(map[string]PluginInstanceRow, len(instances))
	for _, instance := range instances {
		instancesByID[instance.ID] = instance
	}
	var rules []HTTPRuleRow
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("agent_id = ?", agentBinding.ResourceID).Order("id").Find(&rules).Error; err != nil {
		return err
	}
	for _, rule := range rules {
		for _, backend := range parseHTTPBackendsForIntent(rule.BackendsJSON) {
			if backend.Kind != pluginsdk.HTTPBackendKindPluginProvider || backend.PluginProvider == nil {
				continue
			}
			instance, found := instancesByID[backend.PluginProvider.InstanceID]
			if !found {
				return fmt.Errorf("%w: HTTP rule %d references missing plugin instance %s", ErrPluginDependencyConsumerInUse, rule.ID, backend.PluginProvider.InstanceID)
			}
			providerGroupID := strings.TrimSpace(instance.ResourceGroupID)
			targets, err := pluginInstanceTargets(instance.TargetJSON, defaultTargetID)
			if err != nil {
				return err
			}
			if containsPluginTarget(targets, agentBinding.ResourceID) {
				providerGroupID = agentBinding.ResourceGroupID
			}
			var owner ResourceBindingRow
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("resource_kind = ? AND resource_id = ?", PluginDependencyConsumerHTTPRule, agentBinding.ResourceID+":"+strconv.Itoa(rule.ID)).First(&owner).Error; err != nil {
				return fmt.Errorf("%w: HTTP rule %d provider ownership is unavailable: %v", ErrPluginDependencyConsumerInUse, rule.ID, err)
			}
			if strings.TrimSpace(owner.ResourceGroupID) != providerGroupID {
				return fmt.Errorf("%w: HTTP rule %d cannot follow Agent %s to resource group %s while provider %s is referenced", ErrPluginDependencyConsumerInUse, rule.ID, agentBinding.ResourceID, providerGroupID, instance.ID)
			}
		}
	}
	return nil
}

func detachPluginConsumerBindingsTx(tx *gorm.DB, resourceKind, resourceID string, now time.Time) error {
	if resourceKind != PluginDependencyConsumerHTTPRule && resourceKind != PluginDependencyConsumerL4Rule {
		return nil
	}
	agentID, numericID, ok := splitBoundResourceID(resourceID)
	if !ok {
		return nil
	}
	consumerID := strconv.Itoa(numericID)
	var instances []PluginInstanceRow
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Order("id").Find(&instances).Error; err != nil {
		return err
	}
	for index := range instances {
		updates := make(map[string]any)
		for column, raw := range map[string]string{
			"bindings_json":          instances[index].BindingsJSON,
			"pending_bindings_json":  instances[index].PendingBindingsJSON,
			"rollback_bindings_json": instances[index].RollbackBindingsJSON,
		} {
			bindings, err := CanonicalPluginInstanceBindings(raw)
			if err != nil {
				return fmt.Errorf("plugin instance %s bindings: %w", instances[index].ID, err)
			}
			filtered := bindings[:0]
			for _, binding := range bindings {
				if binding.Consumer.Kind == resourceKind && binding.Consumer.ID == consumerID && binding.TargetAgentID == agentID {
					continue
				}
				filtered = append(filtered, binding)
			}
			if len(filtered) == len(bindings) {
				continue
			}
			encoded, err := EncodePluginInstanceBindings(filtered)
			if err != nil {
				return err
			}
			updates[column] = encoded
		}
		if len(updates) == 0 {
			continue
		}
		updates["state_version"] = gorm.Expr("state_version + 1")
		updates["updated_at"] = now
		if err := tx.Model(&PluginInstanceRow{}).Where("id = ?", instances[index].ID).Updates(updates).Error; err != nil {
			return err
		}
	}
	return nil
}
