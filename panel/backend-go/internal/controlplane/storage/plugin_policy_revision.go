package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *GormStore) capturePluginPolicyCatalogs(ctx context.Context, agents []string) (map[string]string, error) {
	result := make(map[string]string, len(agents))
	for _, agentID := range agents {
		semantic, err := s.semanticPluginPolicyCatalog(ctx, agentID)
		if err != nil {
			return nil, err
		}
		result[agentID] = semantic
	}
	return result, nil
}

func (s *GormStore) reconcilePluginPolicyCatalogRevisions(ctx context.Context, agents []string, before map[string]string, now time.Time) error {
	after, err := s.capturePluginPolicyCatalogs(ctx, agents)
	if err != nil {
		return err
	}
	if err := s.validatePluginPolicyReferenceTransition(ctx, before, after); err != nil {
		return err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	for _, agentID := range agents {
		left, ok := before[agentID]
		if !ok {
			return fmt.Errorf("plugin policy catalog before-image for agent %q is missing", agentID)
		}
		right, ok := after[agentID]
		if !ok {
			return fmt.Errorf("plugin policy catalog after-image for agent %q is missing", agentID)
		}
		if left == right {
			continue
		}
		if err := s.bumpPluginPolicyCatalogRevision(ctx, agentID, now); err != nil {
			return err
		}
	}
	return nil
}

func (s *GormStore) validateProspectivePluginPolicyTransition(ctx context.Context, mutation PluginMutation, agents []string, before map[string]string) error {
	if mutation.CompleteOperation || (mutation.Operation.Status != "applying" && mutation.Operation.Status != "staged") {
		return nil
	}
	const savepoint = "plugin_policy_prospective"
	if err := s.db.WithContext(ctx).SavePoint(savepoint).Error; err != nil {
		return err
	}
	rollback := func() error { return s.db.WithContext(ctx).RollbackTo(savepoint).Error }
	var prospectiveErr error
	func() {
		var installed InstalledPluginRow
		if err := s.db.WithContext(ctx).Where("plugin_id = ?", mutation.PluginID).First(&installed).Error; err != nil {
			prospectiveErr = err
			return
		}
		if installed.StagedPackageIdentity != "" && installed.StagedPackageDigest != "" {
			var candidate PluginPackageRow
			if err := s.db.WithContext(ctx).Where("identity = ? AND digest = ?", installed.StagedPackageIdentity, installed.StagedPackageDigest).First(&candidate).Error; err != nil {
				prospectiveErr = err
				return
			}
			installed.ActivePackageDigest = installed.StagedPackageDigest
			installed.ActivePackageIdentity = installed.StagedPackageIdentity
			installed.RuntimeKind, installed.RuntimeABI, installed.HostScope = candidate.RuntimeKind, candidate.RuntimeABI, candidate.HostScope
			installed.ActiveSourceID, installed.ActiveSourceKind, installed.ActiveSourceRiskLabel = installed.StagedSourceID, installed.StagedSourceKind, installed.StagedSourceRiskLabel
			installed.ActiveSignatureKeyID, installed.ActiveSignaturePublicKey, installed.ActiveSignatureFingerprint = installed.StagedSignatureKeyID, installed.StagedSignaturePublicKey, installed.StagedSignatureFingerprint
		}
		if installed.DesiredLifecycle == "enabled" {
			installed.CurrentLifecycle = "active"
		} else {
			installed.CurrentLifecycle = "disabled"
		}
		if err := s.db.WithContext(ctx).Model(&InstalledPluginRow{}).Where("plugin_id = ?", installed.PluginID).Select(
			"active_package_digest", "active_package_identity", "runtime_kind", "runtime_abi", "host_scope",
			"active_source_id", "active_source_kind", "active_source_risk_label", "active_signature_key_id",
			"active_signature_public_key", "active_signature_fingerprint", "current_lifecycle",
		).Updates(&installed).Error; err != nil {
			prospectiveErr = err
			return
		}
		var instances []PluginInstanceRow
		if err := s.db.WithContext(ctx).Where("plugin_id = ?", mutation.PluginID).Find(&instances).Error; err != nil {
			prospectiveErr = err
			return
		}
		for _, instance := range instances {
			if instance.PendingOperationID != mutation.Operation.ID || instance.PendingVersion == 0 {
				continue
			}
			updates := map[string]any{
				"config_json": instance.PendingConfigJSON, "config_version": instance.PendingVersion,
				"policy_chains_json": instance.PendingPolicyChainsJSON,
			}
			if instance.PendingTargetJSON != "" {
				updates["target_json"] = instance.PendingTargetJSON
			}
			if instance.PendingResourceGroupID != "" {
				updates["resource_group_id"] = instance.PendingResourceGroupID
			}
			if err := s.db.WithContext(ctx).Model(&PluginInstanceRow{}).Where("id = ?", instance.ID).Updates(updates).Error; err != nil {
				prospectiveErr = err
				return
			}
		}
		after, err := s.capturePluginPolicyCatalogs(ctx, agents)
		if err != nil {
			prospectiveErr = err
			return
		}
		prospectiveErr = s.validatePluginPolicyReferenceTransition(ctx, before, after)
	}()
	return errors.Join(prospectiveErr, rollback())
}

func (s *GormStore) validatePluginPolicyReferenceTransition(ctx context.Context, before, after map[string]string) error {
	agents := make(map[string]struct{}, len(before)+len(after))
	for agentID := range before {
		agents[agentID] = struct{}{}
	}
	for agentID := range after {
		agents[agentID] = struct{}{}
	}
	for agentID := range agents {
		beforePolicies, err := decodeSemanticPluginPolicies(before[agentID])
		if err != nil {
			return err
		}
		afterPolicies, err := decodeSemanticPluginPolicies(after[agentID])
		if err != nil {
			return err
		}
		if err := s.validateHTTPPolicyReferenceTransition(ctx, agentID, beforePolicies, afterPolicies); err != nil {
			return err
		}
		if err := s.validateL4PolicyReferenceTransition(ctx, agentID, beforePolicies, afterPolicies); err != nil {
			return err
		}
	}
	return nil
}

func decodeSemanticPluginPolicies(raw string) (map[string]PluginPolicy, error) {
	result := make(map[string]PluginPolicy)
	if raw == "" {
		return result, nil
	}
	var policies []PluginPolicy
	if err := json.Unmarshal([]byte(raw), &policies); err != nil {
		return nil, err
	}
	for _, policy := range policies {
		result[policy.ID] = policy
	}
	return result, nil
}

func (s *GormStore) validateHTTPPolicyReferenceTransition(ctx context.Context, agentID string, before, after map[string]PluginPolicy) error {
	var rows []HTTPRuleRow
	if err := s.db.WithContext(ctx).Clauses(clause.Locking{Strength: "SHARE"}).Where("agent_id = ? AND enabled = ?", agentID, true).Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		ref := parsePolicyRef(row.PolicyRefJSON)
		if ref == nil {
			continue
		}
		previous, previouslyAvailable := before[ref.ID]
		candidate, available := after[ref.ID]
		if previouslyAvailable && !available {
			return fmt.Errorf("%w: HTTP rule %d policy chain %q would become unavailable", ErrPluginConflict, row.ID, ref.ID)
		}
		if available && !policySupportsRule(candidate, "http.request", ref.Overlay) && (!previouslyAvailable || policySupportsRule(previous, "http.request", ref.Overlay)) {
			return fmt.Errorf("%w: HTTP rule %d policy chain %q does not support its http.request overlay budget", ErrPluginConflict, row.ID, ref.ID)
		}
	}
	return nil
}

