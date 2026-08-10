package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

const (
	PluginDependencyConsumerHTTPRule = "http_rule"
	PluginDependencyConsumerL4Rule   = "l4_rule"
)

// PluginInstanceBinding is the durable, operator-configured authority for a
// core consumer to require one plugin instance on one target Agent.
type PluginInstanceBinding struct {
	Consumer      PluginDependencyConsumer `json:"consumer"`
	TargetAgentID string                   `json:"target_agent_id"`
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
	return nil
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
	return pluginBindingExtensionSupported(strings.TrimSpace(kind), stringSet(extensionPoints))
}

func (s *GormStore) PluginDependencyConsumerState(ctx context.Context, binding PluginInstanceBinding) (exists, enabled bool, err error) {
	if err := validatePluginInstanceBinding(binding); err != nil {
		return false, false, err
	}
	id, _ := strconv.Atoi(binding.Consumer.ID)
	switch binding.Consumer.Kind {
	case PluginDependencyConsumerHTTPRule:
		var row HTTPRuleRow
		result := s.db.WithContext(ctx).Where("agent_id = ? AND id = ?", binding.TargetAgentID, id).Limit(1).Find(&row)
		return result.RowsAffected == 1, row.Enabled, result.Error
	case PluginDependencyConsumerL4Rule:
		var row L4RuleRow
		result := s.db.WithContext(ctx).Where("agent_id = ? AND id = ?", binding.TargetAgentID, id).Limit(1).Find(&row)
		return result.RowsAffected == 1, row.Enabled, result.Error
	default:
		return false, false, nil
	}
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
				Consumer: binding.Consumer, ProviderInstanceID: generation.InstanceID,
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

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[strings.TrimSpace(value)] = struct{}{}
	}
	return result
}
