package storage

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"sort"

	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"gorm.io/gorm"
)

func (s *GormStore) ListDatasetConsumptions(ctx context.Context, source string) ([]PluginDatasetConsumptionRow, error) {
	var rows []PluginDatasetConsumptionRow
	query := s.db.WithContext(ctx)
	if source != "" {
		query = query.Where("source_id = ?", source)
	}
	err := query.Find(&rows).Error
	return rows, err
}

// Logical intent survives an empty RPC target list. Expansion uses the actual
// instance/face producer rules and never writes during snapshot reads.
func (s *GormStore) ResolveDatasetBindings(ctx context.Context, agent string) ([]DatasetBindingRow, error) {
	var physical []DatasetBindingRow
	if err := s.db.WithContext(ctx).Where("agent_id = ?", agent).Find(&physical).Error; err != nil {
		return nil, err
	}
	rows, err := s.ListDatasetConsumptions(ctx, "")
	if err != nil {
		return nil, err
	}
	logical := map[string]bool{}
	for _, row := range rows {
		logical[row.InstanceID+"\x00"+row.SourceID] = true
	}
	result := []DatasetBindingRow{}
	for _, row := range physical {
		if !logical[row.InstanceID+"\x00"+row.SourceID] {
			result = append(result, row)
		}
	}
	for _, row := range rows {
		if row.RecordJSON == "" {
			continue
		}
		var record sdk.DatasetBindingRecord
		if json.Unmarshal([]byte(row.RecordJSON), &record) != nil || record.Validate() != nil {
			return nil, errors.New("logical dataset binding is invalid")
		}
		instance, found, err := s.GetPluginInstance(ctx, row.InstanceID)
		if err != nil {
			return nil, err
		}
		if !found || !instance.DesiredEnabled {
			continue
		}
		installed, found, err := s.GetInstalledPlugin(ctx, instance.PluginID)
		if err != nil {
			return nil, err
		}
		if !found || installed.DesiredLifecycle != "enabled" {
			continue
		}
		identity := installed.ActivePackageIdentity
		targetJSON := instance.TargetJSON
		if instance.PendingOperationID != "" && instance.PendingOperationID == installed.PendingOperationID {
			if installed.StagedPackageIdentity != "" {
				identity = installed.StagedPackageIdentity
			}
			if instance.PendingTargetJSON != "" {
				targetJSON = instance.PendingTargetJSON
			}
		}
		pkg, found, err := s.GetPluginPackageByIdentity(ctx, identity)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		var manifest sdk.Manifest
		if json.Unmarshal([]byte(pkg.ManifestJSON), &manifest) != nil {
			return nil, errors.New("binding instance manifest is invalid")
		}
		eligible := false
		if sdk.RuntimeProjectsControlPlaneUIAndAgentPolicy(manifest.Runtime) {
			eligible = true
		} else if sdk.RuntimeProjectsAgentRPC(manifest.Runtime) {
			targets, err := pluginInstanceExplicitTargets(targetJSON)
			if err != nil {
				return nil, err
			}
			eligible = slices.Contains(targets, agent)
		} else if sdk.RuntimeProjectsAgentPolicy(manifest.Runtime) {
			instance.TargetJSON = targetJSON
			_, eligible, err = agentPolicyTargetKey(manifest.Runtime, instance, agent, s.LocalAgentID())
			if err != nil {
				return nil, err
			}
		}
		if record.Targets.Mode == sdk.ExecutionTargetsSubset && !slices.Contains(record.Targets.AgentIDs, agent) {
			eligible = false
		}
		if !eligible {
			continue
		}
		classes, _ := json.Marshal(record.Spec.Classifications)
		result = append(result, DatasetBindingRow{AgentID: agent, InstanceID: row.InstanceID, SourceID: row.SourceID, VersionDigest: record.Spec.VersionDigest, ClassificationsJSON: string(classes)})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].SourceID == result[j].SourceID {
			return result[i].InstanceID < result[j].InstanceID
		}
		return result[i].SourceID < result[j].SourceID
	})
	return result, nil
}

func advanceLogicalDatasetVersion(tx *gorm.DB, source, digest string) error {
	var rows []PluginDatasetConsumptionRow
	if err := tx.Where("source_id = ? AND record_json <> ''", source).Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		var record sdk.DatasetBindingRecord
		if json.Unmarshal([]byte(row.RecordJSON), &record) != nil || record.Validate() != nil {
			return errors.New("logical dataset binding is invalid")
		}
		if record.Spec.VersionDigest == digest {
			continue
		}
		if row.Revision == math.MaxUint64 {
			return ErrPluginConflict
		}
		record.Spec.VersionDigest = digest
		encoded, err := json.Marshal(record)
		if err != nil {
			return err
		}
		row.Revision++
		record.Revision = row.Revision
		encoded, err = json.Marshal(record)
		if err != nil {
			return err
		}
		row.RecordJSON = string(encoded)
		if err := tx.Save(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

func deletePluginConsumptionTx(tx *gorm.DB, ids []string) error {
	for _, model := range []any{&PluginPolicySettingsRow{}, &PluginPolicyEntryModeRow{}, &PluginDatasetConsumptionRow{}, &DatasetBindingRow{}} {
		if err := tx.Where("instance_id IN ?", ids).Delete(model).Error; err != nil {
			return err
		}
	}
	// Reference-only operation outcomes are retained for audit and cannot confer
	// authority after deletion: every replay first resolves the live instance.
	return nil
}

func (s *GormStore) DatasetConsumerBindings(ctx context.Context, source string) ([]DatasetBindingRow, error) {
	physical, err := s.DatasetBindings(ctx, source)
	if err != nil {
		return nil, err
	}
	nodes, err := s.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	ids := map[string]bool{s.LocalAgentID(): true}
	for _, node := range nodes {
		ids[node.ID] = true
	}
	for _, row := range physical {
		ids[row.AgentID] = true
	}
	seen := map[string]bool{}
	result := append([]DatasetBindingRow(nil), physical...)
	for _, row := range physical {
		seen[row.AgentID+"\x00"+row.InstanceID] = true
	}
	for id := range ids {
		rows, err := s.ResolveDatasetBindings(ctx, id)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			key := row.AgentID + "\x00" + row.InstanceID
			if row.SourceID == source && !seen[key] {
				seen[key] = true
				result = append(result, row)
			}
		}
	}
	return result, nil
}
