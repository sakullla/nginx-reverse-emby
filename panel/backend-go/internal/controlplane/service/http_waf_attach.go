package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

const (
	officialWAFOverlayModeObserve = "observe"
	officialWAFOverlayModeDeny    = "deny"
)

var officialWAFObserveOverlay = json.RawMessage(`{"mode":"observe"}`)

type httpPolicyAttachStore interface {
	ListAgents(context.Context) ([]storage.AgentRow, error)
	ListHTTPRules(context.Context, string) ([]storage.HTTPRuleRow, error)
	SaveHTTPRules(context.Context, string, []storage.HTTPRuleRow) error
}

func officialDualFaceWAF(manifest plugins.Manifest) bool {
	return pluginsdk.RuntimeProjectsControlPlaneUIAndAgentPolicy(manifest.Runtime) &&
		strings.EqualFold(strings.TrimSpace(manifest.Runtime.PolicyKind), "waf")
}

func officialWAFPolicyID(policies []storage.PluginPolicy) string {
	ids := make([]string, 0, len(policies))
	for _, policy := range policies {
		if !pluginPolicyIsOfficialWAF(policy) {
			continue
		}
		id := strings.TrimSpace(policy.ID)
		if id == "" {
			continue
		}
		ids = append(ids, id)
	}
	slices.Sort(ids)
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

func pluginPolicyIsOfficialWAF(policy storage.PluginPolicy) bool {
	if len(policy.Stages) != 1 {
		return false
	}
	stage := policy.Stages[0]
	return strings.EqualFold(strings.TrimSpace(stage.Kind), "waf") &&
		slices.Contains(stage.ExtensionPoints, policyExtensionHTTP)
}

func officialWAFObservePolicyRef(id string) *storage.PolicyRef {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	return &storage.PolicyRef{ID: id, Overlay: append(json.RawMessage(nil), officialWAFObserveOverlay...)}
}

func defaultOfficialWAFPolicyRef(policies []storage.PluginPolicy, current *storage.PolicyRef) *storage.PolicyRef {
	if current != nil && strings.TrimSpace(current.ID) != "" {
		return cloneRulePolicyRef(current)
	}
	return officialWAFObservePolicyRef(officialWAFPolicyID(policies))
}

func validateWAFPolicyOverlay(overlay json.RawMessage) error {
	overlay = bytes.TrimSpace(overlay)
	if len(overlay) == 0 || string(overlay) == "null" {
		return nil
	}
	if !json.Valid(overlay) {
		return fmt.Errorf("%w: policy_ref overlay is invalid", ErrInvalidArgument)
	}
	decoder := json.NewDecoder(bytes.NewReader(overlay))
	decoder.DisallowUnknownFields()
	var parsed struct {
		Mode string `json:"mode"`
	}
	if err := decoder.Decode(&parsed); err != nil {
		return fmt.Errorf("%w: policy_ref overlay is invalid", ErrInvalidArgument)
	}
	if decoder.More() {
		return fmt.Errorf("%w: policy_ref overlay is invalid", ErrInvalidArgument)
	}
	switch strings.TrimSpace(parsed.Mode) {
	case officialWAFOverlayModeObserve, officialWAFOverlayModeDeny:
		return nil
	default:
		return fmt.Errorf("%w: policy_ref overlay is invalid", ErrInvalidArgument)
	}
}

func applyDefaultOfficialWAFPolicyRef(ctx context.Context, store any, agentID string, current *storage.PolicyRef) (*storage.PolicyRef, error) {
	if current != nil && strings.TrimSpace(current.ID) != "" {
		return cloneRulePolicyRef(current), nil
	}
	catalogStore, ok := store.(rulePolicyCatalogStore)
	if !ok {
		return cloneRulePolicyRef(current), nil
	}
	policies, err := catalogStore.LoadAgentPluginPolicies(ctx, agentID)
	if err != nil {
		return nil, fmt.Errorf("resolve default waf policy_ref: %w", err)
	}
	return defaultOfficialWAFPolicyRef(policies, current), nil
}

func pluginInstanceIDs(instances []storage.PluginInstanceRow) []string {
	ids := make([]string, 0, len(instances))
	seen := make(map[string]struct{}, len(instances))
	for _, instance := range instances {
		id := strings.TrimSpace(instance.ID)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func (s *PluginService) attachOfficialWAFHTTPPolicyRefs(ctx context.Context, pluginID string, instances []storage.PluginInstanceRow) error {
	if s == nil {
		return nil
	}
	ok, err := s.pluginIsOfficialDualFaceWAF(ctx, pluginID)
	if err != nil || !ok {
		return err
	}
	ids := pluginInstanceIDs(instances)
	if len(ids) == 0 {
		return nil
	}
	return s.rewriteOfficialWAFHTTPPolicyRefs(ctx, ids[0], ids, true)
}

func (s *PluginService) detachOfficialWAFHTTPPolicyRefs(ctx context.Context, instanceIDs []string) error {
	if s == nil || len(instanceIDs) == 0 {
		return nil
	}
	return s.rewriteOfficialWAFHTTPPolicyRefs(ctx, "", instanceIDs, false)
}

func (s *PluginService) pluginIsOfficialDualFaceWAF(ctx context.Context, pluginID string) (bool, error) {
	installed, ok, err := s.store.GetInstalledPlugin(ctx, pluginID)
	if err != nil || !ok {
		return false, err
	}
	packageRow, exists, err := s.storedPackage(ctx, installed.ActivePackageIdentity, installed.ActivePackageDigest)
	if err != nil || !exists {
		return false, err
	}
	var manifest plugins.Manifest
	if err := json.Unmarshal([]byte(packageRow.ManifestJSON), &manifest); err != nil {
		return false, err
	}
	return officialDualFaceWAF(manifest), nil
}

func (s *PluginService) rewriteOfficialWAFHTTPPolicyRefs(ctx context.Context, attachID string, instanceIDs []string, attach bool) error {
	store, ok := s.store.(httpPolicyAttachStore)
	if !ok {
		return nil
	}
	agentIDs, err := s.httpPolicyAttachAgentIDs(ctx, store)
	if err != nil {
		return err
	}
	detach := make(map[string]struct{}, len(instanceIDs))
	for _, id := range instanceIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		detach[id] = struct{}{}
	}
	for _, agentID := range agentIDs {
		rows, err := store.ListHTTPRules(ctx, agentID)
		if err != nil {
			return err
		}
		changed := false
		nextRevision := 0
		for _, row := range rows {
			if row.Revision > nextRevision {
				nextRevision = row.Revision
			}
		}
		nextRevision = configMutationRevision(s.revisionNumbers, agentID, nextRevision+1)
		for index, row := range rows {
			var nextJSON string
			var mutated bool
			if attach {
				nextJSON, mutated = attachOfficialWAFPolicyRefJSON(row.PolicyRefJSON, attachID)
			} else {
				nextJSON, mutated = detachOfficialWAFPolicyRefJSON(row.PolicyRefJSON, detach)
			}
			if !mutated {
				continue
			}
			rows[index].PolicyRefJSON = nextJSON
			rows[index].Revision = nextRevision
			changed = true
		}
		if !changed {
			continue
		}
		if err := store.SaveHTTPRules(ctx, agentID, rows); err != nil {
			return err
		}
	}
	return nil
}

func (s *PluginService) httpPolicyAttachAgentIDs(ctx context.Context, store httpPolicyAttachStore) ([]string, error) {
	agents, err := store.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(agents)+1)
	for _, agent := range agents {
		if id := strings.TrimSpace(agent.ID); id != "" {
			ids = append(ids, id)
		}
	}
	if localID, err := s.defaultPluginTargetID(ctx); err == nil && strings.TrimSpace(localID) != "" {
		ids = append(ids, strings.TrimSpace(localID))
	} else if id := strings.TrimSpace(s.cfg.LocalAgentID); id != "" {
		ids = append(ids, id)
	}
	return uniqueAgentIDs(ids), nil
}

func attachOfficialWAFPolicyRefJSON(policyRefJSON, policyID string) (string, bool) {
	policyID = strings.TrimSpace(policyID)
	if policyID == "" {
		return policyRefJSON, false
	}
	ref := parseRulePolicyRef(policyRefJSON)
	if ref != nil && strings.TrimSpace(ref.ID) != "" {
		return policyRefJSON, false
	}
	return marshalJSON(officialWAFObservePolicyRef(policyID), ""), true
}

func detachOfficialWAFPolicyRefJSON(policyRefJSON string, instanceIDs map[string]struct{}) (string, bool) {
	ref := parseRulePolicyRef(policyRefJSON)
	if ref == nil || strings.TrimSpace(ref.ID) == "" {
		return policyRefJSON, false
	}
	if _, ok := instanceIDs[strings.TrimSpace(ref.ID)]; !ok {
		return policyRefJSON, false
	}
	return "", true
}