func (s *GormStore) validateL4PolicyReferenceTransition(ctx context.Context, agentID string, before, after map[string]PluginPolicy) error {
	var rows []L4RuleRow
	if err := s.db.WithContext(ctx).Clauses(clause.Locking{Strength: "SHARE"}).Where("agent_id = ? AND enabled = ?", agentID, true).Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		ref := parsePolicyRef(row.PolicyRefJSON)
		if ref == nil {
			continue
		}
		previous, previouslyAvailable := before[ref.ID]
		candidate, available := after[ref.ID]
		if previouslyAvailable && !available {
			return fmt.Errorf("%w: L4 rule %d policy chain %q would become unavailable", ErrPluginConflict, row.ID, ref.ID)
		}
		if available && !policySupportsRule(candidate, "l4.accept", ref.Overlay) && (!previouslyAvailable || policySupportsRule(previous, "l4.accept", ref.Overlay)) {
			return fmt.Errorf("%w: L4 rule %d policy chain %q does not support its l4.accept overlay budget", ErrPluginConflict, row.ID, ref.ID)
		}
	}
	return nil
}

func policySupportsRule(policy PluginPolicy, extension string, overlay json.RawMessage) bool {
	if len(policy.Stages) == 0 {
		return false
	}
	frameBytes, err := pluginsdk.PolicyV1EvaluateRequestFrameBytes(extension, strings.Repeat("r", pluginsdk.PolicyRequestIDMaxBytes), overlay)
	if err != nil {
		return false
	}
	for _, stage := range policy.Stages {
		if stage.Kind == "waf" && extension == "l4.accept" {
			return false
		}
		found := false
		for _, point := range stage.ExtensionPoints {
			if point == extension {
				found = true
				break
			}
		}
		if !found {
			return false
		}
		if stage.ResourceBudget.InputBytes < int64(frameBytes) {
			return false
		}
	}
	return true
}

func (s *GormStore) pluginMutationPolicyAgents(ctx context.Context, mutation PluginMutation) ([]string, error) {
	var instances []PluginInstanceRow
	if err := s.db.WithContext(ctx).Where("plugin_id = ?", mutation.PluginID).Order("id").Find(&instances).Error; err != nil {
		return nil, err
	}
	result := make(map[string]struct{})
	addInstanceTargets := func(instance PluginInstanceRow) error {
		if err := ValidatePluginPolicyIdentity(instance.ID); err != nil {
			return fmt.Errorf("plugin instance identity %q: %w", instance.ID, err)
		}
		targets, err := pluginInstanceTargets(instance.TargetJSON, s.LocalAgentID())
		if err != nil {
			return fmt.Errorf("plugin instance %s targets: %w", instance.ID, err)
		}
		for _, target := range targets {
			result[target] = struct{}{}
		}
		if strings.TrimSpace(instance.PendingTargetJSON) != "" {
			pendingTargets, err := pluginInstanceTargets(instance.PendingTargetJSON, s.LocalAgentID())
			if err != nil {
				return fmt.Errorf("plugin instance %s pending targets: %w", instance.ID, err)
			}
			for _, target := range pendingTargets {
				result[target] = struct{}{}
			}
		}
		return nil
	}
	for _, instance := range instances {
		if err := addInstanceTargets(instance); err != nil {
			return nil, err
		}
	}
	if mutation.ReplaceInstance != nil {
		if err := addInstanceTargets(*mutation.ReplaceInstance); err != nil {
			return nil, err
		}
	}
	for _, instance := range mutation.ReplaceInstances {
		if err := addInstanceTargets(instance); err != nil {
			return nil, err
		}
	}
	agents := make([]string, 0, len(result))
	for agentID := range result {
		agents = append(agents, agentID)
	}
	sort.Strings(agents)
	return agents, nil
}

// lockPluginPolicyAgentCatalogs is the single serialization boundary shared by
// plugin lifecycle mutations and rule validation. Rows are inserted and locked
// in canonical Agent order, avoiding graph-wide shared-lock/upgraded-lock cycles.
func (s *GormStore) lockPluginPolicyAgentCatalogs(ctx context.Context, agents []string, now time.Time) error {
	if len(agents) == 0 {
		return nil
	}
	ordered := append([]string(nil), agents...)
	for index := range ordered {
		ordered[index] = s.resolveAgentID(ordered[index])
	}
	sort.Strings(ordered)
	ordered = slicesCompactStrings(ordered)
	if now.IsZero() {
		now = time.Now().UTC()
	}
	for _, agentID := range ordered {
		row := PluginPolicyAgentRevisionRow{AgentID: agentID, Revision: 0, UpdatedAt: now}
		if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "agent_id"}},
			DoNothing: true,
		}).Create(&row).Error; err != nil {
			return err
		}
	}
	var locked []PluginPolicyAgentRevisionRow
	if err := s.db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("agent_id IN ?", ordered).Order("agent_id").Find(&locked).Error; err != nil {
		return err
	}
	if len(locked) != len(ordered) {
		return fmt.Errorf("plugin policy Agent catalog fence is incomplete")
	}
	return nil
}

func slicesCompactStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	write := 1
	for _, value := range values[1:] {
		if value == values[write-1] {
			continue
		}
		values[write] = value
		write++
	}
	return values[:write]
}

func (s *GormStore) semanticPluginPolicyCatalog(ctx context.Context, agentID string) (string, error) {
	policies, err := s.loadAgentPluginPolicies(ctx, agentID)
	if err != nil {
		return "", err
	}
	if policies == nil {
		policies = []PluginPolicy{}
	}
	for index := range policies {
		policies[index].Revision = 0
	}
	encoded, err := json.Marshal(policies)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func (s *GormStore) bumpPluginPolicyCatalogRevision(ctx context.Context, agentID string, now time.Time) error {
	agentID = s.resolveAgentID(agentID)
	var current PluginPolicyAgentRevisionRow
	err := s.db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("agent_id = ?", agentID).First(&current).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	snapshot, err := s.LoadAgentIntentSnapshot(ctx, agentID, AgentSnapshotInput{})
	if err != nil {
		return fmt.Errorf("load agent %s revision floor: %w", agentID, err)
	}
	floor := maxInt64(current.Revision, snapshot.Revision)
	var historical int64
	if err := s.db.WithContext(ctx).Model(&AgentRevisionRow{}).Where("agent_id = ?", agentID).Select("COALESCE(MAX(revision), 0)").Scan(&historical).Error; err != nil {
		return err
	}
	floor = maxInt64(floor, historical)
	var pointer AgentRevisionPointerRow
	if err := s.db.WithContext(ctx).Where("agent_id = ?", agentID).First(&pointer).Error; err == nil {
		floor = maxInt64(floor, pointer.DesiredRevision, pointer.AppliedRevision, pointer.LastKnownGoodRevision)
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if floor == math.MaxInt64 {
		return fmt.Errorf("agent %q policy catalog revision space is exhausted", agentID)
	}
	next := PluginPolicyAgentRevisionRow{AgentID: agentID, Revision: floor + 1, UpdatedAt: now}
	if current.AgentID == "" {
		return s.db.WithContext(ctx).Create(&next).Error
	}
	result := s.db.WithContext(ctx).Model(&PluginPolicyAgentRevisionRow{}).
		Where("agent_id = ? AND revision = ?", agentID, current.Revision).
		Updates(map[string]any{"revision": next.Revision, "updated_at": now})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("%w: policy catalog revision changed concurrently", ErrPluginConflict)
	}
	return nil
}

func maxInt64(values ...int64) int64 {
	var result int64
	for _, value := range values {
		if value > result {
			result = value
		}
	}
	return result
}
