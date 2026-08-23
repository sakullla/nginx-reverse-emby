package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/marketplace"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrPluginAlreadyInstalled  = errors.New("plugin already installed")
	ErrPluginNotInstalled      = errors.New("plugin not installed")
	ErrMarketplaceSourceExists = errors.New("marketplace source already exists")
	ErrMarketplaceCatalogStale = errors.New("marketplace catalog provenance is stale")
	ErrPluginInstanceScope     = errors.New("invalid plugin instance scope")
	ErrPluginConflict          = errors.New("plugin state conflict")
)

// ProjectPluginPackage binds the durable projection to the single current
// runtime-aware manifest contract. Callers still persist the canonical
// manifest JSON, while these columns and rows support safe runtime selection
// without interpreting arbitrary package content.
func ProjectPluginPackage(row PluginPackageRow, manifest plugins.Manifest) (PluginPackageRow, []PluginArtifactRow, error) {
	if row.Identity == "" {
		row.Identity = PluginPackageIdentity(row.Digest, row.SourceID, row.SignatureFingerprint)
	}
	budgetJSON, err := json.Marshal(manifest.ResourceBudget)
	if err != nil {
		return PluginPackageRow{}, nil, err
	}
	failureJSON, err := json.Marshal(manifest.FailurePolicy)
	if err != nil {
		return PluginPackageRow{}, nil, err
	}
	row.RuntimeKind = strings.TrimSpace(manifest.Runtime.Kind)
	row.RuntimeABI = strings.TrimSpace(manifest.Runtime.ABI)
	row.HostScope = strings.TrimSpace(manifest.Runtime.HostScope)
	row.PolicyKind = strings.TrimSpace(manifest.Runtime.PolicyKind)
	row.EntryPath = strings.TrimSpace(manifest.Runtime.Entry)
	row.SignatureKeyID = strings.TrimSpace(manifest.Signature.KeyID)
	row.SignatureVerdict = "verified"
	row.ResourceBudgetJSON = string(budgetJSON)
	row.FailurePolicyJSON = string(failureJSON)
	if row.RuntimeKind == "" || row.RuntimeABI == "" || row.HostScope == "" || (row.RuntimeKind == "wasm-policy" && row.PolicyKind == "") || row.EntryPath == "" || row.SignatureKeyID == "" || len(manifest.Artifacts) == 0 {
		return PluginPackageRow{}, nil, errors.New("runtime-aware plugin package projection is incomplete")
	}
	artifacts := make([]PluginArtifactRow, 0, len(manifest.Artifacts))
	for _, artifact := range manifest.Artifacts {
		artifactPath := strings.TrimSpace(artifact.Path)
		artifactDigest := strings.ToLower(strings.TrimSpace(artifact.SHA256))
		if artifactPath == "" || !marketplace.IsDigest(artifactDigest) || artifact.Size < 0 {
			return PluginPackageRow{}, nil, errors.New("runtime artifact projection is invalid")
		}
		artifacts = append(artifacts, PluginArtifactRow{
			ID: pluginStorageDigest(row.Identity, artifactPath), PackageIdentity: row.Identity, PackageDigest: strings.ToLower(strings.TrimSpace(row.Digest)),
			Path: artifactPath, SHA256: artifactDigest, SizeBytes: artifact.Size, Mode: strings.TrimSpace(artifact.Mode), RuntimeKind: row.RuntimeKind,
			RuntimeABI: row.RuntimeABI, HostScope: row.HostScope, GOOS: strings.TrimSpace(artifact.GOOS), GOARCH: strings.TrimSpace(artifact.GOARCH),
		})
	}
	return row, artifacts, nil
}

// PluginPackageIdentity separates source-bound signature envelopes while the
// externally visible package digest remains the canonical content digest.
func PluginPackageIdentity(digest, sourceID, signerFingerprint string) string {
	digest = strings.ToLower(strings.TrimSpace(digest))
	if sourceID == "" || signerFingerprint == "" {
		return digest
	}
	return pluginStorageDigest(digest, strings.TrimSpace(sourceID), strings.ToLower(strings.TrimSpace(signerFingerprint)))
}

// BindPluginOperationPackage projects the immutable package and signer tuple
// used by every package-targeting lifecycle operation.
func BindPluginOperationPackage(operation *PluginOperationRow, candidate PluginPackageRow) error {
	if operation == nil {
		return errors.New("plugin operation is required")
	}
	operation.TargetPackageDigest = strings.ToLower(strings.TrimSpace(candidate.Digest))
	operation.TargetPackageIdentity = strings.ToLower(strings.TrimSpace(candidate.Identity))
	if operation.TargetPackageIdentity == "" {
		operation.TargetPackageIdentity = PluginPackageIdentity(operation.TargetPackageDigest, candidate.SourceID, candidate.SignatureFingerprint)
	}
	operation.SourceID = strings.TrimSpace(candidate.SourceID)
	operation.SourceKind = strings.TrimSpace(candidate.SourceKind)
	operation.SourceRiskLabel = strings.TrimSpace(candidate.SourceRiskLabel)
	operation.SourceRevision = candidate.SourceRevision
	operation.SourceRefKind = strings.TrimSpace(candidate.SourceRefKind)
	operation.SourceRefName = strings.TrimSpace(candidate.SourceRefName)
	operation.SourceResolvedOID = strings.ToLower(strings.TrimSpace(candidate.SourceResolvedOID))
	operation.TargetSignatureKeyID = strings.TrimSpace(candidate.SignatureKeyID)
	operation.TargetSignaturePublicKey = strings.TrimSpace(candidate.SignaturePublicKey)
	operation.TargetSignatureFingerprint = strings.ToLower(strings.TrimSpace(candidate.SignatureFingerprint))
	return validatePluginOperationPackageProvenance(*operation, &candidate)
}

func validatePluginOperationPackageProvenance(operation PluginOperationRow, candidate *PluginPackageRow) error {
	digest := strings.ToLower(strings.TrimSpace(operation.TargetPackageDigest))
	identity := strings.ToLower(strings.TrimSpace(operation.TargetPackageIdentity))
	trust := marketplace.SignatureTrust{
		SourceID: strings.TrimSpace(operation.SourceID), SourceKind: strings.TrimSpace(operation.SourceKind),
		KeyID: strings.TrimSpace(operation.TargetSignatureKeyID), PublicKey: strings.TrimSpace(operation.TargetSignaturePublicKey),
		Fingerprint: strings.ToLower(strings.TrimSpace(operation.TargetSignatureFingerprint)),
	}
	if digest == "" {
		if identity != "" || trust.SourceID != "" || trust.SourceKind != "" || trust.KeyID != "" || trust.PublicKey != "" || trust.Fingerprint != "" || strings.TrimSpace(operation.SourceRiskLabel) != "" || operation.SourceRevision != 0 || operation.SourceRefKind != "" || operation.SourceRefName != "" || operation.SourceResolvedOID != "" {
			return errors.New("plugin operation without a package target has package provenance")
		}
		return nil
	}
	if !marketplace.IsDigest(digest) || !marketplace.IsDigest(identity) {
		return errors.New("plugin operation package digest or identity is invalid")
	}
	if err := marketplace.ValidateSignatureTrust(trust); err != nil {
		return fmt.Errorf("plugin operation signature provenance is invalid: %w", err)
	}
	if expected := PluginPackageIdentity(digest, trust.SourceID, trust.Fingerprint); !strings.EqualFold(identity, expected) {
		return errors.New("plugin operation package identity does not match its signer provenance")
	}
	hasRepositoryProvenance := operation.SourceRevision != 0 || operation.SourceRefKind != "" || operation.SourceRefName != "" || operation.SourceResolvedOID != ""
	if hasRepositoryProvenance && (operation.SourceRevision == 0 || (operation.SourceRefKind != marketplace.GitRefKindBranch && operation.SourceRefKind != marketplace.GitRefKindTag) || strings.TrimSpace(operation.SourceRefName) == "" || !marketplace.IsFullCommitOID(operation.SourceResolvedOID)) {
		return errors.New("plugin operation repository provenance is invalid")
	}
	if candidate == nil {
		if !pluginOperationCompleted(operation) && strings.TrimSpace(operation.Kind) != "disable" {
			return errors.New("pending plugin operation package candidate is unavailable")
		}
		return nil
	}
	candidateIdentity := strings.ToLower(strings.TrimSpace(candidate.Identity))
	if candidateIdentity == "" {
		candidateIdentity = PluginPackageIdentity(candidate.Digest, candidate.SourceID, candidate.SignatureFingerprint)
	}
	if !strings.EqualFold(candidate.Digest, digest) || !strings.EqualFold(candidateIdentity, identity) ||
		strings.TrimSpace(candidate.SourceID) != trust.SourceID || strings.TrimSpace(candidate.SourceKind) != trust.SourceKind ||
		strings.TrimSpace(candidate.SourceRiskLabel) != strings.TrimSpace(operation.SourceRiskLabel) ||
		candidate.SourceRevision != operation.SourceRevision || candidate.SourceRefKind != operation.SourceRefKind || candidate.SourceRefName != operation.SourceRefName || !strings.EqualFold(candidate.SourceResolvedOID, operation.SourceResolvedOID) ||
		strings.TrimSpace(candidate.SignatureKeyID) != trust.KeyID || strings.TrimSpace(candidate.SignaturePublicKey) != trust.PublicKey ||
		!strings.EqualFold(candidate.SignatureFingerprint, trust.Fingerprint) {
		return errors.New("plugin operation package provenance differs from its source-bound candidate")
	}
	return nil
}

func pluginOperationCompleted(operation PluginOperationRow) bool {
	status := strings.TrimSpace(operation.Status)
	return operation.CompletedAt != nil || status == "succeeded" || status == "failed"
}

func preparePluginOperationForWriteTx(tx *gorm.DB, operation *PluginOperationRow) error {
	if operation == nil {
		return errors.New("plugin operation is required")
	}
	if strings.TrimSpace(operation.TargetPackageDigest) == "" {
		return validatePluginOperationPackageProvenance(*operation, nil)
	}
	var candidate PluginPackageRow
	err := tx.Where("identity = ?", strings.ToLower(strings.TrimSpace(operation.TargetPackageIdentity))).First(&candidate).Error
	if err == nil {
		// Repository provenance belongs to the acquisition that authorized this
		// lifecycle operation, not to the content-addressed package row. The same
		// package identity may be acquired again from a later source revision/OID.
		candidate.SourceRevision = operation.SourceRevision
		candidate.SourceRefKind = operation.SourceRefKind
		candidate.SourceRefName = operation.SourceRefName
		candidate.SourceResolvedOID = operation.SourceResolvedOID
		return validatePluginOperationPackageProvenance(*operation, &candidate)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return validatePluginOperationPackageProvenance(*operation, nil)
}

func samePluginOperationPackageProvenance(left, right PluginOperationRow) bool {
	return strings.EqualFold(strings.TrimSpace(left.TargetPackageDigest), strings.TrimSpace(right.TargetPackageDigest)) &&
		strings.EqualFold(strings.TrimSpace(left.TargetPackageIdentity), strings.TrimSpace(right.TargetPackageIdentity)) &&
		strings.TrimSpace(left.SourceID) == strings.TrimSpace(right.SourceID) &&
		strings.TrimSpace(left.SourceKind) == strings.TrimSpace(right.SourceKind) &&
		strings.TrimSpace(left.SourceRiskLabel) == strings.TrimSpace(right.SourceRiskLabel) &&
		left.SourceRevision == right.SourceRevision &&
		strings.TrimSpace(left.SourceRefKind) == strings.TrimSpace(right.SourceRefKind) &&
		strings.TrimSpace(left.SourceRefName) == strings.TrimSpace(right.SourceRefName) &&
		strings.EqualFold(strings.TrimSpace(left.SourceResolvedOID), strings.TrimSpace(right.SourceResolvedOID)) &&
		strings.TrimSpace(left.TargetSignatureKeyID) == strings.TrimSpace(right.TargetSignatureKeyID) &&
		strings.TrimSpace(left.TargetSignaturePublicKey) == strings.TrimSpace(right.TargetSignaturePublicKey) &&
		strings.EqualFold(strings.TrimSpace(left.TargetSignatureFingerprint), strings.TrimSpace(right.TargetSignatureFingerprint))
}

func migratePluginRuntimeProjection(ctx context.Context, db *gorm.DB) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var packages []PluginPackageRow
		if err := tx.Order("identity, digest").Find(&packages).Error; err != nil {
			return err
		}
		legacy := false
		for index := range packages {
			row := packages[index]
			var manifest plugins.Manifest
			if err := json.Unmarshal([]byte(row.ManifestJSON), &manifest); err != nil {
				legacy = true
				break
			}
			projected, artifacts, err := ProjectPluginPackage(row, manifest)
			if err != nil {
				legacy = true
				break
			}
			var storedArtifacts []PluginArtifactRow
			if err := tx.Where("package_identity = ?", strings.ToLower(projected.Identity)).Order("path").Find(&storedArtifacts).Error; err != nil {
				return err
			}
			sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Path < artifacts[j].Path })
			storedArtifacts = durablePluginPackageArtifacts(storedArtifacts, artifacts)
			if row.RuntimeKind != projected.RuntimeKind || row.RuntimeABI != projected.RuntimeABI || row.HostScope != projected.HostScope || row.PolicyKind != projected.PolicyKind || row.EntryPath != projected.EntryPath || row.SignatureKeyID != projected.SignatureKeyID || row.SignatureVerdict != "verified" || row.ResourceBudgetJSON != projected.ResourceBudgetJSON || row.FailurePolicyJSON != projected.FailurePolicyJSON || !samePluginArtifactRows(storedArtifacts, artifacts) {
				legacy = true
				break
			}
		}
		var snapshots []MarketSnapshotRow
		if !legacy {
			if err := tx.Order("id").Find(&snapshots).Error; err != nil {
				return err
			}
			for _, snapshot := range snapshots {
				var entries []plugins.MarketEntry
				if err := json.Unmarshal([]byte(snapshot.EntriesJSON), &entries); err != nil {
					legacy = true
					break
				}
				for _, entry := range entries {
					if strings.TrimSpace(entry.Runtime.Kind) == "" || strings.TrimSpace(entry.Runtime.ABI) == "" || strings.TrimSpace(entry.Runtime.HostScope) == "" || (entry.Runtime.Kind == "wasm-policy" && strings.TrimSpace(entry.Runtime.PolicyKind) == "") || len(entry.Artifacts) == 0 || strings.TrimSpace(entry.SignatureKeyID) == "" || strings.TrimSpace(entry.Provenance) == "" {
						legacy = true
						break
					}
					artifactsJSON, err := json.Marshal(entry.Artifacts)
					if err != nil {
						return err
					}
					result := tx.Model(&MarketEntryRow{}).Where("snapshot_id = ? AND plugin_id = ? AND version = ? AND package_digest = ?", snapshot.ID, entry.ID, entry.Version, strings.ToLower(entry.PackageSHA256)).Updates(map[string]any{
						"runtime_kind": entry.Runtime.Kind, "runtime_abi": entry.Runtime.ABI, "host_scope": entry.Runtime.HostScope, "policy_kind": entry.Runtime.PolicyKind, "artifacts_json": string(artifactsJSON), "signature_key_id": entry.SignatureKeyID, "provenance": entry.Provenance,
					})
					if result.Error != nil {
						return result.Error
					}
					if result.RowsAffected != 1 {
						legacy = true
						break
					}
				}
				if legacy {
					break
				}
			}
		}
		if legacy {
			return rebuildLegacyPluginStateTx(tx)
		}
		packageByIdentity := make(map[string]PluginPackageRow, len(packages))
		for _, row := range packages {
			packageByIdentity[strings.ToLower(row.Identity)] = row
		}
		var installedRows []InstalledPluginRow
		if err := tx.Find(&installedRows).Error; err != nil {
			return err
		}
		for _, installed := range installedRows {
			active, ok := packageByIdentity[strings.ToLower(installed.ActivePackageIdentity)]
			if !ok || (installed.StagedPackageIdentity != "" && packageByIdentity[strings.ToLower(installed.StagedPackageIdentity)].Digest == "") || (installed.RollbackPackageIdentity != "" && packageByIdentity[strings.ToLower(installed.RollbackPackageIdentity)].Digest == "") {
				return rebuildLegacyPluginStateTx(tx)
			}
			if installed.RuntimeKind != active.RuntimeKind || installed.RuntimeABI != active.RuntimeABI || installed.HostScope != active.HostScope {
				if err := tx.Model(&InstalledPluginRow{}).Where("plugin_id = ?", installed.PluginID).Updates(map[string]any{"runtime_kind": active.RuntimeKind, "runtime_abi": active.RuntimeABI, "host_scope": active.HostScope}).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func rebuildLegacyPluginStateTx(tx *gorm.DB) error {
	var instanceIDs []string
	if err := tx.Model(&PluginInstanceRow{}).Pluck("id", &instanceIDs).Error; err != nil {
		return err
	}
	if len(instanceIDs) > 0 {
		if err := tx.Where("resource_kind = ? AND resource_id IN ?", "plugin_instance", instanceIDs).Delete(&QuotaAllocationRow{}).Error; err != nil {
			return err
		}
		if err := tx.Where("resource_kind = ? AND resource_id IN ?", "plugin_instance", instanceIDs).Delete(&ResourceBindingRow{}).Error; err != nil {
			return err
		}
	}
	for _, model := range []any{&PluginInstanceRow{}, &PluginGrantRow{}, &InstalledPluginRow{}, &PluginArtifactRow{}, &PluginPackageAcquisitionRow{}, &PluginPackageStagingRow{}, &PluginCacheGCIntentRow{}, &PluginDigestFenceRow{}, &PluginPackageRow{}, &MarketEntryRow{}, &MarketSnapshotRow{}} {
		if err := tx.Where("1 = 1").Delete(model).Error; err != nil {
			return err
		}
	}
	return tx.Model(&MarketplaceSourceRow{}).Where("current_snapshot_id <> ?", "").Updates(map[string]any{
		"current_snapshot_id": "", "last_result": "rebuild_required", "last_error": "legacy data-only plugin records were removed during runtime contract migration", "updated_at": time.Now().UTC(),
	}).Error
}

func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint") ||
		strings.Contains(message, "duplicate key") ||
		strings.Contains(message, "duplicate entry") ||
		strings.Contains(message, "sqlstate 23505")
}

func pluginStorageDigest(parts ...string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(parts, "\x00"))))
}

func normalizeLegacyControlPlanePluginTargets(ctx context.Context, db *gorm.DB, defaultTargetID string) error {
	defaultTargetID = strings.TrimSpace(defaultTargetID)
	if defaultTargetID == "" {
		return errors.New("canonical local plugin target is unavailable")
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var installed []InstalledPluginRow
		if err := tx.Order("plugin_id").Find(&installed).Error; err != nil {
			return err
		}
		for index := range installed {
			if err := normalizeLegacyInstalledPluginTargetsTx(tx, &installed[index], defaultTargetID); err != nil {
				return err
			}
		}
		return nil
	})
}

func normalizeLegacyInstalledPluginTargetsTx(tx *gorm.DB, installed *InstalledPluginRow, defaultTargetID string) error {
	pendingOperationID, pendingKind := installed.PendingOperationID, installed.PendingKind
	var instances []PluginInstanceRow
	if err := tx.Where("plugin_id = ?", installed.PluginID).Order("id").Find(&instances).Error; err != nil {
		return err
	}
	if len(instances) == 0 {
		return nil
	}
	var authorityStatuses []PluginAgentRuntimeStatusRow
	if err := tx.Where("plugin_id = ? AND authority_slot IN ?", installed.PluginID, []string{"active", "pending"}).Order("instance_id, operation_id, agent_id").Find(&authorityStatuses).Error; err != nil {
		return err
	}
	activeStatusInstances := make(map[string]struct{})
	pendingStatusOperations := make(map[string]map[string]struct{})
	for _, status := range authorityStatuses {
		switch status.AuthoritySlot {
		case "active":
			activeStatusInstances[status.InstanceID] = struct{}{}
		case "pending":
			if pendingStatusOperations[status.InstanceID] == nil {
				pendingStatusOperations[status.InstanceID] = make(map[string]struct{})
			}
			pendingStatusOperations[status.InstanceID][status.OperationID] = struct{}{}
		}
	}
	canonicalTargetJSON, err := json.Marshal([]string{defaultTargetID})
	if err != nil {
		return err
	}
	var activeManifest, pendingManifest plugins.Manifest
	var activeManifestErr, pendingManifestErr error
	activeLoaded, pendingLoaded := false, false
	loadActive := func() (plugins.Manifest, error) {
		if !activeLoaded {
			activeManifest, activeManifestErr = loadAuthoritativePluginManifestTx(tx, installed.PluginID, installed.ActivePackageIdentity, installed.ActivePackageDigest)
			activeLoaded = true
		}
		return activeManifest, activeManifestErr
	}
	loadPending := func() (plugins.Manifest, error) {
		if strings.TrimSpace(installed.StagedPackageIdentity) == "" || strings.TrimSpace(installed.StagedPackageDigest) == "" {
			return loadActive()
		}
		if !pendingLoaded {
			pendingManifest, pendingManifestErr = loadAuthoritativePluginManifestTx(tx, installed.PluginID, installed.StagedPackageIdentity, installed.StagedPackageDigest)
			pendingLoaded = true
		}
		return pendingManifest, pendingManifestErr
	}

	cancelInstalledPending := false
	retirePendingOperations := make(map[string]struct{})
	cancelPendingOperations := make(map[string]struct{})
	clearPendingInstances := make(map[string]struct{})
	retireActiveStatusInstances := make(map[string]struct{})
	retirePendingStatusInstances := make(map[string]struct{})
	preservedPendingTargets := make(map[string]string)
	for index := range instances {
		activeTargetJSON := instances[index].TargetJSON
		activeTargets, err := pluginInstanceTargets(activeTargetJSON, defaultTargetID)
		if err != nil {
			return fmt.Errorf("plugin instance %s active targets: %w", instances[index].ID, err)
		}
		activeNeedsAuthority := !canonicalPluginLocalTargets(activeTargets, defaultTargetID)
		_, hasActiveStatus := activeStatusInstances[instances[index].ID]
		if activeNeedsAuthority || hasActiveStatus {
			manifest, err := loadActive()
			if err != nil {
				return fmt.Errorf("plugin %s active target authority: %w", installed.PluginID, err)
			}
			if !pluginsdk.RuntimeDeclaresHostScope(manifest.Runtime, pluginsdk.HostScopeAgent) {
				if activeNeedsAuthority {
					instances[index].TargetJSON = string(canonicalTargetJSON)
				}
				retireActiveStatusInstances[instances[index].ID] = struct{}{}
			}
		}

		hasInstalledPending := installed.PendingOperationID != ""
		hasInstancePending := instances[index].PendingOperationID != "" || strings.TrimSpace(instances[index].PendingTargetJSON) != "" || strings.TrimSpace(instances[index].PendingResourceGroupID) != ""
		statusOperations := pendingStatusOperations[instances[index].ID]
		if !hasInstalledPending && !hasInstancePending && len(statusOperations) == 0 {
			continue
		}
		pendingTargetJSON := instances[index].PendingTargetJSON
		if strings.TrimSpace(pendingTargetJSON) == "" {
			// The pending operation was created against the pre-normalization
			// active scope, not the canonical replacement above.
			pendingTargetJSON = activeTargetJSON
		}
		pendingTargets, err := pluginInstanceTargets(pendingTargetJSON, defaultTargetID)
		if err != nil {
			return fmt.Errorf("plugin instance %s pending targets: %w", instances[index].ID, err)
		}
		pendingTargetsNoncanonical := !canonicalPluginLocalTargets(pendingTargets, defaultTargetID)
		if !pendingTargetsNoncanonical && len(statusOperations) == 0 {
			continue
		}
		manifest, err := loadPending()
		if err != nil {
			return fmt.Errorf("plugin %s pending target authority: %w", installed.PluginID, err)
		}
		if pluginsdk.RuntimeDeclaresHostScope(manifest.Runtime, pluginsdk.HostScopeAgent) {
			if pendingTargetsNoncanonical && activeNeedsAuthority && strings.TrimSpace(instances[index].PendingTargetJSON) == "" && instances[index].TargetJSON != activeTargetJSON {
				instances[index].PendingTargetJSON = activeTargetJSON
				preservedPendingTargets[instances[index].ID] = activeTargetJSON
			}
			continue
		}
		for operationID := range statusOperations {
			if operationID != "" {
				retirePendingOperations[operationID] = struct{}{}
			}
		}
		if len(statusOperations) == 0 {
			retirePendingStatusInstances[instances[index].ID] = struct{}{}
		}
		if pendingTargetsNoncanonical {
			clearPendingInstances[instances[index].ID] = struct{}{}
			if hasInstalledPending {
				cancelInstalledPending = true
			}
			if instances[index].PendingOperationID != "" {
				retirePendingOperations[instances[index].PendingOperationID] = struct{}{}
				cancelPendingOperations[instances[index].PendingOperationID] = struct{}{}
			}
		}
	}

	if cancelInstalledPending {
		retirePendingOperations[installed.PendingOperationID] = struct{}{}
		cancelPendingOperations[installed.PendingOperationID] = struct{}{}
		clearLegacyInstalledPluginPending(installed)
		if err := tx.Model(&InstalledPluginRow{}).Where("plugin_id = ?", installed.PluginID).Updates(map[string]any{
			"current_lifecycle": installed.CurrentLifecycle, "pending_operation_id": "", "pending_kind": "", "pending_target_digest": "", "pending_target_identity": "", "pending_revision": 0,
			"staged_package_digest": "", "staged_package_identity": "", "staged_source_id": "", "staged_source_kind": "", "staged_source_risk_label": "", "staged_source_revision": 0,
			"staged_source_ref_kind": "", "staged_source_ref_name": "", "staged_source_resolved_oid": "", "staged_signature_key_id": "", "staged_signature_public_key": "", "staged_signature_fingerprint": "", "pending_grants_json": "[]",
		}).Error; err != nil {
			return err
		}
	}

	for index := range instances {
		_, normalizeActive := retireActiveStatusInstances[instances[index].ID]
		_, clearStale := cancelPendingOperations[instances[index].PendingOperationID]
		_, clearPending := clearPendingInstances[instances[index].ID]
		preservedPendingTarget, preservePending := preservedPendingTargets[instances[index].ID]
		clearInstalled := cancelInstalledPending && (pendingKind == "enable" || pendingKind == "disable")
		if cancelInstalledPending && pendingOperationID != "" && instances[index].PendingOperationID == pendingOperationID {
			clearInstalled = true
		}
		if clearPending || clearStale || clearInstalled {
			clearLegacyPluginInstancePending(&instances[index])
		}
		updates := map[string]any{}
		if normalizeActive {
			updates["target_json"] = instances[index].TargetJSON
		}
		if preservePending && !clearPending && !clearStale && !clearInstalled {
			updates["pending_target_json"] = preservedPendingTarget
		}
		if clearPending || clearStale || clearInstalled {
			updates["current_state"] = instances[index].CurrentState
			updates["pending_config_json"] = ""
			updates["pending_version"] = 0
			updates["pending_operation_id"] = ""
			updates["pending_resource_group_id"] = ""
			updates["pending_target_json"] = ""
			updates["pending_policy_chains_json"] = "[]"
			updates["pending_bindings_json"] = "[]"
			updates["pending_secret_handles_json"] = "[]"
		}
		if len(updates) > 0 {
			if err := tx.Model(&PluginInstanceRow{}).Where("id = ?", instances[index].ID).Updates(updates).Error; err != nil {
				return err
			}
		}
	}
	for instanceID := range retireActiveStatusInstances {
		if err := tx.Model(&PluginAgentRuntimeStatusRow{}).
			Where("plugin_id = ? AND instance_id = ? AND authority_slot = ?", installed.PluginID, instanceID, "active").
			Update("authority_slot", "retired").Error; err != nil {
			return err
		}
	}
	for instanceID := range retirePendingStatusInstances {
		if err := tx.Model(&PluginAgentRuntimeStatusRow{}).
			Where("plugin_id = ? AND instance_id = ? AND authority_slot = ?", installed.PluginID, instanceID, "pending").
			Update("authority_slot", "retired").Error; err != nil {
			return err
		}
	}
	for operationID := range retirePendingOperations {
		if err := tx.Model(&PluginAgentRuntimeStatusRow{}).
			Where("plugin_id = ? AND operation_id = ? AND authority_slot = ?", installed.PluginID, operationID, "pending").
			Update("authority_slot", "retired").Error; err != nil {
			return err
		}
	}
	return nil
}

func canonicalPluginLocalTargets(targets []string, defaultTargetID string) bool {
	return len(targets) == 1 && targets[0] == defaultTargetID
}

func loadAuthoritativePluginManifestTx(tx *gorm.DB, pluginID, identity, digest string) (plugins.Manifest, error) {
	identity, digest = strings.TrimSpace(identity), strings.ToLower(strings.TrimSpace(digest))
	if identity == "" || digest == "" {
		return plugins.Manifest{}, errors.New("authoritative package identity is incomplete")
	}
	var packageRow PluginPackageRow
	if err := tx.Where("identity = ? AND digest = ?", identity, digest).First(&packageRow).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return plugins.Manifest{}, errors.New("authoritative package is unavailable")
		}
		return plugins.Manifest{}, err
	}
	var manifest plugins.Manifest
	if err := json.Unmarshal([]byte(packageRow.ManifestJSON), &manifest); err != nil {
		return plugins.Manifest{}, fmt.Errorf("authoritative manifest is invalid: %w", err)
	}
	if manifest.ID != pluginID || manifest.ID != packageRow.PluginID || manifest.Version != packageRow.Version || manifest.Runtime.Kind != packageRow.RuntimeKind || manifest.Runtime.ABI != packageRow.RuntimeABI || manifest.Runtime.HostScope != packageRow.HostScope {
		return plugins.Manifest{}, errors.New("authoritative manifest differs from its verified package projection")
	}
	if packageRow.SignatureVerdict != "verified" || strings.TrimSpace(packageRow.SignatureKeyID) == "" || !ValidPluginGenerationDigest(packageRow.SignatureFingerprint) {
		return plugins.Manifest{}, errors.New("authoritative package signature projection is not verified")
	}
	if manifest.Runtime.HostScope != strings.TrimSpace(manifest.Runtime.HostScope) || (manifest.Runtime.HostScope != pluginsdk.HostScopeAgent && manifest.Runtime.HostScope != pluginsdk.HostScopeControlPlane) {
		return plugins.Manifest{}, errors.New("authoritative manifest primary host scope is invalid")
	}
	seenScopes := make(map[string]struct{}, len(manifest.Runtime.HostScopes))
	for _, scope := range manifest.Runtime.HostScopes {
		if scope != strings.TrimSpace(scope) || (scope != pluginsdk.HostScopeAgent && scope != pluginsdk.HostScopeControlPlane) {
			return plugins.Manifest{}, errors.New("authoritative manifest host scopes are invalid")
		}
		if _, duplicate := seenScopes[scope]; duplicate {
			return plugins.Manifest{}, errors.New("authoritative manifest host scopes are duplicated")
		}
		seenScopes[scope] = struct{}{}
	}
	return manifest, nil
}

func clearLegacyInstalledPluginPending(installed *InstalledPluginRow) {
	installed.PendingOperationID, installed.PendingKind = "", ""
	installed.PendingTargetDigest, installed.PendingTargetIdentity, installed.PendingRevision = "", "", 0
	installed.StagedPackageIdentity, installed.StagedPackageDigest = "", ""
	installed.StagedSourceID, installed.StagedSourceKind, installed.StagedSourceRiskLabel = "", "", ""
	installed.StagedSourceRevision, installed.StagedSourceRefKind, installed.StagedSourceRefName, installed.StagedSourceResolvedOID = 0, "", "", ""
	installed.StagedSignatureKeyID, installed.StagedSignaturePublicKey, installed.StagedSignatureFingerprint = "", "", ""
	installed.PendingGrantsJSON = "[]"
	if installed.DesiredLifecycle == "disabled" {
		installed.CurrentLifecycle = "disabled"
	} else {
		installed.CurrentLifecycle = "active"
	}
}

func clearLegacyPluginInstancePending(instance *PluginInstanceRow) {
	instance.PendingConfigJSON, instance.PendingResourceGroupID, instance.PendingTargetJSON = "", "", ""
	instance.PendingPolicyChainsJSON, instance.PendingBindingsJSON, instance.PendingSecretHandlesJSON = "[]", "[]", "[]"
	instance.PendingVersion, instance.PendingOperationID = 0, ""
	if instance.DesiredEnabled {
		instance.CurrentState = "active"
	} else {
		instance.CurrentState = "disabled"
	}
}

func backfillPluginOwnershipAndAcquisitions(ctx context.Context, db *gorm.DB, defaultTargetID string) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&PluginInstanceRow{}).Where("state_version = 0").Update("state_version", 1).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		if err := backfillPluginInstanceOwnershipTx(tx, now, defaultTargetID); err != nil {
			return err
		}
		var grants []PluginGrantRow
		if err := tx.Find(&grants).Error; err != nil {
			return err
		}
		for _, grant := range grants {
			key := pluginGrantKey(grant)
			if err := tx.Model(&PluginGrantRow{}).Where("id = ?", grant.ID).Update("grant_key", key).Error; err != nil {
				return err
			}
		}
		if err := backfillPluginLifecycleProvenanceTx(tx); err != nil {
			return err
		}
		var sources []MarketplaceSourceRow
		if err := tx.Where("current_snapshot_id <> ?", "").Find(&sources).Error; err != nil {
			return err
		}
		bySnapshot := make(map[string]string, len(sources))
		sourceBySnapshot := make(map[string]MarketplaceSourceRow, len(sources))
		trustBySnapshot := make(map[string]marketplace.SignatureTrust, len(sources))
		trustBySource := make(map[string]marketplace.SignatureTrust, len(sources))
		currentIDs := make([]string, 0, len(sources))
		for _, source := range sources {
			bySnapshot[source.CurrentSnapshotID] = source.ID
			sourceBySnapshot[source.CurrentSnapshotID] = source
			trust, err := marketplaceSourceFromRow(source).SignatureTrust()
			if err != nil {
				return fmt.Errorf("current marketplace source %s signature trust: %w", source.ID, err)
			}
			trustBySnapshot[source.CurrentSnapshotID] = trust
			trustBySource[source.ID] = trust
			currentIDs = append(currentIDs, source.CurrentSnapshotID)
		}
		var entries []MarketEntryRow
		snapshotByID := make(map[string]MarketSnapshotRow, len(currentIDs))
		if len(currentIDs) > 0 {
			var snapshots []MarketSnapshotRow
			if err := tx.Where("id IN ?", currentIDs).Find(&snapshots).Error; err != nil {
				return err
			}
			for _, snapshot := range snapshots {
				snapshotByID[snapshot.ID] = snapshot
			}
			if err := tx.Where("snapshot_id IN ?", currentIDs).Find(&entries).Error; err != nil {
				return err
			}
		}
		for _, entry := range entries {
			trust, ok := trustBySnapshot[entry.SnapshotID]
			if !ok || entry.SignatureKeyID != trust.KeyID {
				return fmt.Errorf("marketplace entry %s signer differs from current source trust", entry.ID)
			}
		}
		var existingAcquisitions []PluginPackageAcquisitionRow
		if err := tx.Find(&existingAcquisitions).Error; err != nil {
			return err
		}
		missingSourceIDs := make([]string, 0)
		seenMissingSources := make(map[string]struct{})
		for _, acquisition := range existingAcquisitions {
			if _, ok := trustBySource[acquisition.SourceID]; ok {
				continue
			}
			if _, seen := seenMissingSources[acquisition.SourceID]; !seen {
				seenMissingSources[acquisition.SourceID] = struct{}{}
				missingSourceIDs = append(missingSourceIDs, acquisition.SourceID)
			}
		}
		if len(missingSourceIDs) > 0 {
			var acquisitionSources []MarketplaceSourceRow
			if err := tx.Where("id IN ?", missingSourceIDs).Find(&acquisitionSources).Error; err != nil {
				return err
			}
			for _, source := range acquisitionSources {
				trust, err := marketplaceSourceFromRow(source).SignatureTrust()
				if err != nil {
					return fmt.Errorf("marketplace source %s acquisition signer trust: %w", source.ID, err)
				}
				trustBySource[source.ID] = trust
			}
		}
		for _, acquisition := range existingAcquisitions {
			trust, ok := trustBySource[acquisition.SourceID]
			if !ok {
				return fmt.Errorf("plugin package acquisition %s source trust is unavailable", acquisition.Digest)
			}
			if err := validatePluginAcquisitionTrust(acquisition, trust); err != nil {
				return err
			}
		}
		if tx.Migrator().HasColumn("plugin_package_acquisitions", "operation_id") {
			var legacy []struct{ SourceID, Digest, OperationID, Status string }
			if err := tx.Table("plugin_package_acquisitions").Select("source_id, digest, operation_id, status").Where("status = ? AND operation_id <> ?", "staging", "").Order("digest, source_id, operation_id").Scan(&legacy).Error; err != nil {
				return err
			}
			sort.Slice(legacy, func(i, j int) bool {
				leftDigest, rightDigest := strings.ToLower(legacy[i].Digest), strings.ToLower(legacy[j].Digest)
				if leftDigest != rightDigest {
					return leftDigest < rightDigest
				}
				if legacy[i].SourceID != legacy[j].SourceID {
					return legacy[i].SourceID < legacy[j].SourceID
				}
				return legacy[i].OperationID < legacy[j].OperationID
			})
			for _, row := range legacy {
				var count int64
				if err := tx.Model(&MarketplaceRefreshOperationRow{}).Where("id = ? AND source_id = ? AND status = ? AND lease_expires_at > ?", row.OperationID, row.SourceID, "running", now).Count(&count).Error; err != nil {
					return err
				}
				if count == 1 {
					staging := PluginPackageStagingRow{SourceID: row.SourceID, OperationID: row.OperationID, Digest: strings.ToLower(row.Digest), UpdatedAt: now}
					if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&staging).Error; err != nil {
						return err
					}
				} else if err := schedulePackageGCTx(tx, row.SourceID, row.Digest, "", now); err != nil {
					return err
				}
			}
		}
		if err := tx.Where("1 = 1").Delete(&PluginPackageAcquisitionRow{}).Error; err != nil {
			return err
		}
		for _, entry := range entries {
			sourceID := bySnapshot[entry.SnapshotID]
			if sourceID == "" {
				continue
			}
			snapshot := snapshotByID[entry.SnapshotID]
			source := sourceBySnapshot[entry.SnapshotID]
			row := PluginPackageAcquisitionRow{SourceID: sourceID, Digest: strings.ToLower(entry.PackageDigest), SnapshotID: entry.SnapshotID, SourceRevision: snapshot.SourceRevision, RefKind: snapshot.RefKind, RefName: snapshot.RefName, ResolvedOID: snapshot.Commit, Status: "catalog", UpdatedAt: now}
			if snapshot.SourceRevision != source.ConfigRevision || snapshot.RefKind != source.RefKind || snapshot.RefName != source.RefName || !strings.EqualFold(snapshot.Commit, source.CurrentResolvedOID) {
				// Preserve startup compatibility for a legacy source while keeping
				// its catalog unusable until an authenticated refresh establishes
				// the complete repository provenance tuple.
				row.SourceRevision, row.RefKind, row.RefName, row.ResolvedOID = 0, "", "", ""
			}
			trust := trustBySnapshot[entry.SnapshotID]
			row.SourceKind, row.SignatureKeyID = trust.SourceKind, trust.KeyID
			row.SignaturePublicKey, row.SignatureFingerprint = trust.PublicKey, trust.Fingerprint
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "source_id"}, {Name: "digest"}}, DoNothing: true}).Create(&row).Error; err != nil {
				return err
			}
		}
		return recomputeCountQuotaUsageTx(tx, now)
	})
}

func pluginGrantKey(row PluginGrantRow) string {
	return pluginStorageDigest(row.PluginID, strings.ToLower(row.PackageDigest), row.Permission, row.ResourceSelector)
}

func normalizePluginGrantRows(rows []PluginGrantRow) {
	for index := range rows {
		rows[index].GrantKey = pluginGrantKey(rows[index])
	}
}

func validatePluginAcquisitionTrust(row PluginPackageAcquisitionRow, trust marketplace.SignatureTrust) error {
	if row.SourceID != trust.SourceID {
		return fmt.Errorf("plugin package acquisition %s source differs from signer trust", row.Digest)
	}
	checks := []struct {
		name, current, expected string
		equal                   func(string, string) bool
	}{
		{"source kind", row.SourceKind, trust.SourceKind, func(left, right string) bool { return left == right }},
		{"signature key", row.SignatureKeyID, trust.KeyID, func(left, right string) bool { return left == right }},
		{"signature public key", row.SignaturePublicKey, trust.PublicKey, func(left, right string) bool { return left == right }},
		{"signature fingerprint", row.SignatureFingerprint, trust.Fingerprint, strings.EqualFold},
	}
	for _, check := range checks {
		if check.current != "" && !check.equal(check.current, check.expected) {
			return fmt.Errorf("plugin package acquisition %s %s differs from current source trust", row.Digest, check.name)
		}
	}
	return nil
}

func backfillPluginInstanceOwnershipTx(tx *gorm.DB, now time.Time, defaultTargetID string) error {
	var instances []PluginInstanceRow
	if err := tx.Order("id").Find(&instances).Error; err != nil {
		return err
	}
	for _, instance := range instances {
		groupID := strings.TrimSpace(instance.ResourceGroupID)
		if groupID == "" {
			return fmt.Errorf("plugin instance %s has no resource group", instance.ID)
		}
		var group ResourceGroupRow
		if err := tx.Where("id = ?", groupID).First(&group).Error; err != nil {
			return fmt.Errorf("plugin instance %s resource group %s is unavailable", instance.ID, groupID)
		}
		targets, err := pluginInstanceTargets(instance.TargetJSON, defaultTargetID)
		if err != nil {
			return fmt.Errorf("plugin instance %s targets: %w", instance.ID, err)
		}
		for _, target := range targets {
			var targetBinding ResourceBindingRow
			err := tx.Where("resource_kind = ? AND resource_id = ?", "agent", target).First(&targetBinding).Error
			if errors.Is(err, gorm.ErrRecordNotFound) && groupID == "default" {
				if target == defaultTargetID {
					continue
				}
				var agent AgentRow
				if tx.Where("id = ?", target).First(&agent).Error == nil {
					continue
				}
			}
			if err != nil || targetBinding.ResourceGroupID != groupID {
				return fmt.Errorf("plugin instance %s target %s is outside resource group %s", instance.ID, target, groupID)
			}
		}
		var existing ResourceBindingRow
		err = tx.Where("resource_kind = ? AND resource_id = ?", "plugin_instance", instance.ID).First(&existing).Error
		if err == nil && existing.ResourceGroupID != groupID {
			return fmt.Errorf("plugin instance %s binding conflicts with resource group %s", instance.ID, groupID)
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		binding := ResourceBindingRow{ID: pluginStorageID("binding"), ResourceKind: "plugin_instance", ResourceID: instance.ID, ResourceGroupID: groupID, UpdatedAt: now}
		if err == nil {
			binding.ID = existing.ID
		}
		if len(targets) == 1 {
			binding.ParentResourceKind, binding.ParentResourceID = "agent", targets[0]
		}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "resource_kind"}, {Name: "resource_id"}}, DoUpdates: clause.AssignmentColumns([]string{"resource_group_id", "parent_resource_kind", "parent_resource_id", "updated_at"})}).Create(&binding).Error; err != nil {
			return err
		}
		scope := quotaScope{SubjectKind: "resource_group", SubjectID: groupID, ResourceGroupID: groupID}
		allocation := QuotaAllocationRow{ID: quotaAllocationID("plugin_instance", instance.ID, "application_count", scope), ResourceKind: "plugin_instance", ResourceID: instance.ID, Metric: "application_count", SubjectKind: scope.SubjectKind, SubjectID: scope.SubjectID, ResourceGroupID: scope.ResourceGroupID, Amount: 1, CreatedAt: now}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&allocation).Error; err != nil {
			return err
		}
	}
	var policies []QuotaPolicyRow
	if err := tx.Where("metric = ? AND resource_group_id <> ?", "application_count", "").Find(&policies).Error; err != nil {
		return err
	}
	for _, policy := range policies {
		if policy.SubjectKind != "resource_group" || policy.SubjectID != policy.ResourceGroupID {
			continue
		}
		var current int64
		if err := tx.Model(&QuotaAllocationRow{}).Where("metric = ? AND subject_kind = ? AND subject_id = ? AND resource_group_id = ?", "application_count", "resource_group", policy.SubjectID, policy.ResourceGroupID).Select("COALESCE(SUM(amount), 0)").Scan(&current).Error; err != nil {
			return err
		}
		if current > policy.Limit {
			return fmt.Errorf("plugin instance application_count %d exceeds resource group %s limit %d", current, policy.ResourceGroupID, policy.Limit)
		}
	}
	return nil
}

func backfillPluginLifecycleProvenanceTx(tx *gorm.DB) error {
	var installed []InstalledPluginRow
	if err := tx.Find(&installed).Error; err != nil {
		return err
	}
	for index := range installed {
		changed := false
		for _, slot := range []struct {
			digest                        string
			identity                      *string
			id, kind, risk                *string
			keyID, publicKey, fingerprint *string
		}{
			{installed[index].ActivePackageDigest, &installed[index].ActivePackageIdentity, &installed[index].ActiveSourceID, &installed[index].ActiveSourceKind, &installed[index].ActiveSourceRiskLabel, &installed[index].ActiveSignatureKeyID, &installed[index].ActiveSignaturePublicKey, &installed[index].ActiveSignatureFingerprint},
			{installed[index].StagedPackageDigest, &installed[index].StagedPackageIdentity, &installed[index].StagedSourceID, &installed[index].StagedSourceKind, &installed[index].StagedSourceRiskLabel, &installed[index].StagedSignatureKeyID, &installed[index].StagedSignaturePublicKey, &installed[index].StagedSignatureFingerprint},
			{installed[index].RollbackPackageDigest, &installed[index].RollbackPackageIdentity, &installed[index].RollbackSourceID, &installed[index].RollbackSourceKind, &installed[index].RollbackSourceRiskLabel, &installed[index].RollbackSignatureKeyID, &installed[index].RollbackSignaturePublicKey, &installed[index].RollbackSignatureFingerprint},
		} {
			if slot.digest == "" {
				continue
			}
			operation := PluginOperationRow{
				ID: "lifecycle-" + installed[index].PluginID, TargetPackageDigest: slot.digest, TargetPackageIdentity: *slot.identity,
				SourceID: *slot.id, SourceKind: *slot.kind, SourceRiskLabel: *slot.risk,
				TargetSignatureKeyID: *slot.keyID, TargetSignaturePublicKey: *slot.publicKey, TargetSignatureFingerprint: *slot.fingerprint,
				Status: "running",
			}
			candidate, err := resolveInstalledLifecyclePackageTx(tx, operation)
			if err != nil {
				return fmt.Errorf("installed plugin %s lifecycle package provenance: %w", installed[index].PluginID, err)
			}
			if err := BindPluginOperationPackage(&operation, candidate); err != nil {
				return fmt.Errorf("installed plugin %s lifecycle package provenance: %w", installed[index].PluginID, err)
			}
			*slot.identity = operation.TargetPackageIdentity
			*slot.id, *slot.kind, *slot.risk = operation.SourceID, operation.SourceKind, operation.SourceRiskLabel
			*slot.keyID, *slot.publicKey, *slot.fingerprint = operation.TargetSignatureKeyID, operation.TargetSignaturePublicKey, operation.TargetSignatureFingerprint
			changed = true
		}
		if changed {
			if err := tx.Model(&InstalledPluginRow{}).Where("plugin_id = ?", installed[index].PluginID).Updates(map[string]any{
				"active_package_identity": installed[index].ActivePackageIdentity,
				"active_source_id":        installed[index].ActiveSourceID, "active_source_kind": installed[index].ActiveSourceKind, "active_source_risk_label": installed[index].ActiveSourceRiskLabel,
				"active_signature_key_id": installed[index].ActiveSignatureKeyID, "active_signature_public_key": installed[index].ActiveSignaturePublicKey, "active_signature_fingerprint": installed[index].ActiveSignatureFingerprint,
				"staged_package_identity": installed[index].StagedPackageIdentity,
				"staged_source_id":        installed[index].StagedSourceID, "staged_source_kind": installed[index].StagedSourceKind, "staged_source_risk_label": installed[index].StagedSourceRiskLabel,
				"staged_signature_key_id": installed[index].StagedSignatureKeyID, "staged_signature_public_key": installed[index].StagedSignaturePublicKey, "staged_signature_fingerprint": installed[index].StagedSignatureFingerprint,
				"rollback_package_identity": installed[index].RollbackPackageIdentity,
				"rollback_source_id":        installed[index].RollbackSourceID, "rollback_source_kind": installed[index].RollbackSourceKind, "rollback_source_risk_label": installed[index].RollbackSourceRiskLabel,
				"rollback_signature_key_id": installed[index].RollbackSignatureKeyID, "rollback_signature_public_key": installed[index].RollbackSignaturePublicKey, "rollback_signature_fingerprint": installed[index].RollbackSignatureFingerprint,
			}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func resolveInstalledLifecyclePackageTx(tx *gorm.DB, operation PluginOperationRow) (PluginPackageRow, error) {
	digest := strings.ToLower(strings.TrimSpace(operation.TargetPackageDigest))
	identity := strings.ToLower(strings.TrimSpace(operation.TargetPackageIdentity))
	if identity != "" {
		var candidate PluginPackageRow
		if err := tx.Where("identity = ? AND digest = ?", identity, digest).First(&candidate).Error; err != nil {
			return PluginPackageRow{}, err
		}
		return candidate, nil
	}
	var candidates []PluginPackageRow
	if err := tx.Where("digest = ?", digest).Find(&candidates).Error; err != nil {
		return PluginPackageRow{}, err
	}
	byIdentity := make(map[string]PluginPackageRow, len(candidates))
	byDigest := map[string][]PluginPackageRow{digest: candidates}
	for _, candidate := range candidates {
		byIdentity[strings.ToLower(strings.TrimSpace(candidate.Identity))] = candidate
	}
	candidate, err := resolvePluginOperationMigrationCandidate(operation, byIdentity, byDigest)
	if err != nil {
		return PluginPackageRow{}, err
	}
	if candidate == nil {
		return PluginPackageRow{}, errors.New("lifecycle package candidate is unavailable")
	}
	return *candidate, nil
}

func pluginInstanceTargets(raw, defaultTargetID string) ([]string, error) {
	var targets []string
	if strings.TrimSpace(raw) != "" && strings.TrimSpace(raw) != "null" {
		if err := json.Unmarshal([]byte(raw), &targets); err != nil {
			return nil, err
		}
	}
	if len(targets) == 0 {
		defaultTargetID = strings.TrimSpace(defaultTargetID)
		if defaultTargetID == "" {
			return nil, errors.New("default target is unavailable")
		}
		targets = []string{defaultTargetID}
	}
	for index := range targets {
		targets[index] = strings.TrimSpace(targets[index])
		if targets[index] == "" {
			return nil, errors.New("empty target")
		}
	}
	sort.Strings(targets)
	return targets, nil
}

func (s *GormStore) loadAgentPluginPolicies(ctx context.Context, agentID string) ([]PluginPolicy, error) {
	if !s.transactionScoped {
		var policies []PluginPolicy
		err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			scoped := s.transactionView(tx)
			var err error
			policies, err = scoped.loadAgentPluginPolicies(ctx, agentID)
			return err
		}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		return policies, err
	}
	agentID = s.resolveAgentID(agentID)
	var instances []PluginInstanceRow
	if err := s.pluginPolicyGraphQuery(ctx).Order("id").Find(&instances).Error; err != nil {
		return nil, err
	}
	if err := s.validatePolicyChainMembershipTopology(ctx, instances); err != nil {
		return nil, err
	}
	var catalogRevision PluginPolicyAgentRevisionRow
	if err := s.pluginPolicyGraphQuery(ctx).Where("agent_id = ?", agentID).First(&catalogRevision).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	type chainProjection struct {
		targetsKey string
		stages     []PolicyStage
		kinds      map[string]string
	}
	chains := make(map[string]*chainProjection)
	for _, instance := range instances {
		if err := ValidatePluginPolicyIdentity(instance.ID); err != nil {
			return nil, fmt.Errorf("plugin instance identity %q: %w", instance.ID, err)
		}
		memberships, err := CanonicalPluginPolicyChains(instance.PolicyChainsJSON)
		if err != nil {
			return nil, fmt.Errorf("plugin instance %s policy chains: %w", instance.ID, err)
		}
		// ConfigJSON/ConfigVersion are the committed active values. Pending fields
		// and lifecycle presentation state must not hide the old generation while
		// configure, upgrade, rollback, or disable is still awaiting Agent apply.
		if instance.ConfigVersion == 0 || len(memberships) == 0 {
			continue
		}
		targets, err := pluginInstanceTargets(instance.TargetJSON, s.LocalAgentID())
		if err != nil {
			return nil, fmt.Errorf("plugin instance %s targets: %w", instance.ID, err)
		}
		if !containsPluginTarget(targets, agentID) {
			continue
		}

		var installed InstalledPluginRow
		if err := s.pluginPolicyGraphQuery(ctx).Where("plugin_id = ?", instance.PluginID).First(&installed).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			// Cleanup may intentionally retain disabled instance/config rows after
			// uninstall. Installed state is lifecycle authority, so these rows do
			// not publish a policy until the plugin is installed again.
			continue
		} else if err != nil {
			return nil, fmt.Errorf("plugin instance %s installed state: %w", instance.ID, err)
		}
		if !installedProjectsActivePolicy(installed) || installed.RuntimeKind != "wasm-policy" || installed.HostScope != "agent" {
			continue
		}
		var packageRow PluginPackageRow
		if err := s.pluginPolicyGraphQuery(ctx).
			Where("identity = ? AND digest = ?", installed.ActivePackageIdentity, installed.ActivePackageDigest).
			First(&packageRow).Error; err != nil {
			return nil, fmt.Errorf("plugin instance %s active package: %w", instance.ID, err)
		}
		if err := validateActivePolicyPackage(installed, packageRow); err != nil {
			return nil, fmt.Errorf("plugin instance %s: %w", instance.ID, err)
		}
		var manifest plugins.Manifest
		if err := json.Unmarshal([]byte(packageRow.ManifestJSON), &manifest); err != nil {
			return nil, fmt.Errorf("plugin instance %s manifest: %w", instance.ID, err)
		}
		projected, expectedArtifacts, err := ProjectPluginPackage(packageRow, manifest)
		if err != nil {
			return nil, fmt.Errorf("plugin instance %s projection: %w", instance.ID, err)
		}
		var artifacts []PluginArtifactRow
		if err := s.db.WithContext(ctx).Where("package_identity = ?", packageRow.Identity).Order("path").Find(&artifacts).Error; err != nil {
			return nil, err
		}
		sort.Slice(expectedArtifacts, func(i, j int) bool { return expectedArtifacts[i].Path < expectedArtifacts[j].Path })
		if projected.RuntimeKind != packageRow.RuntimeKind || projected.RuntimeABI != packageRow.RuntimeABI || projected.HostScope != packageRow.HostScope || projected.PolicyKind != packageRow.PolicyKind || projected.EntryPath != packageRow.EntryPath || projected.SignatureKeyID != packageRow.SignatureKeyID || projected.ResourceBudgetJSON != packageRow.ResourceBudgetJSON || projected.FailurePolicyJSON != packageRow.FailurePolicyJSON || !samePluginArtifactRows(artifacts, expectedArtifacts) {
			return nil, fmt.Errorf("plugin instance %s active package projection differs from its verified manifest", instance.ID)
		}
		kind, err := policyKindFromManifest(manifest)
		if err != nil {
			return nil, fmt.Errorf("plugin instance %s: %w", instance.ID, err)
		}
		entryArtifact, ok := policyEntryArtifact(artifacts, packageRow.EntryPath)
		if !ok {
			return nil, fmt.Errorf("plugin instance %s policy entry artifact is unavailable", instance.ID)
		}
		cacheRoot := filepath.Join(s.dataRoot, "plugins", "packages")
		if err := marketplace.ValidateCachePath(cacheRoot, packageRow.CachePath, packageRow.Digest, packageRow.SignatureFingerprint); err != nil {
			return nil, fmt.Errorf("plugin instance %s verified cache: %w", instance.ID, err)
		}
		artifactPath := filepath.Join(packageRow.CachePath, filepath.FromSlash(entryArtifact.Path))
		relativeArtifact, err := filepath.Rel(packageRow.CachePath, artifactPath)
		if err != nil || relativeArtifact == "." || relativeArtifact == ".." || strings.HasPrefix(relativeArtifact, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("plugin instance %s artifact path escapes its verified package", instance.ID)
		}
		config := json.RawMessage(strings.TrimSpace(instance.ConfigJSON))
		if len(config) == 0 {
			config = json.RawMessage(`{}`)
		}
		if !json.Valid(config) {
			return nil, fmt.Errorf("plugin instance %s config is invalid", instance.ID)
		}
		declaredScopes, grantedScopes, err := s.activePolicyScopes(ctx, installed, manifest, instance.ID, agentID)
		if err != nil {
			return nil, fmt.Errorf("plugin instance %s grants: %w", instance.ID, err)
		}
		if agentID != s.LocalAgentID() {
			artifactPath = ""
		}
		targetsJSON, _ := json.Marshal(targets)
		stage := PolicyStage{
			Kind: kind, PluginID: packageRow.PluginID, PluginVersion: packageRow.Version,
			PolicyID: instance.ID, InstanceID: instance.ID, PackageDigest: packageRow.Digest, ArtifactPath: artifactPath,
			ArtifactDigest: entryArtifact.SHA256,
			ArtifactSource: PolicyArtifactSource{
				ArtifactID: entryArtifact.ID, PackageIdentity: packageRow.Identity, PackageDigest: packageRow.Digest,
				RelativePath: entryArtifact.Path, SHA256: entryArtifact.SHA256, SizeBytes: entryArtifact.SizeBytes,
			},
			SignatureVerified: true,
			SignerKeyID:       installed.ActiveSignatureKeyID, SignerFingerprint: installed.ActiveSignatureFingerprint,
			ABI: packageRow.RuntimeABI, ExtensionPoints: append([]string(nil), manifest.ExtensionPoints...),
			DeclaredScopes: declaredScopes, GrantedScopes: grantedScopes, ResourceGroupID: instance.ResourceGroupID,
			Config: append(json.RawMessage(nil), config...),
			ResourceBudget: PolicyResourceBudget{
				TimeoutMS: manifest.ResourceBudget.TimeoutMS, MemoryBytes: manifest.ResourceBudget.MemoryBytes,
				Concurrency: manifest.ResourceBudget.Concurrency, InputBytes: manifest.ResourceBudget.InputBytes,
				OutputBytes: manifest.ResourceBudget.OutputBytes,
			},
			FailurePolicy: PolicyFailurePolicy{
				OnError: manifest.FailurePolicy.OnError, OnBudget: manifest.FailurePolicy.OnBudget,
				Restart: manifest.FailurePolicy.Restart, CoreFallback: manifest.FailurePolicy.CoreFallback,
			},
		}
		for _, chainID := range memberships {
			chain := chains[chainID]
			if chain == nil {
				chain = &chainProjection{targetsKey: string(targetsJSON), kinds: make(map[string]string)}
				chains[chainID] = chain
			} else if chain.targetsKey != string(targetsJSON) {
				return nil, fmt.Errorf("policy chain %s has incompatible target sets", chainID)
			}
			if previous, exists := chain.kinds[kind]; exists {
				return nil, fmt.Errorf("policy chain %s has duplicate %s stages from instances %s and %s", chainID, kind, previous, instance.ID)
			}
			chain.kinds[kind] = instance.ID
			chain.stages = append(chain.stages, stage)
		}
	}
	policies := make([]PluginPolicy, 0, len(chains))
	for chainID, chain := range chains {
		sort.Slice(chain.stages, func(i, j int) bool {
			left, right := policyStageOrder(chain.stages[i].Kind), policyStageOrder(chain.stages[j].Kind)
			if left != right {
				return left < right
			}
			return chain.stages[i].InstanceID < chain.stages[j].InstanceID
		})
		policies = append(policies, PluginPolicy{ID: chainID, Revision: catalogRevision.Revision, Stages: chain.stages})
	}
	sort.Slice(policies, func(i, j int) bool { return policies[i].ID < policies[j].ID })
	return policies, nil
}

func (s *GormStore) validatePolicyChainMembershipTopology(ctx context.Context, instances []PluginInstanceRow) error {
	type topology struct {
		targets string
		kinds   map[string]string
	}
	chains := make(map[string]*topology)
	for _, instance := range instances {
		memberships, err := CanonicalPluginPolicyChains(instance.PolicyChainsJSON)
		if err != nil {
			return fmt.Errorf("plugin instance %s policy chains: %w", instance.ID, err)
		}
		if instance.ConfigVersion == 0 || len(memberships) == 0 {
			continue
		}
		targets, err := pluginInstanceTargets(instance.TargetJSON, s.LocalAgentID())
		if err != nil {
			return fmt.Errorf("plugin instance %s targets: %w", instance.ID, err)
		}
		var installed InstalledPluginRow
		if err := s.pluginPolicyGraphQuery(ctx).Where("plugin_id = ?", instance.PluginID).First(&installed).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		} else if err != nil {
			return fmt.Errorf("plugin instance %s installed state: %w", instance.ID, err)
		}
		if !installedProjectsActivePolicy(installed) || installed.RuntimeKind != "wasm-policy" || installed.HostScope != "agent" {
			continue
		}
		var packageRow PluginPackageRow
		if err := s.pluginPolicyGraphQuery(ctx).Where("identity = ? AND digest = ?", installed.ActivePackageIdentity, installed.ActivePackageDigest).First(&packageRow).Error; err != nil {
			return fmt.Errorf("plugin instance %s active package: %w", instance.ID, err)
		}
		kind := strings.ToLower(packageRow.PolicyKind)
		if policyStageOrder(kind) > 2 {
			return fmt.Errorf("plugin instance %s policy kind is invalid", instance.ID)
		}
		targetsJSON, _ := json.Marshal(targets)
		for _, chainID := range memberships {
			current := chains[chainID]
			if current == nil {
				current = &topology{targets: string(targetsJSON), kinds: make(map[string]string)}
				chains[chainID] = current
			} else if current.targets != string(targetsJSON) {
				return fmt.Errorf("policy chain %s has incompatible target sets", chainID)
			}
			if previous, exists := current.kinds[kind]; exists {
				return fmt.Errorf("policy chain %s has duplicate %s stages from instances %s and %s", chainID, kind, previous, instance.ID)
			}
			current.kinds[kind] = instance.ID
		}
	}
	return nil
}

func policyStageOrder(kind string) int {
	switch kind {
	case "ip":
		return 0
	case "rate":
		return 1
	case "waf":
		return 2
	default:
		return 3
	}
}

// ValidatePluginPolicyIdentity delegates every storage identity boundary to
// the canonical SDK contract.
func ValidatePluginPolicyIdentity(value string) error { return pluginsdk.ValidatePolicyIdentity(value) }

// CanonicalPluginPolicyChains parses and canonicalizes an instance's explicit
// chain membership. Blank legacy values mean no published policy.
func CanonicalPluginPolicyChains(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return []string{}, nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	var chains []string
	if err := decoder.Decode(&chains); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("multiple policy chain values")
		}
		return nil, err
	}
	seen := make(map[string]struct{}, len(chains))
	for _, chainID := range chains {
		if err := ValidatePluginPolicyIdentity(chainID); err != nil {
			return nil, fmt.Errorf("policy chain identity %q: %w", chainID, err)
		}
		if _, exists := seen[chainID]; exists {
			return nil, fmt.Errorf("duplicate policy chain identity %q", chainID)
		}
		seen[chainID] = struct{}{}
	}
	sort.Strings(chains)
	return chains, nil
}

// EncodePluginPolicyChains validates and encodes canonical chain membership.
func EncodePluginPolicyChains(chains []string) (string, error) {
	encoded, err := json.Marshal(chains)
	if err != nil {
		return "", err
	}
	canonical, err := CanonicalPluginPolicyChains(string(encoded))
	if err != nil {
		return "", err
	}
	encoded, err = json.Marshal(canonical)
	return string(encoded), err
}

// LoadAgentPluginPolicies returns the authoritative active policy catalog for
// one agent. Reads never create or lock the per-Agent catalog fence. Callers
// that must serialize with plugin lifecycle changes explicitly acquire the
// existing fence through LockAgentPluginPolicyCatalog first.
func (s *GormStore) LoadAgentPluginPolicies(ctx context.Context, agentID string) ([]PluginPolicy, error) {
	agentID = s.resolveAgentID(agentID)
	return s.loadAgentPluginPolicies(ctx, agentID)
}

// pluginPolicyGraphQuery always participates in the loader's surrounding
// transaction. Standalone loaders establish one repeatable-read snapshot;
// mutation-scoped loaders reuse their caller's transaction. Per-statement
// shared locks cannot make a multi-query projection atomic and would create
// avoidable lock-upgrade cycles with plugin lifecycle mutations.
func (s *GormStore) pluginPolicyGraphQuery(ctx context.Context) *gorm.DB {
	return s.db.WithContext(ctx)
}

func installedProjectsActivePolicy(installed InstalledPluginRow) bool {
	switch installed.CurrentLifecycle {
	case "active", "upgrading", "rolling_back":
		return installed.DesiredLifecycle == "enabled"
	case "applying":
		return installed.DesiredLifecycle == "disabled" && installed.PendingKind == "disable"
	case "degraded":
		return installed.DesiredLifecycle == "disabled"
	default:
		return false
	}
}

func validateActivePolicyPackage(installed InstalledPluginRow, packageRow PluginPackageRow) error {
	if packageRow.SignatureVerdict != "verified" || packageRow.RuntimeKind != "wasm-policy" || packageRow.RuntimeABI != installed.RuntimeABI || packageRow.HostScope != "agent" {
		return errors.New("active policy package runtime evidence is incomplete")
	}
	if !strings.EqualFold(packageRow.Identity, installed.ActivePackageIdentity) || !strings.EqualFold(packageRow.Digest, installed.ActivePackageDigest) || packageRow.PluginID != installed.PluginID ||
		packageRow.SourceID != installed.ActiveSourceID || packageRow.SourceKind != installed.ActiveSourceKind || packageRow.SignatureKeyID != installed.ActiveSignatureKeyID ||
		packageRow.SignaturePublicKey != installed.ActiveSignaturePublicKey || !strings.EqualFold(packageRow.SignatureFingerprint, installed.ActiveSignatureFingerprint) {
		return errors.New("active policy package identity or signer binding differs from installed state")
	}
	return nil
}

func policyKindFromManifest(manifest plugins.Manifest) (string, error) {
	kind := strings.ToLower(strings.TrimSpace(manifest.Runtime.PolicyKind))
	switch kind {
	case "ip", "rate", "waf":
		return kind, nil
	default:
		return "", errors.New("wasm-policy manifest must declare runtime.policy_kind as ip, rate, or waf")
	}
}

func policyEntryArtifact(artifacts []PluginArtifactRow, entryPath string) (PluginArtifactRow, bool) {
	entryPath = filepath.ToSlash(filepath.Clean(strings.TrimSpace(entryPath)))
	for _, artifact := range artifacts {
		if filepath.ToSlash(filepath.Clean(artifact.Path)) == entryPath {
			return artifact, true
		}
	}
	return PluginArtifactRow{}, false
}

func (s *GormStore) activePolicyScopes(ctx context.Context, installed InstalledPluginRow, manifest plugins.Manifest, instanceID, agentID string) ([]string, []string, error) {
	var rows []PluginGrantRow
	if err := s.db.WithContext(ctx).
		Where("plugin_id = ? AND package_identity = ? AND package_digest = ?", installed.PluginID, installed.ActivePackageIdentity, installed.ActivePackageDigest).
		Order("permission").Find(&rows).Error; err != nil {
		return nil, nil, err
	}
	return projectPolicyScopes(manifest, rows, instanceID, agentID)
}

func projectPolicyScopes(manifest plugins.Manifest, rows []PluginGrantRow, instanceID, agentID string) ([]string, []string, error) {
	allDeclared := make(map[string]struct{}, len(manifest.Permissions))
	declared := make(map[string]struct{}, len(manifest.Permissions))
	for _, permission := range manifest.Permissions {
		name := strings.TrimSpace(permission.Name)
		allDeclared[name] = struct{}{}
		if policySelectorApplies(permission.Resource, instanceID, agentID) {
			declared[name] = struct{}{}
		}
	}
	declaredResult := make([]string, 0, len(declared))
	for permission := range declared {
		declaredResult = append(declaredResult, permission)
	}
	grantedResult := make([]string, 0, len(rows))
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		permission := strings.TrimSpace(row.Permission)
		if _, ok := allDeclared[permission]; !ok {
			return nil, nil, fmt.Errorf("permission %q is not declared by the active manifest", permission)
		}
		if _, ok := declared[permission]; !ok || !policySelectorApplies(row.ResourceSelector, instanceID, agentID) {
			continue
		}
		if _, ok := seen[permission]; ok {
			continue
		}
		seen[permission] = struct{}{}
		grantedResult = append(grantedResult, permission)
	}
	sort.Strings(declaredResult)
	sort.Strings(grantedResult)
	return declaredResult, grantedResult, nil
}

func policySelectorApplies(selector, instanceID, agentID string) bool {
	selector = strings.TrimSpace(selector)
	return selector == "" || selector == strings.TrimSpace(instanceID) || selector == strings.TrimSpace(agentID)
}

func containsPluginTarget(targets []string, agentID string) bool {
	agentID = strings.TrimSpace(agentID)
	for _, target := range targets {
		if strings.TrimSpace(target) == agentID {
			return true
		}
	}
	return false
}

func (s *GormStore) SaveMarketplaceSource(ctx context.Context, source marketplace.Source) error {
	if err := marketplace.ValidateSource(source); err != nil {
		return err
	}
	row := marketplaceSourceToRow(source)
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		if source.Kind == marketplace.SourceKindCustom {
			var existing MarketplaceSourceRow
			if err := tx.Where("id = ?", marketplace.OfficialSourceID).First(&existing).Error; err != nil && err != gorm.ErrRecordNotFound {
				return err
			}
		}
		if err := tx.Create(&row).Error; err != nil {
			if isDuplicateKeyError(err) {
				return ErrMarketplaceSourceExists
			}
			return err
		}
		if source.Kind == marketplace.SourceKindCustom {
			actor, _ := QuotaActorFromContext(ctx)
			metadata, _ := json.Marshal(map[string]any{"kind": source.Kind, "risk_label": source.RiskLabel, "has_credential_ref": source.CredentialRef != "", "signer_key_id": source.SignerKeyID, "has_signer_ref": source.SignerSecretRef != ""})
			if err := tx.Create(&AuditEventRow{ID: pluginStorageID("audit"), ActorID: actor.UserID, SessionID: actor.SessionID, Action: "marketplace.source.add", TargetKind: "marketplace_source", TargetID: source.ID, CorrelationID: actor.CorrelationID, Result: "success", MetadataJSON: string(metadata), CreatedAt: time.Now().UTC()}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *GormStore) UpdateMarketplaceSource(ctx context.Context, source marketplace.Source, expectedRevision uint64) (marketplace.Source, error) {
	validCustom := source.Kind == marketplace.SourceKindCustom && source.ID != marketplace.OfficialSourceID
	validOfficial := source.Kind == marketplace.SourceKindOfficial && source.ID == marketplace.OfficialSourceID
	if (!validCustom && !validOfficial) || expectedRevision == 0 || source.ConfigRevision != expectedRevision+1 {
		return marketplace.Source{}, marketplace.ErrInvalidSource
	}
	if err := marketplace.ValidateSource(source); err != nil {
		return marketplace.Source{}, err
	}
	var updated marketplace.Source
	err := s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var locked MarketplaceSourceRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND kind = ? AND deleting = ?", source.ID, source.Kind, false).First(&locked).Error; err != nil {
			return err
		}
		if locked.ConfigRevision != expectedRevision {
			return fmt.Errorf("%w: marketplace source config revision changed", ErrPluginConflict)
		}
		now := time.Now().UTC()
		if locked.RefreshLeaseToken != "" {
			var active MarketplaceRefreshOperationRow
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("source_id = ? AND lease_token = ? AND status = ?", locked.ID, locked.RefreshLeaseToken, "running").First(&active).Error; err != nil {
				return fmt.Errorf("%w: active marketplace refresh lease is inconsistent", ErrPluginConflict)
			}
			if err := validateRefreshOperationSourceBinding(active, locked); err != nil {
				return err
			}
			message := "source configuration changed during refresh"
			active.Status, active.ErrorClass, active.Error, active.FinishedAt, active.LeaseExpiresAt = "failed", "source_generation_changed", message, &now, now
			result := tx.Model(&MarketplaceRefreshOperationRow{}).Where("id = ? AND status = ? AND lease_token = ?", active.ID, "running", active.LeaseToken).Updates(map[string]any{
				"status": active.Status, "error_class": active.ErrorClass, "error": active.Error, "finished_at": now, "lease_expires_at": now,
			})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("%w: active marketplace refresh changed concurrently", ErrPluginConflict)
			}
			var staged []PluginPackageStagingRow
			if err := tx.Where("source_id = ? AND operation_id = ?", locked.ID, active.ID).Order("digest, signer_fingerprint").Find(&staged).Error; err != nil {
				return err
			}
			for _, candidate := range staged {
				if !strings.EqualFold(candidate.SignerFingerprint, active.SignerFingerprint) {
					return fmt.Errorf("%w: staged package signer differs from refresh operation", ErrPluginConflict)
				}
				if err := scheduleRefreshOperationPackageGCTx(tx, active, candidate.Digest, now); err != nil {
					return err
				}
			}
			audit, err := marketplaceRefreshAudit(active, "failure", now)
			if err != nil {
				return err
			}
			if err := tx.Create(&audit).Error; err != nil {
				return err
			}
		}
		row := marketplaceSourceToRow(source)
		result := tx.Model(&MarketplaceSourceRow{}).Where("id = ? AND config_revision = ? AND deleting = ?", source.ID, expectedRevision, false).Updates(map[string]any{
			"purpose": row.Purpose, "name": row.Name, "name_key": row.NameKey, "url": row.URL, "ref_kind": row.RefKind, "ref_name": row.RefName,
			"credential_ref": row.CredentialRef, "signer_key_id": row.SignerKeyID, "signer_secret_ref": row.SignerSecretRef,
			"signer_public_key": row.SignerPublicKey, "signer_fingerprint": row.SignerFingerprint,
			"refresh_interval_ns": row.RefreshIntervalNS, "risk_label": row.RiskLabel, "config_revision": row.ConfigRevision,
			"current_snapshot_id": "", "current_resolved_oid": "", "last_result": "refresh_required", "last_error": "", "refresh_lease_token": "", "refresh_lease_expires_at": time.Time{}, "updated_at": now,
		})
		if result.Error != nil {
			if isDuplicateKeyError(result.Error) {
				return ErrMarketplaceSourceExists
			}
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("%w: marketplace source config revision changed", ErrPluginConflict)
		}
		if err := tx.Where("id = ?", source.ID).First(&locked).Error; err != nil {
			return err
		}
		updated = marketplaceSourceFromRow(locked)
		actor, _ := QuotaActorFromContext(ctx)
		metadata, _ := json.Marshal(map[string]any{"purpose": updated.Purpose, "ref_kind": updated.RefKind, "ref_name": updated.RefName, "config_revision": updated.ConfigRevision, "has_credential_ref": updated.CredentialRef != ""})
		action := "marketplace.source.edit"
		if source.Kind == marketplace.SourceKindOfficial {
			action = "marketplace.source.policy_sync"
		}
		return tx.Create(&AuditEventRow{ID: pluginStorageID("audit"), ActorID: actor.UserID, SessionID: actor.SessionID, Action: action, TargetKind: "marketplace_source", TargetID: source.ID, CorrelationID: actor.CorrelationID, Result: "success", MetadataJSON: string(metadata), CreatedAt: time.Now().UTC()}).Error
	})
	return updated, err
}

func (s *GormStore) ReconcileOfficialMarketplaceSource(ctx context.Context, desired marketplace.Source) (marketplace.Source, error) {
	if desired.Kind != marketplace.SourceKindOfficial || desired.ID != marketplace.OfficialSourceID {
		return marketplace.Source{}, marketplace.ErrInvalidSource
	}
	if err := marketplace.ValidateSource(desired); err != nil {
		return marketplace.Source{}, err
	}
	for attempt := 0; attempt < 3; attempt++ {
		current, found, err := s.GetMarketplaceSource(ctx, marketplace.OfficialSourceID)
		if err != nil {
			return marketplace.Source{}, err
		}
		if !found {
			candidate, err := marketplace.OfficialSourceForBranch(desired.RefName, 1)
			if err != nil {
				return marketplace.Source{}, err
			}
			if err := s.SaveMarketplaceSource(ctx, candidate); err == nil {
				return candidate, nil
			} else if !errors.Is(err, ErrMarketplaceSourceExists) {
				return marketplace.Source{}, err
			}
			continue
		}
		if current.RefKind == desired.RefKind && current.RefName == desired.RefName {
			return current, nil
		}
		candidate := current
		candidate.RefKind = desired.RefKind
		candidate.RefName = desired.RefName
		candidate.ConfigRevision = current.ConfigRevision + 1
		updated, err := s.UpdateMarketplaceSource(ctx, candidate, current.ConfigRevision)
		if err == nil {
			return updated, nil
		}
		if !errors.Is(err, ErrPluginConflict) {
			return marketplace.Source{}, err
		}
	}
	return marketplace.Source{}, fmt.Errorf("%w: official marketplace branch changed concurrently", ErrPluginConflict)
}

func (s *GormStore) ListMarketplaceSources(ctx context.Context) ([]marketplace.Source, error) {
	var rows []MarketplaceSourceRow
	if err := s.db.WithContext(ctx).Order("kind, id").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]marketplace.Source, 0, len(rows))
	for _, row := range rows {
		result = append(result, marketplaceSourceFromRow(row))
	}
	return result, nil
}

func (s *GormStore) GetMarketplaceSource(ctx context.Context, sourceID string) (marketplace.Source, bool, error) {
	var row MarketplaceSourceRow
	err := s.db.WithContext(ctx).Where("id = ?", sourceID).First(&row).Error
	if err == gorm.ErrRecordNotFound {
		return marketplace.Source{}, false, nil
	}
	if err != nil {
		return marketplace.Source{}, false, err
	}
	if row.Deleting {
		return marketplace.Source{}, false, nil
	}
	return marketplaceSourceFromRow(row), true, nil
}

func backfillMarketplaceDirectoryCleanup(ctx context.Context, db *gorm.DB) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existingRows []MarketplaceDirectoryCleanupRow
		if err := tx.Find(&existingRows).Error; err != nil {
			return err
		}
		for _, row := range existingRows {
			state := row.State
			if state == "" {
				state = "retired"
			}
			if err := tx.Model(&MarketplaceDirectoryCleanupRow{}).Where("id = ?", row.ID).Updates(map[string]any{"path_digest": pluginStorageDigest(row.Path), "state": state}).Error; err != nil {
				return err
			}
		}
		var deletions []MarketplaceSourceDeletionRow
		if err := tx.Find(&deletions).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		for _, deletion := range deletions {
			var paths []string
			if strings.TrimSpace(deletion.SnapshotPathsJSON) != "" && deletion.SnapshotPathsJSON != "[]" {
				if err := json.Unmarshal([]byte(deletion.SnapshotPathsJSON), &paths); err != nil {
					return err
				}
			}
			for _, candidate := range paths {
				candidate = filepath.ToSlash(filepath.Clean(filepath.FromSlash(candidate)))
				if candidate == "." || candidate == ".." || strings.HasPrefix(candidate, "../") || filepath.IsAbs(candidate) {
					return errors.New("invalid marketplace snapshot cleanup path")
				}
				relative := path.Join("snapshots", candidate)
				row := MarketplaceDirectoryCleanupRow{ID: pluginStorageID("dirgc"), SourceID: deletion.SourceID, Path: relative, PathDigest: pluginStorageDigest(relative), State: "retired", UpdatedAt: now}
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
					return err
				}
			}
			if len(paths) > 0 {
				if err := tx.Model(&MarketplaceSourceDeletionRow{}).Where("source_id = ?", deletion.SourceID).Update("snapshot_paths_json", "[]").Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (s *GormStore) RegisterMarketplaceDirectoryCleanup(ctx context.Context, sourceID, operationID string, candidates []string) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		now := time.Now().UTC()
		for _, candidate := range candidates {
			relative, err := relativeMarketplaceDirectoryPath(s.dataRoot, candidate)
			if err != nil {
				return err
			}
			row := MarketplaceDirectoryCleanupRow{ID: pluginStorageID("dirgc"), SourceID: sourceID, OperationID: operationID, Path: relative, PathDigest: pluginStorageDigest(relative), State: "provisional", UpdatedAt: now}
			var existing MarketplaceDirectoryCleanupRow
			err = tx.Where("path_digest = ?", row.PathDigest).First(&existing).Error
			if err == nil {
				if existing.Path != row.Path {
					return errors.New("marketplace cleanup path digest collision")
				}
				continue
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *GormStore) ClaimMarketplaceDirectoryCleanup(ctx context.Context, sourceID string, ttl time.Duration) (marketplace.DirectoryCleanupWork, bool, error) {
	if ttl <= 0 {
		return marketplace.DirectoryCleanupWork{}, false, errors.New("marketplace directory cleanup claim TTL must be positive")
	}
	var work marketplace.DirectoryCleanupWork
	err := s.writeTransaction(ctx, func(tx *gorm.DB) error {
		now := time.Now().UTC()
		query := tx.Where("claim_token = ? OR claim_expires_at <= ?", "", now)
		if sourceID != "" {
			query = query.Where("source_id = ?", sourceID)
		}
		var rows []MarketplaceDirectoryCleanupRow
		if err := query.Order("source_id, path_digest").Limit(32).Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			var source MarketplaceSourceRow
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", row.SourceID).First(&source).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			var locked MarketplaceDirectoryCleanupRow
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND (claim_token = ? OR claim_expires_at <= ?)", row.ID, "", now).First(&locked).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					continue
				}
				return err
			}
			row = locked
			if row.State == "provisional" && row.OperationID != "" {
				var operation MarketplaceRefreshOperationRow
				err := tx.Where("id = ?", row.OperationID).First(&operation).Error
				if err == nil && operation.Status == "running" && operation.LeaseExpiresAt.After(now) {
					continue
				}
				if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
			}
			var current int64
			if err := tx.Model(&MarketSnapshotRow{}).Joins("JOIN marketplace_sources ON marketplace_sources.current_snapshot_id = market_snapshots.id").Where("market_snapshots.path = ? AND marketplace_sources.deleting = ?", absoluteMarketplaceDirectoryPath(s.dataRoot, row.Path), false).Count(&current).Error; err != nil {
				return err
			}
			if current != 0 {
				continue
			}
			token := pluginStorageID("dirclaim")
			result := tx.Model(&MarketplaceDirectoryCleanupRow{}).Where("id = ? AND (claim_token = ? OR claim_expires_at <= ?)", row.ID, "", now).Updates(map[string]any{"claim_token": token, "claim_expires_at": now.Add(ttl), "updated_at": now})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 1 {
				work = marketplace.DirectoryCleanupWork{ID: row.ID, SourceID: row.SourceID, OperationID: row.OperationID, Path: row.Path, ClaimToken: token}
				return nil
			}
		}
		return nil
	})
	return work, work.ID != "", err
}

// ListMarketplaceDirectoryCleanup is a read-only diagnostic projection. File
// deletion must always use ClaimMarketplaceDirectoryCleanup instead.
func (s *GormStore) ListMarketplaceDirectoryCleanup(ctx context.Context) ([]marketplace.DirectoryCleanupWork, error) {
	var rows []MarketplaceDirectoryCleanupRow
	if err := s.db.WithContext(ctx).Order("source_id, path_digest").Find(&rows).Error; err != nil {
		return nil, err
	}
	works := make([]marketplace.DirectoryCleanupWork, 0, len(rows))
	for _, row := range rows {
		works = append(works, marketplace.DirectoryCleanupWork{ID: row.ID, SourceID: row.SourceID, OperationID: row.OperationID, Path: row.Path, ClaimToken: row.ClaimToken})
	}
	return works, nil
}

func (s *GormStore) CompleteMarketplaceDirectoryCleanup(ctx context.Context, work marketplace.DirectoryCleanupWork, failure string) error {
	if strings.TrimSpace(work.ID) == "" || strings.TrimSpace(work.ClaimToken) == "" {
		return errors.New("marketplace directory cleanup identity is required")
	}
	if failure != "" {
		return s.db.WithContext(ctx).Model(&MarketplaceDirectoryCleanupRow{}).Where("id = ? AND claim_token = ?", work.ID, work.ClaimToken).Updates(map[string]any{"claim_token": "", "claim_expires_at": time.Time{}, "last_error": failure, "updated_at": time.Now().UTC()}).Error
	}
	result := s.db.WithContext(ctx).Where("id = ? AND claim_token = ?", work.ID, work.ClaimToken).Delete(&MarketplaceDirectoryCleanupRow{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("marketplace directory cleanup claim is stale")
	}
	return nil
}

func absoluteMarketplaceDirectoryPath(dataRoot, relative string) string {
	return filepath.Clean(filepath.Join(dataRoot, "marketplace", filepath.FromSlash(relative)))
}

func ensureMarketplaceDirectoryCleanupTx(tx *gorm.DB, row MarketplaceDirectoryCleanupRow) error {
	row.PathDigest = pluginStorageDigest(row.Path)
	if row.State == "" {
		row.State = "retired"
	}
	var existing MarketplaceDirectoryCleanupRow
	err := tx.Where("path_digest = ?", row.PathDigest).First(&existing).Error
	if err == nil {
		if existing.Path != row.Path {
			return errors.New("marketplace cleanup path digest collision")
		}
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return tx.Create(&row).Error
}

func (s *GormStore) ListMarketplaceSourceDeletions(ctx context.Context) ([]string, error) {
	var sourceIDs []string
	if err := s.db.WithContext(ctx).Model(&MarketplaceSourceDeletionRow{}).Order("source_id").Pluck("source_id", &sourceIDs).Error; err != nil {
		return nil, err
	}
	return sourceIDs, nil
}

func relativeMarketplaceDirectoryPath(dataRoot, candidate string) (string, error) {
	root, err := filepath.Abs(filepath.Join(dataRoot, "marketplace"))
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, filepath.FromSlash(candidate))
	}
	candidate, err = filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("marketplace cleanup path is outside the managed root")
	}
	return filepath.ToSlash(relative), nil
}

func relativeMarketplaceSnapshotDirectoryPath(dataRoot, candidate string) (string, error) {
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(dataRoot, "marketplace", "snapshots", filepath.FromSlash(candidate))
	}
	return relativeMarketplaceDirectoryPath(dataRoot, candidate)
}

func (s *GormStore) DeleteMarketplaceSource(ctx context.Context, sourceID string) (marketplace.SourceDeletion, error) {
	if sourceID == marketplace.OfficialSourceID {
		return marketplace.SourceDeletion{}, errors.New("official marketplace source cannot be deleted")
	}
	var deletion marketplace.SourceDeletion
	err := s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var source MarketplaceSourceRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND kind = ?", sourceID, marketplace.SourceKindCustom).First(&source).Error; err != nil {
			return err
		}
		if source.Deleting {
			var pending MarketplaceSourceDeletionRow
			if err := tx.Where("source_id = ?", sourceID).First(&pending).Error; err != nil {
				return err
			}
			if err := json.Unmarshal([]byte(pending.SnapshotPathsJSON), &deletion.SnapshotPaths); err != nil {
				return err
			}
			var intents []PluginCacheGCIntentRow
			if err := tx.Where("source_id = ?", sourceID).Find(&intents).Error; err != nil {
				return err
			}
			for _, intent := range intents {
				deletion.CacheDigests = append(deletion.CacheDigests, intent.Digest)
			}
			return nil
		}
		if err := tx.Model(&MarketplaceSourceRow{}).Where("id = ? AND deleting = ?", sourceID, false).Updates(map[string]any{"deleting": true, "current_snapshot_id": ""}).Error; err != nil {
			return err
		}
		var snapshots []MarketSnapshotRow
		if err := tx.Where("source_id = ?", sourceID).Find(&snapshots).Error; err != nil {
			return err
		}
		snapshotIDs := make([]string, 0, len(snapshots))
		for _, snapshot := range snapshots {
			snapshotIDs = append(snapshotIDs, snapshot.ID)
			relative, err := relativeMarketplaceSnapshotDirectoryPath(s.dataRoot, snapshot.Path)
			if err != nil {
				return err
			}
			deletion.SnapshotPaths = append(deletion.SnapshotPaths, relative)
			work := MarketplaceDirectoryCleanupRow{ID: pluginStorageID("dirgc"), SourceID: sourceID, Path: relative, State: "retired", UpdatedAt: time.Now().UTC()}
			if err := ensureMarketplaceDirectoryCleanupTx(tx, work); err != nil {
				return err
			}
		}
		var entries []MarketEntryRow
		if len(snapshotIDs) > 0 {
			if err := tx.Where("snapshot_id IN ?", snapshotIDs).Find(&entries).Error; err != nil {
				return err
			}
			if err := tx.Where("snapshot_id IN ?", snapshotIDs).Delete(&MarketEntryRow{}).Error; err != nil {
				return err
			}
			if err := tx.Where("id IN ?", snapshotIDs).Delete(&MarketSnapshotRow{}).Error; err != nil {
				return err
			}
		}
		var acquisitions []PluginPackageAcquisitionRow
		if err := tx.Where("source_id = ?", sourceID).Find(&acquisitions).Error; err != nil {
			return err
		}
		type gcVariant struct{ digest, fingerprint string }
		seenVariants := map[gcVariant]struct{}{}
		seenDigests := map[string]struct{}{}
		for _, entry := range entries {
			digest := strings.ToLower(entry.PackageDigest)
			seenDigests[digest] = struct{}{}
			seenVariants[gcVariant{digest, source.SignerFingerprint}] = struct{}{}
		}
		for _, acquisition := range acquisitions {
			digest := strings.ToLower(acquisition.Digest)
			seenDigests[digest] = struct{}{}
			seenVariants[gcVariant{digest, acquisition.SignatureFingerprint}] = struct{}{}
		}
		var staging []PluginPackageStagingRow
		if err := tx.Where("source_id = ?", sourceID).Find(&staging).Error; err != nil {
			return err
		}
		for _, acquisition := range staging {
			digest := strings.ToLower(acquisition.Digest)
			seenDigests[digest] = struct{}{}
			seenVariants[gcVariant{digest, acquisition.SignerFingerprint}] = struct{}{}
		}
		var packages []PluginPackageRow
		if err := tx.Where("source_id = ?", sourceID).Find(&packages).Error; err != nil {
			return err
		}
		for _, pkg := range packages {
			digest := strings.ToLower(pkg.Digest)
			seenDigests[digest] = struct{}{}
			seenVariants[gcVariant{digest, pkg.SignatureFingerprint}] = struct{}{}
		}
		now := time.Now().UTC()
		for digest := range seenDigests {
			deletion.CacheDigests = append(deletion.CacheDigests, digest)
		}
		sort.Strings(deletion.CacheDigests)
		variants := make([]gcVariant, 0, len(seenVariants))
		for variant := range seenVariants {
			variants = append(variants, variant)
		}
		sort.Slice(variants, func(i, j int) bool {
			if variants[i].digest != variants[j].digest {
				return variants[i].digest < variants[j].digest
			}
			return variants[i].fingerprint < variants[j].fingerprint
		})
		for _, variant := range variants {
			if err := schedulePackageGCTx(tx, sourceID, variant.digest, variant.fingerprint, now); err != nil {
				return err
			}
		}
		if err := tx.Where("source_id = ?", sourceID).Delete(&PluginPackageAcquisitionRow{}).Error; err != nil {
			return err
		}
		if err := tx.Where("source_id = ?", sourceID).Delete(&PluginPackageStagingRow{}).Error; err != nil {
			return err
		}
		if err := tx.Create(&MarketplaceSourceDeletionRow{SourceID: sourceID, SnapshotPathsJSON: "[]", UpdatedAt: now}).Error; err != nil {
			return err
		}
		actor, _ := QuotaActorFromContext(ctx)
		metadata, _ := json.Marshal(map[string]any{"kind": source.Kind, "risk_label": source.RiskLabel, "has_credential_ref": source.CredentialRef != ""})
		return tx.Create(&AuditEventRow{ID: pluginStorageID("audit"), ActorID: actor.UserID, SessionID: actor.SessionID, Action: "marketplace.source.delete", TargetKind: "marketplace_source", TargetID: source.ID, CorrelationID: actor.CorrelationID, Result: "accepted", MetadataJSON: string(metadata), CreatedAt: now}).Error
	})
	return deletion, err
}

func (s *GormStore) ClaimPackageGC(ctx context.Context, sourceID, digest, signerFingerprint string) (marketplace.PackageGCClaim, bool, error) {
	digest = strings.ToLower(digest)
	signerFingerprint = strings.ToLower(strings.TrimSpace(signerFingerprint))
	if !marketplace.IsDigest(digest) || (signerFingerprint != "" && !marketplace.IsDigest(signerFingerprint)) {
		return marketplace.PackageGCClaim{}, false, errors.New("invalid package digest")
	}
	var claim marketplace.PackageGCClaim
	claimed := false
	err := s.writeTransaction(ctx, func(tx *gorm.DB) error {
		now := time.Now().UTC()
		fence, err := lockPackageDigestFenceTx(tx, digest, now)
		if err != nil {
			return err
		}
		if fence.ClaimToken != "" {
			if fence.ClaimExpiresAt.After(now) {
				return nil
			}
			if err := tx.Model(&PluginCacheGCIntentRow{}).Where("digest = ? AND claim_token = ?", digest, fence.ClaimToken).Updates(map[string]any{"status": "pending", "claim_token": "", "claim_expires_at": time.Time{}, "last_error": "package GC claim expired", "updated_at": now}).Error; err != nil {
				return err
			}
			if err := tx.Model(&PluginDigestFenceRow{}).Where("digest = ? AND claim_token = ?", digest, fence.ClaimToken).Updates(map[string]any{"claim_token": "", "claim_expires_at": time.Time{}, "updated_at": now}).Error; err != nil {
				return err
			}
		}
		var intent PluginCacheGCIntentRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("source_id = ? AND digest = ? AND signer_fingerprint = ?", sourceID, digest, signerFingerprint).First(&intent).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return nil
			}
			return err
		}
		var variants []PluginPackageRow
		if err := tx.Where("digest = ? AND signature_fingerprint = ?", digest, signerFingerprint).Order("identity").Find(&variants).Error; err != nil {
			return err
		}
		if intent.SignerFingerprint != "" && !marketplace.IsDigest(intent.SignerFingerprint) {
			return errors.New("package GC intent signer fingerprint is invalid")
		}
		identities := make([]string, 0, len(variants))
		for _, variant := range variants {
			identities = append(identities, variant.Identity)
		}
		referenced, err := pluginVariantReferenced(ctx, tx, "", digest, signerFingerprint, identities)
		if err != nil {
			return err
		}
		if referenced {
			return tx.Model(&PluginCacheGCIntentRow{}).Where("source_id = ? AND digest = ? AND signer_fingerprint = ?", sourceID, digest, signerFingerprint).Updates(map[string]any{"status": "pending", "deferred": true, "claim_token": "", "claim_expires_at": time.Time{}, "updated_at": now}).Error
		}
		token := pluginStorageID("gc")
		quarantineID := strings.TrimSpace(intent.QuarantineID)
		if quarantineID == "" {
			quarantineID = pluginStorageID("gcq")
		}
		expires := now.Add(5 * time.Minute)
		result := tx.Model(&PluginCacheGCIntentRow{}).Where("source_id = ? AND digest = ? AND signer_fingerprint = ?", sourceID, digest, signerFingerprint).Updates(map[string]any{"status": "deleting", "deferred": false, "claim_token": token, "claim_expires_at": expires, "quarantine_id": quarantineID, "last_error": "", "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		claimed = result.RowsAffected == 1
		if claimed {
			if err := tx.Model(&PluginDigestFenceRow{}).Where("digest = ?", digest).Updates(map[string]any{"claim_token": token, "claim_expires_at": expires, "updated_at": now}).Error; err != nil {
				return err
			}
			claim = marketplace.PackageGCClaim{SourceID: sourceID, Digest: digest, SignerFingerprint: intent.SignerFingerprint, Token: token, QuarantineID: quarantineID, QuarantinePath: intent.QuarantinePath, Trust: marketplace.SignatureTrust{SourceID: sourceID, SourceKind: intent.SignerSourceKind, KeyID: intent.SignerKeyID, PublicKey: intent.SignerPublicKey, Fingerprint: intent.SignerFingerprint}, ObjectsPrepared: intent.ObjectsPrepared}
			if intent.ObjectsPrepared {
				if err := json.Unmarshal([]byte(intent.CacheObjectsJSON), &claim.Objects); err != nil {
					return fmt.Errorf("decode package GC cache objects: %w", err)
				}
				for _, object := range claim.Objects {
					if err := marketplace.ValidatePackageGCObject(claim, object); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
	return claim, claimed, err
}

// pluginVariantReferenced is the single ownership query for a durable package
// variant. A blank sourceID asks about the shared physical signer object across
// all sources; a non-empty sourceID asks about one source-bound package row.
// Completed operations are immutable history and carry their own provenance,
// while pending operations and grants remain live authorization references.
func pluginVariantReferenced(ctx context.Context, db *gorm.DB, sourceID, digest, signerFingerprint string, identities []string) (bool, error) {
	digest = strings.ToLower(strings.TrimSpace(digest))
	signerFingerprint = strings.ToLower(strings.TrimSpace(signerFingerprint))
	sourceID = strings.TrimSpace(sourceID)

	var catalog int64
	acquisition := db.WithContext(ctx).Model(&PluginPackageAcquisitionRow{}).Where("digest = ?", digest)
	if signerFingerprint != "" {
		acquisition = acquisition.Where("signature_fingerprint = ?", signerFingerprint)
	}
	if sourceID != "" {
		acquisition = acquisition.Where("source_id = ?", sourceID)
	}
	if err := acquisition.Count(&catalog).Error; err != nil {
		return false, err
	}
	if catalog > 0 {
		return true, nil
	}

	var staging int64
	staged := db.WithContext(ctx).Model(&PluginPackageStagingRow{}).Where("digest = ?", digest)
	if signerFingerprint != "" {
		staged = staged.Where("signer_fingerprint = ?", signerFingerprint)
	}
	if sourceID != "" {
		staged = staged.Where("source_id = ?", sourceID)
	}
	if err := staged.Count(&staging).Error; err != nil {
		return false, err
	}
	if staging > 0 {
		return true, nil
	}

	var lifecycle, grants, operations int64
	if signerFingerprint == "" {
		if err := db.WithContext(ctx).Model(&InstalledPluginRow{}).
			Where("active_package_digest = ? OR staged_package_digest = ? OR rollback_package_digest = ? OR pending_target_digest = ?", digest, digest, digest, digest).
			Count(&lifecycle).Error; err != nil {
			return false, err
		}
		if err := db.WithContext(ctx).Model(&PluginGrantRow{}).Where("package_digest = ?", digest).Count(&grants).Error; err != nil {
			return false, err
		}
		if err := db.WithContext(ctx).Model(&PluginOperationRow{}).
			Where("target_package_digest = ? AND completed_at IS NULL AND status NOT IN ?", digest, []string{"succeeded", "failed"}).
			Count(&operations).Error; err != nil {
			return false, err
		}
	} else if len(identities) > 0 {
		if err := db.WithContext(ctx).Model(&InstalledPluginRow{}).
			Where("active_package_identity IN ? OR staged_package_identity IN ? OR rollback_package_identity IN ? OR pending_target_identity IN ?", identities, identities, identities, identities).
			Count(&lifecycle).Error; err != nil {
			return false, err
		}
		if err := db.WithContext(ctx).Model(&PluginGrantRow{}).
			Where("package_identity IN ? OR (package_identity = ? AND package_digest = ?)", identities, "", digest).
			Count(&grants).Error; err != nil {
			return false, err
		}
		if err := db.WithContext(ctx).Model(&PluginOperationRow{}).
			Where("completed_at IS NULL AND status NOT IN ? AND (target_package_identity IN ? OR (target_package_identity = ? AND target_package_digest = ?))", []string{"succeeded", "failed"}, identities, "", digest).
			Count(&operations).Error; err != nil {
			return false, err
		}
	}
	if lifecycle+grants+operations > 0 {
		return true, nil
	}

	// Historical operations do not own the cache, but detaching them is only
	// safe while their persisted provenance can still be checked against the
	// package candidate that is about to be retired.
	var completed []PluginOperationRow
	if err := db.WithContext(ctx).Where("target_package_digest = ? AND (completed_at IS NOT NULL OR status IN ?)", digest, []string{"succeeded", "failed"}).Find(&completed).Error; err != nil {
		return false, err
	}
	for _, operation := range completed {
		var candidate PluginPackageRow
		err := db.WithContext(ctx).Where("identity = ?", operation.TargetPackageIdentity).First(&candidate).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if err := validatePluginOperationPackageProvenance(operation, nil); err != nil {
				return false, fmt.Errorf("completed plugin operation %s detached package provenance: %w", operation.ID, err)
			}
			continue
		}
		if err != nil {
			return false, err
		}
		if err := validatePluginOperationPackageProvenance(operation, &candidate); err != nil {
			return false, fmt.Errorf("completed plugin operation %s package provenance: %w", operation.ID, err)
		}
	}
	return false, nil
}

func (s *GormStore) PreparePackageGCObjects(ctx context.Context, claim marketplace.PackageGCClaim, objects []marketplace.PackageGCObject) error {
	digest := strings.ToLower(strings.TrimSpace(claim.Digest))
	if !marketplace.IsDigest(digest) || claim.Token == "" || claim.QuarantineID == "" {
		return errors.New("valid package GC object claim is required")
	}
	for _, object := range objects {
		if err := marketplace.ValidatePackageGCObject(claim, object); err != nil {
			return err
		}
	}
	encoded, err := json.Marshal(objects)
	if err != nil {
		return err
	}
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		now := time.Now().UTC()
		expires := now.Add(5 * time.Minute)
		result := tx.Model(&PluginCacheGCIntentRow{}).Where("source_id = ? AND digest = ? AND signer_fingerprint = ? AND claim_token = ? AND quarantine_id = ? AND status = ?", claim.SourceID, digest, claim.SignerFingerprint, claim.Token, claim.QuarantineID, "deleting").Updates(map[string]any{"objects_prepared": true, "cache_objects_json": string(encoded), "claim_expires_at": expires, "updated_at": now})
		if result.Error != nil || result.RowsAffected != 1 {
			return errors.New("package GC claim is stale")
		}
		result = tx.Model(&PluginDigestFenceRow{}).Where("digest = ? AND claim_token = ?", digest, claim.Token).Updates(map[string]any{"claim_expires_at": expires, "updated_at": now})
		if result.Error != nil || result.RowsAffected != 1 {
			return errors.New("package GC claim is stale")
		}
		return nil
	})
}

func (s *GormStore) PreparePackageGCQuarantine(ctx context.Context, claim marketplace.PackageGCClaim, quarantinePath string) error {
	digest := strings.ToLower(claim.Digest)
	if !marketplace.IsDigest(digest) || claim.Token == "" || strings.TrimSpace(quarantinePath) == "" {
		return errors.New("valid package GC quarantine is required")
	}
	if claim.SignerFingerprint != "" {
		expected := claim
		expected.QuarantinePath = quarantinePath
		if _, err := marketplace.PackageGCQuarantinePath(expected); err != nil {
			return err
		}
	}
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		now := time.Now().UTC()
		expires := now.Add(5 * time.Minute)
		result := tx.Model(&PluginCacheGCIntentRow{}).Where("source_id = ? AND digest = ? AND signer_fingerprint = ? AND claim_token = ? AND quarantine_id = ? AND status = ?", claim.SourceID, digest, claim.SignerFingerprint, claim.Token, claim.QuarantineID, "deleting").Updates(map[string]any{"quarantine_path": quarantinePath, "claim_expires_at": expires, "updated_at": now})
		if result.Error != nil || result.RowsAffected != 1 {
			return errors.New("package GC claim is stale")
		}
		result = tx.Model(&PluginDigestFenceRow{}).Where("digest = ? AND claim_token = ?", digest, claim.Token).Updates(map[string]any{"claim_expires_at": expires, "updated_at": now})
		if result.Error != nil || result.RowsAffected != 1 {
			return errors.New("package GC claim is stale")
		}
		return nil
	})
}

// WithPackageGCMutation serializes one cache filesystem mutation with digest
// takeover and acquisition. The callback runs only while the durable fence and
// intent both name the current renewable token/generation; holding the write
// transaction until it returns prevents an expired worker and its replacement
// from mutating or republishing the same digest concurrently.
func (s *GormStore) WithPackageGCMutation(ctx context.Context, claim marketplace.PackageGCClaim, mutation func() error) error {
	digest := strings.ToLower(strings.TrimSpace(claim.Digest))
	if !marketplace.IsDigest(digest) || claim.Token == "" || claim.QuarantineID == "" || mutation == nil {
		return errors.New("valid package GC mutation claim is required")
	}
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		now := time.Now().UTC()
		expires := now.Add(5 * time.Minute)
		var fence PluginDigestFenceRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("digest = ? AND claim_token = ? AND claim_expires_at > ?", digest, claim.Token, now).First(&fence).Error; err != nil {
			return errors.New("package GC claim is stale")
		}
		var intent PluginCacheGCIntentRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("source_id = ? AND digest = ? AND signer_fingerprint = ? AND claim_token = ? AND quarantine_id = ? AND status = ? AND claim_expires_at > ?", claim.SourceID, digest, claim.SignerFingerprint, claim.Token, claim.QuarantineID, "deleting", now).First(&intent).Error; err != nil {
			return errors.New("package GC claim is stale")
		}
		result := tx.Model(&PluginDigestFenceRow{}).Where("digest = ? AND claim_token = ?", digest, claim.Token).Updates(map[string]any{"claim_expires_at": expires, "updated_at": now})
		if result.Error != nil || result.RowsAffected != 1 {
			return errors.New("package GC claim is stale")
		}
		result = tx.Model(&PluginCacheGCIntentRow{}).Where("source_id = ? AND digest = ? AND signer_fingerprint = ? AND claim_token = ? AND quarantine_id = ?", claim.SourceID, digest, claim.SignerFingerprint, claim.Token, claim.QuarantineID).Updates(map[string]any{"claim_expires_at": expires, "updated_at": now})
		if result.Error != nil || result.RowsAffected != 1 {
			return errors.New("package GC claim is stale")
		}
		return mutation()
	})
}

func (s *GormStore) ListPackageGCIntents(ctx context.Context) ([]marketplace.PackageGCIntent, error) {
	var rows []PluginCacheGCIntentRow
	if err := s.db.WithContext(ctx).Order("source_id, digest, signer_fingerprint").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]marketplace.PackageGCIntent, 0, len(rows))
	for _, row := range rows {
		result = append(result, marketplace.PackageGCIntent{SourceID: row.SourceID, Digest: row.Digest, SignerFingerprint: row.SignerFingerprint})
	}
	return result, nil
}

func (s *GormStore) RecordPackageGCFailure(ctx context.Context, sourceID, digest, signerFingerprint, failure string) error {
	return s.db.WithContext(ctx).Model(&PluginCacheGCIntentRow{}).Where("source_id = ? AND digest = ? AND signer_fingerprint = ?", sourceID, digest, signerFingerprint).Updates(map[string]any{"status": "pending", "last_error": failure, "updated_at": time.Now().UTC()}).Error
}

func (s *GormStore) CompletePackageGC(ctx context.Context, claim marketplace.PackageGCClaim, failure string) error {
	digest := strings.ToLower(claim.Digest)
	if !marketplace.IsDigest(digest) || claim.Token == "" || claim.QuarantineID == "" {
		return errors.New("valid package GC claim is required")
	}
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		now := time.Now().UTC()
		var fence PluginDigestFenceRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("digest = ? AND claim_token = ? AND claim_expires_at > ?", digest, claim.Token, now).First(&fence).Error; err != nil {
			return errors.New("package GC claim is stale")
		}
		if failure != "" {
			result := tx.Model(&PluginCacheGCIntentRow{}).Where("source_id = ? AND digest = ? AND signer_fingerprint = ? AND claim_token = ? AND quarantine_id = ? AND claim_expires_at > ?", claim.SourceID, digest, claim.SignerFingerprint, claim.Token, claim.QuarantineID, now).Updates(map[string]any{"status": "pending", "claim_token": "", "claim_expires_at": time.Time{}, "last_error": failure, "updated_at": now})
			if result.Error != nil || result.RowsAffected != 1 {
				return errors.New("package GC claim is stale")
			}
			return tx.Model(&PluginDigestFenceRow{}).Where("digest = ? AND claim_token = ?", digest, claim.Token).Updates(map[string]any{"claim_token": "", "claim_expires_at": time.Time{}, "updated_at": now}).Error
		}
		result := tx.Where("source_id = ? AND digest = ? AND signer_fingerprint = ? AND status = ? AND claim_token = ? AND quarantine_id = ? AND claim_expires_at > ?", claim.SourceID, digest, claim.SignerFingerprint, "deleting", claim.Token, claim.QuarantineID, now).Delete(&PluginCacheGCIntentRow{})
		if result.Error != nil || result.RowsAffected != 1 {
			return errors.New("package GC claim is stale")
		}
		var identities []string
		if err := tx.Model(&PluginPackageRow{}).Where("source_id = ? AND digest = ? AND signature_fingerprint = ?", claim.SourceID, digest, claim.SignerFingerprint).Pluck("identity", &identities).Error; err != nil {
			return err
		}
		if len(identities) > 0 {
			if err := tx.Where("package_identity IN ?", identities).Delete(&PluginArtifactRow{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("source_id = ? AND digest = ? AND signature_fingerprint = ?", claim.SourceID, digest, claim.SignerFingerprint).Delete(&PluginPackageRow{}).Error; err != nil {
			return err
		}
		return tx.Model(&PluginDigestFenceRow{}).Where("digest = ? AND claim_token = ?", digest, claim.Token).Updates(map[string]any{"claim_token": "", "claim_expires_at": time.Time{}, "updated_at": now}).Error
	})
}

func (s *GormStore) CompleteMarketplaceSourceDeletion(ctx context.Context, sourceID string, failure string) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		if failure != "" {
			return tx.Model(&MarketplaceSourceDeletionRow{}).Where("source_id = ?", sourceID).Updates(map[string]any{"last_error": failure, "updated_at": time.Now().UTC()}).Error
		}
		var pending int64
		if err := tx.Model(&PluginCacheGCIntentRow{}).Where("source_id = ? AND deferred = ?", sourceID, false).Count(&pending).Error; err != nil {
			return err
		}
		if pending != 0 {
			return errors.New("marketplace source cache cleanup is still pending")
		}
		var directories int64
		if err := tx.Model(&MarketplaceDirectoryCleanupRow{}).Where("source_id = ?", sourceID).Count(&directories).Error; err != nil {
			return err
		}
		if directories != 0 {
			return errors.New("marketplace source directory cleanup is still pending")
		}
		if err := tx.Where("source_id = ?", sourceID).Delete(&MarketplaceSourceDeletionRow{}).Error; err != nil {
			return err
		}
		return tx.Where("id = ? AND deleting = ?", sourceID, true).Delete(&MarketplaceSourceRow{}).Error
	})
}

func lockPackageDigestFenceTx(tx *gorm.DB, digest string, now time.Time) (PluginDigestFenceRow, error) {
	row := PluginDigestFenceRow{Digest: digest, UpdatedAt: now}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return PluginDigestFenceRow{}, err
	}
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("digest = ?", digest).First(&row).Error; err != nil {
		return PluginDigestFenceRow{}, err
	}
	return row, nil
}

func schedulePackageGCTx(tx *gorm.DB, sourceID, digest, fingerprint string, now time.Time) error {
	digest = strings.ToLower(strings.TrimSpace(digest))
	if !marketplace.IsDigest(digest) {
		return errors.New("invalid package digest")
	}
	if _, err := lockPackageDigestFenceTx(tx, digest, now); err != nil {
		return err
	}
	fingerprint = strings.ToLower(strings.TrimSpace(fingerprint))
	var trust marketplace.SignatureTrust
	var source MarketplaceSourceRow
	if err := tx.Where("id = ?", sourceID).First(&source).Error; err == nil {
		if fingerprint == "" {
			fingerprint = strings.ToLower(strings.TrimSpace(source.SignerFingerprint))
		}
		candidate, trustErr := marketplaceSourceFromRow(source).SignatureTrust()
		if trustErr == nil && strings.EqualFold(candidate.Fingerprint, fingerprint) {
			trust = candidate
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if fingerprint != "" && !marketplace.IsDigest(fingerprint) {
		return errors.New("package GC source signer fingerprint is invalid")
	}
	if fingerprint != "" && trust.Fingerprint == "" {
		var pkg PluginPackageRow
		if err := tx.Where("source_id = ? AND digest = ? AND signature_fingerprint = ?", sourceID, digest, fingerprint).Order("identity").First(&pkg).Error; err == nil {
			trust = marketplace.SignatureTrust{SourceID: sourceID, SourceKind: pkg.SourceKind, KeyID: pkg.SignatureKeyID, PublicKey: pkg.SignaturePublicKey, Fingerprint: pkg.SignatureFingerprint}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
	}
	if fingerprint != "" && trust.Fingerprint == "" {
		var acquisition PluginPackageAcquisitionRow
		if err := tx.Where("source_id = ? AND digest = ? AND signature_fingerprint = ?", sourceID, digest, fingerprint).First(&acquisition).Error; err == nil {
			trust = marketplace.SignatureTrust{SourceID: sourceID, SourceKind: acquisition.SourceKind, KeyID: acquisition.SignatureKeyID, PublicKey: acquisition.SignaturePublicKey, Fingerprint: acquisition.SignatureFingerprint}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
	}
	if trust.Fingerprint != "" {
		if trust.SourceKind == "" || trust.KeyID == "" || trust.PublicKey == "" {
			trust = marketplace.SignatureTrust{}
		} else if err := marketplace.ValidateSignatureTrust(trust); err != nil {
			return err
		}
	}
	intent := PluginCacheGCIntentRow{SourceID: sourceID, Digest: digest, SignerFingerprint: fingerprint, SignerSourceKind: trust.SourceKind, SignerKeyID: trust.KeyID, SignerPublicKey: trust.PublicKey, Status: "pending", UpdatedAt: now}
	conflict := clause.OnConflict{Columns: []clause.Column{{Name: "source_id"}, {Name: "digest"}, {Name: "signer_fingerprint"}}, DoNothing: true}
	if trust.Fingerprint != "" {
		// A pre-upgrade intent may not yet carry the exact trust material needed
		// to authenticate a legacy digest-layout object after source deletion.
		// Scheduling the same immutable signer variant safely fills that gap
		// without changing claim, object, or lease ownership.
		conflict = clause.OnConflict{
			Columns:   conflict.Columns,
			DoUpdates: clause.AssignmentColumns([]string{"signer_source_kind", "signer_key_id", "signer_public_key"}),
		}
	}
	return tx.Clauses(conflict).Create(&intent).Error
}

func (s *GormStore) AcquireRefreshLease(ctx context.Context, operation marketplace.RefreshOperation) error {
	if operation.ID == "" || operation.SourceID == "" || operation.LeaseToken == "" || !operation.LeaseExpiresAt.After(operation.StartedAt) || operation.Status != "running" {
		return errors.New("valid marketplace refresh lease is required")
	}
	if operation.SourceRevision == 0 ||
		(operation.RefKind != marketplace.GitRefKindBranch && operation.RefKind != marketplace.GitRefKindTag) || strings.TrimSpace(operation.RefName) == "" {
		return errors.New("complete marketplace source generation is required")
	}
	if strings.TrimSpace(operation.Actor.ActorID) == "" || strings.TrimSpace(operation.Actor.CorrelationID) == "" {
		return errors.New("complete marketplace refresh actor provenance is required")
	}
	if _, err := refreshOperationSignatureTrust(operation); err != nil {
		return err
	}
	row := marketplaceRefreshOperationToRow(operation)
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var source MarketplaceSourceRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND deleting = ?", operation.SourceID, false).First(&source).Error; err != nil {
			return err
		}
		if source.ConfigRevision != operation.SourceRevision || source.RefKind != operation.RefKind || source.RefName != operation.RefName {
			return fmt.Errorf("%w: expected revision %d %s %q, found revision %d %s %q", marketplace.ErrSourceGenerationChanged, operation.SourceRevision, operation.RefKind, operation.RefName, source.ConfigRevision, source.RefKind, source.RefName)
		}
		trust, err := marketplaceSourceFromRow(source).SignatureTrust()
		if err != nil {
			return fmt.Errorf("refresh source signer provenance: %w", err)
		}
		if row.SignerSourceKind != trust.SourceKind || row.SignerKeyID != trust.KeyID || row.SignerPublicKey != trust.PublicKey || !strings.EqualFold(row.SignerFingerprint, trust.Fingerprint) {
			return fmt.Errorf("%w: refresh attempt signer differs from the locked source", marketplace.ErrSourceGenerationChanged)
		}
		if source.RefreshLeaseToken != "" && source.RefreshLeaseExpiresAt.After(operation.StartedAt) {
			return marketplace.ErrRefreshLeaseHeld
		}
		result := tx.Model(&MarketplaceSourceRow{}).
			Where("id = ? AND deleting = ? AND config_revision = ? AND ref_kind = ? AND ref_name = ?", operation.SourceID, false, operation.SourceRevision, operation.RefKind, operation.RefName).
			Updates(map[string]any{"refresh_lease_token": operation.LeaseToken, "refresh_lease_expires_at": operation.LeaseExpiresAt, "last_result": "running", "last_error": "", "updated_at": operation.StartedAt})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return marketplace.ErrSourceGenerationChanged
		}
		if err := validateMarketplaceRefreshAuditProvenance(row); err != nil {
			return err
		}
		var interrupted []MarketplaceRefreshOperationRow
		if err := tx.Where("source_id = ? AND status = ? AND lease_expires_at <= ?", operation.SourceID, "running", operation.StartedAt).Order("id").Find(&interrupted).Error; err != nil {
			return err
		}
		type abandonedPackage struct {
			operation MarketplaceRefreshOperationRow
			staging   PluginPackageStagingRow
		}
		abandonedPackages := make([]abandonedPackage, 0)
		for _, previous := range interrupted {
			previous.Status, previous.ErrorClass, previous.Error, previous.FinishedAt = "failed", "interrupted", "refresh lease expired before completion", &operation.StartedAt
			audit, err := marketplaceRefreshAudit(previous, "failure", operation.StartedAt)
			if err != nil {
				previous.ErrorClass, previous.Error = "provenance_incomplete", "expired legacy refresh operation has incomplete immutable provenance"
				audit = marketplaceIncompleteRefreshAudit(previous, operation.StartedAt)
			}
			if err := tx.Model(&MarketplaceRefreshOperationRow{}).Where("id = ? AND status = ?", previous.ID, "running").Updates(map[string]any{"status": previous.Status, "error_class": previous.ErrorClass, "error": previous.Error, "finished_at": operation.StartedAt}).Error; err != nil {
				return err
			}
			if err := tx.Create(&audit).Error; err != nil {
				return err
			}
			var abandoned []PluginPackageStagingRow
			if err := tx.Where("source_id = ? AND operation_id = ?", previous.SourceID, previous.ID).Find(&abandoned).Error; err != nil {
				return err
			}
			for _, row := range abandoned {
				abandonedPackages = append(abandonedPackages, abandonedPackage{operation: previous, staging: row})
			}
		}
		sort.Slice(abandonedPackages, func(i, j int) bool {
			left, right := abandonedPackages[i], abandonedPackages[j]
			if left.staging.Digest != right.staging.Digest {
				return left.staging.Digest < right.staging.Digest
			}
			if left.staging.SignerFingerprint != right.staging.SignerFingerprint {
				return left.staging.SignerFingerprint < right.staging.SignerFingerprint
			}
			if left.operation.SourceID != right.operation.SourceID {
				return left.operation.SourceID < right.operation.SourceID
			}
			return left.operation.ID < right.operation.ID
		})
		for _, abandoned := range abandonedPackages {
			if err := schedulePackageGCTx(tx, abandoned.operation.SourceID, abandoned.staging.Digest, abandoned.staging.SignerFingerprint, operation.StartedAt); err != nil {
				return err
			}
		}
		for _, previous := range interrupted {
			if err := tx.Where("source_id = ? AND operation_id = ?", previous.SourceID, previous.ID).Delete(&PluginPackageStagingRow{}).Error; err != nil {
				return err
			}
		}
		return tx.Create(&row).Error
	})
}

func (s *GormStore) RenewRefreshLease(ctx context.Context, operation marketplace.RefreshOperation) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		now := time.Now().UTC()
		var source MarketplaceSourceRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND deleting = ?", operation.SourceID, false).First(&source).Error; err != nil {
			return errors.New("refresh lease is stale or source is deleting")
		}
		var lockedOperation MarketplaceRefreshOperationRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND source_id = ? AND lease_token = ? AND status = ?", operation.ID, operation.SourceID, operation.LeaseToken, "running").First(&lockedOperation).Error; err != nil {
			return errors.New("refresh operation lease is stale")
		}
		if err := validateRefreshOperationCallerBinding(operation, lockedOperation); err != nil {
			return err
		}
		if err := validateRefreshOperationSourceBinding(lockedOperation, source); err != nil {
			return err
		}
		if source.RefreshLeaseToken != lockedOperation.LeaseToken || !source.RefreshLeaseExpiresAt.After(now) || !lockedOperation.LeaseExpiresAt.After(now) {
			return errors.New("refresh lease is stale or source generation changed")
		}
		result := tx.Model(&MarketplaceSourceRow{}).Where("id = ? AND config_revision = ? AND ref_kind = ? AND ref_name = ? AND refresh_lease_token = ?", source.ID, lockedOperation.SourceRevision, lockedOperation.RefKind, lockedOperation.RefName, lockedOperation.LeaseToken).Update("refresh_lease_expires_at", operation.LeaseExpiresAt)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("refresh lease is stale or source generation changed")
		}
		result = tx.Model(&MarketplaceRefreshOperationRow{}).Where("id = ? AND status = ? AND lease_token = ?", lockedOperation.ID, "running", lockedOperation.LeaseToken).Update("lease_expires_at", operation.LeaseExpiresAt)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("refresh operation lease is stale")
		}
		return nil
	})
}

func (s *GormStore) StagePackageAcquisition(ctx context.Context, sourceID, digest, operationID string, trust marketplace.SignatureTrust) error {
	digest = strings.ToLower(strings.TrimSpace(digest))
	if !marketplace.IsDigest(digest) || strings.TrimSpace(operationID) == "" || marketplace.ValidateSignatureTrust(trust) != nil || trust.SourceID != sourceID {
		return errors.New("valid package digest and refresh operation are required")
	}
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		now := time.Now().UTC()
		operation, err := lockPackageAcquisitionTx(tx, sourceID, digest, operationID, trust, now)
		if err != nil {
			return err
		}
		row := PluginPackageStagingRow{SourceID: sourceID, Digest: digest, OperationID: operationID, SignerFingerprint: operation.SignerFingerprint, UpdatedAt: now}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error
	})
}

// PublishPackageAcquisition keeps the durable staging reservation and cache
// publication under the same source, operation, and digest locks. A source
// edit, deletion, lease takeover, startup reconciliation, or GC mutation can
// proceed only before publication begins or after the callback has returned.
func (s *GormStore) PublishPackageAcquisition(ctx context.Context, sourceID, digest, operationID string, trust marketplace.SignatureTrust, publish func() error) error {
	digest = strings.ToLower(strings.TrimSpace(digest))
	if !marketplace.IsDigest(digest) || strings.TrimSpace(operationID) == "" || marketplace.ValidateSignatureTrust(trust) != nil || trust.SourceID != sourceID || publish == nil {
		return errors.New("valid package publication is required")
	}
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		now := time.Now().UTC()
		operation, err := lockPackageAcquisitionTx(tx, sourceID, digest, operationID, trust, now)
		if err != nil {
			return err
		}
		var staged PluginPackageStagingRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("source_id = ? AND operation_id = ? AND digest = ?", sourceID, operationID, digest).First(&staged).Error; err != nil {
			return errors.New("package publication reservation is unavailable")
		}
		if !strings.EqualFold(staged.SignerFingerprint, operation.SignerFingerprint) {
			return errors.New("package publication signer differs from refresh operation")
		}
		if err := publish(); err != nil {
			return err
		}
		result := tx.Model(&PluginPackageStagingRow{}).
			Where("source_id = ? AND operation_id = ? AND digest = ? AND signer_fingerprint = ?", sourceID, operationID, digest, staged.SignerFingerprint).
			Update("updated_at", time.Now().UTC())
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("%w: package publication reservation changed", ErrPluginConflict)
		}
		return nil
	})
}

func lockPackageAcquisitionTx(tx *gorm.DB, sourceID, digest, operationID string, trust marketplace.SignatureTrust, now time.Time) (MarketplaceRefreshOperationRow, error) {
	var source MarketplaceSourceRow
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND deleting = ?", sourceID, false).First(&source).Error; err != nil {
		return MarketplaceRefreshOperationRow{}, err
	}
	var operation MarketplaceRefreshOperationRow
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND source_id = ? AND status = ? AND lease_expires_at > ?", operationID, sourceID, "running", now).First(&operation).Error; err != nil {
		return MarketplaceRefreshOperationRow{}, errors.New("refresh operation is unavailable or expired")
	}
	if err := validateRefreshOperationSourceBinding(operation, source); err != nil {
		return MarketplaceRefreshOperationRow{}, err
	}
	if source.RefreshLeaseToken != operation.LeaseToken || !source.RefreshLeaseExpiresAt.After(now) || trust.KeyID != operation.SignerKeyID || trust.PublicKey != operation.SignerPublicKey || trust.SourceKind != operation.SignerSourceKind || !strings.EqualFold(trust.Fingerprint, operation.SignerFingerprint) {
		return MarketplaceRefreshOperationRow{}, errors.New("refresh operation signer or source generation changed")
	}
	fence, err := lockPackageDigestFenceTx(tx, digest, now)
	if err != nil {
		return MarketplaceRefreshOperationRow{}, err
	}
	if fence.ClaimToken != "" {
		return MarketplaceRefreshOperationRow{}, errors.New("package cache digest is being deleted")
	}
	return operation, nil
}

func (s *GormStore) CompletePackageAcquisitions(ctx context.Context, sourceID, operationID string, succeeded bool) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var source MarketplaceSourceRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", sourceID).First(&source).Error; err != nil {
			return err
		}
		var operation MarketplaceRefreshOperationRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("source_id = ? AND id = ?", sourceID, operationID).First(&operation).Error; err != nil {
			return err
		}
		if succeeded {
			return errors.New("successful package acquisition finalization belongs to snapshot promotion")
		}
		return cleanupRefreshStagingTx(tx, operation, time.Now().UTC(), true)
	})
}

func (s *GormStore) RecordRefreshRejection(ctx context.Context, operation marketplace.RefreshOperation, errorClass string) error {
	if operation.ID == "" || operation.SourceID == "" || operation.SourceRevision == 0 || operation.RefName == "" || operation.Actor.ActorID == "" || operation.Actor.CorrelationID == "" || strings.TrimSpace(errorClass) == "" {
		return errors.New("complete immutable refresh rejection attempt is required")
	}
	if _, err := refreshOperationSignatureTrust(operation); err != nil {
		return err
	}
	now := time.Now().UTC()
	row := marketplaceRefreshOperationToRow(operation)
	row.ErrorClass = errorClass
	audit, err := marketplaceRefreshAudit(row, "failure", now)
	if err != nil {
		return err
	}
	return s.AppendAuditEvent(ctx, audit)
}

func (s *GormStore) SaveRefreshOperation(ctx context.Context, operation marketplace.RefreshOperation) error {
	if operation.Status == "running" {
		if operation.LeaseToken == "" {
			return errors.New("refresh operation acquisition requires a durable lease token")
		}
		return s.AcquireRefreshLease(ctx, operation)
	}
	if operation.Status != "failed" {
		return errors.New("refresh operation finalization only accepts failed operations")
	}
	if operation.LeaseToken == "" {
		return errors.New("refresh failure finalization requires a durable lease token")
	}
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		updatedAt := operation.StartedAt
		if operation.FinishedAt != nil {
			updatedAt = *operation.FinishedAt
		}
		sourceUpdates := map[string]any{"last_result": operation.Status, "last_error": operation.Error, "updated_at": updatedAt}
		if operation.FinishedAt != nil {
			sourceUpdates["last_completed_at"] = *operation.FinishedAt
		}
		var lockedSource MarketplaceSourceRow
		sourceFound := true
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", operation.SourceID).First(&lockedSource).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			sourceFound = false
		}
		var lockedOperation MarketplaceRefreshOperationRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", operation.ID).First(&lockedOperation).Error; err != nil {
			return errors.New("refresh operation lease is stale or already completed")
		}
		controlPlaneNow := time.Now().UTC()
		if err := validateRefreshOperationFinalization(operation, lockedOperation, controlPlaneNow); err != nil {
			return err
		}
		if sourceFound {
			if err := validateRefreshOperationSourceBinding(lockedOperation, lockedSource); err != nil {
				return err
			}
		}
		if !lockedOperation.LeaseExpiresAt.After(controlPlaneNow) {
			return errors.New("refresh operation lease expired")
		}
		if sourceFound && (lockedSource.ID != lockedOperation.SourceID || lockedSource.RefreshLeaseToken != lockedOperation.LeaseToken || !lockedSource.RefreshLeaseExpiresAt.After(controlPlaneNow)) {
			return errors.New("refresh source lease is stale")
		}
		result := tx.Model(&MarketplaceRefreshOperationRow{}).Where("id = ? AND source_id = ? AND lease_token = ? AND status = ?", operation.ID, operation.SourceID, operation.LeaseToken, "running").Updates(map[string]any{"commit": operation.Commit, "status": operation.Status, "error_class": operation.ErrorClass, "error": operation.Error, "diff_json": pluginDefaultJSON(operation.DiffJSON), "finished_at": operation.FinishedAt})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("refresh operation lease is stale or already completed")
		}
		if sourceFound {
			sourceUpdates["refresh_lease_token"] = ""
			sourceUpdates["refresh_lease_expires_at"] = time.Time{}
			result = tx.Model(&MarketplaceSourceRow{}).Where("id = ? AND refresh_lease_token = ?", lockedSource.ID, lockedOperation.LeaseToken).Updates(sourceUpdates)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return errors.New("refresh source lease is stale")
			}
		}
		lockedOperation.Commit, lockedOperation.Status, lockedOperation.ErrorClass, lockedOperation.Error = operation.Commit, operation.Status, operation.ErrorClass, operation.Error
		lockedOperation.DiffJSON, lockedOperation.FinishedAt = pluginDefaultJSON(operation.DiffJSON), operation.FinishedAt
		audit, err := marketplaceRefreshAudit(lockedOperation, "failure", updatedAt)
		if err != nil {
			return err
		}
		return tx.Create(&audit).Error
	})
}

func validateRefreshOperationFinalization(operation marketplace.RefreshOperation, locked MarketplaceRefreshOperationRow, controlPlaneNow time.Time) error {
	if locked.Status != "running" || operation.ID != locked.ID || operation.SourceID != locked.SourceID || operation.LeaseToken != locked.LeaseToken || operation.Actor != marketplaceRefreshOperationFromRow(locked).Actor {
		return errors.New("refresh operation identity differs from its durable lease")
	}
	if err := validateRefreshOperationCallerBinding(operation, locked); err != nil {
		return err
	}
	if locked.Commit != "" && operation.Commit != locked.Commit {
		return errors.New("refresh operation commit differs from its durable lease")
	}
	if operation.FinishedAt == nil || operation.FinishedAt.Before(locked.StartedAt) || operation.FinishedAt.After(controlPlaneNow) {
		return errors.New("refresh operation completion time is outside its trusted control-plane window")
	}
	return nil
}

func validateRefreshOperationCallerBinding(operation marketplace.RefreshOperation, locked MarketplaceRefreshOperationRow) error {
	if operation.SourceRevision != locked.SourceRevision || operation.RefKind != locked.RefKind || operation.RefName != locked.RefName || operation.SignerSourceKind != locked.SignerSourceKind || operation.SignerKeyID != locked.SignerKeyID || operation.SignerPublicKey != locked.SignerPublicKey || !strings.EqualFold(operation.SignerFingerprint, locked.SignerFingerprint) {
		return fmt.Errorf("%w: refresh operation source generation differs from its durable lease", marketplace.ErrSourceGenerationChanged)
	}
	return nil
}

func refreshOperationSignatureTrust(operation marketplace.RefreshOperation) (marketplace.SignatureTrust, error) {
	trust := marketplace.SignatureTrust{
		SourceID: operation.SourceID, SourceKind: operation.SignerSourceKind, KeyID: operation.SignerKeyID,
		PublicKey: operation.SignerPublicKey, Fingerprint: operation.SignerFingerprint,
	}
	if err := marketplace.ValidateSignatureTrust(trust); err != nil {
		return marketplace.SignatureTrust{}, fmt.Errorf("refresh operation signer provenance is incomplete: %w", err)
	}
	return trust, nil
}

func validateRefreshOperationSourceBinding(operation MarketplaceRefreshOperationRow, source MarketplaceSourceRow) error {
	if err := validateMarketplaceRefreshAuditProvenance(operation); err != nil {
		return err
	}
	if operation.SourceID != source.ID || operation.SourceRevision != source.ConfigRevision || operation.RefKind != source.RefKind || operation.RefName != source.RefName {
		return fmt.Errorf("%w: refresh operation source generation differs from the locked source", marketplace.ErrSourceGenerationChanged)
	}
	trust, err := marketplaceSourceFromRow(source).SignatureTrust()
	if err != nil {
		return fmt.Errorf("refresh source signer provenance: %w", err)
	}
	if operation.SignerSourceKind != trust.SourceKind || operation.SignerKeyID != trust.KeyID || operation.SignerPublicKey != trust.PublicKey || !strings.EqualFold(operation.SignerFingerprint, trust.Fingerprint) {
		return fmt.Errorf("%w: refresh operation signer differs from the locked source", marketplace.ErrSourceGenerationChanged)
	}
	return nil
}

func (s *GormStore) AbandonMarketplaceRefresh(ctx context.Context, sourceID, operationID, leaseToken, errorClass string) error {
	if operationID == "" || leaseToken == "" {
		return nil
	}
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		now := time.Now().UTC()
		var source MarketplaceSourceRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", sourceID).First(&source).Error; err != nil {
			return err
		}
		var operation MarketplaceRefreshOperationRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND source_id = ? AND lease_token = ?", operationID, sourceID, leaseToken).First(&operation).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if operation.Status != "running" {
			return cleanupRefreshStagingTx(tx, operation, now, true)
		}
		if err := validateRefreshOperationSourceBinding(operation, source); err != nil {
			return err
		}
		if source.RefreshLeaseToken != leaseToken {
			return errors.New("refresh source lease is stale")
		}
		message := "refresh abandoned after scheduler timeout"
		if err := tx.Model(&MarketplaceRefreshOperationRow{}).Where("id = ? AND status = ?", operation.ID, "running").Updates(map[string]any{"status": "failed", "error_class": errorClass, "error": message, "finished_at": now, "lease_expires_at": now}).Error; err != nil {
			return err
		}
		if err := tx.Model(&MarketplaceSourceRow{}).Where("id = ? AND refresh_lease_token = ?", sourceID, operation.LeaseToken).Updates(map[string]any{"refresh_lease_token": "", "refresh_lease_expires_at": time.Time{}, "last_result": "failed", "last_error": message, "updated_at": now, "last_completed_at": now}).Error; err != nil {
			return err
		}
		if err := cleanupRefreshStagingTx(tx, operation, now, true); err != nil {
			return err
		}
		operation.Status, operation.ErrorClass, operation.Error, operation.FinishedAt = "failed", errorClass, message, &now
		audit, err := marketplaceRefreshAudit(operation, "failure", now)
		if err != nil {
			return err
		}
		return tx.Create(&audit).Error
	})
}

func cleanupRefreshStagingTx(tx *gorm.DB, operation MarketplaceRefreshOperationRow, now time.Time, remove bool) error {
	var staged []PluginPackageStagingRow
	if err := tx.Where("source_id = ? AND operation_id = ?", operation.SourceID, operation.ID).Order("digest, signer_fingerprint").Find(&staged).Error; err != nil {
		return err
	}
	for _, row := range staged {
		if !strings.EqualFold(row.SignerFingerprint, operation.SignerFingerprint) {
			return fmt.Errorf("%w: staged package signer differs from refresh operation", ErrPluginConflict)
		}
		if err := scheduleRefreshOperationPackageGCTx(tx, operation, row.Digest, now); err != nil {
			return err
		}
	}
	if remove {
		return tx.Where("source_id = ? AND operation_id = ?", operation.SourceID, operation.ID).Delete(&PluginPackageStagingRow{}).Error
	}
	return nil
}

func scheduleRefreshOperationPackageGCTx(tx *gorm.DB, operation MarketplaceRefreshOperationRow, digest string, now time.Time) error {
	trust, err := refreshOperationRowSignatureTrust(operation)
	if err != nil {
		return err
	}
	if err := schedulePackageGCTx(tx, operation.SourceID, digest, operation.SignerFingerprint, now); err != nil {
		return err
	}
	return tx.Model(&PluginCacheGCIntentRow{}).
		Where("source_id = ? AND digest = ? AND signer_fingerprint = ?", operation.SourceID, digest, operation.SignerFingerprint).
		Updates(map[string]any{"signer_source_kind": trust.SourceKind, "signer_key_id": trust.KeyID, "signer_public_key": trust.PublicKey}).Error
}

func refreshOperationRowSignatureTrust(operation MarketplaceRefreshOperationRow) (marketplace.SignatureTrust, error) {
	trust := marketplace.SignatureTrust{
		SourceID: operation.SourceID, SourceKind: operation.SignerSourceKind, KeyID: operation.SignerKeyID,
		PublicKey: operation.SignerPublicKey, Fingerprint: operation.SignerFingerprint,
	}
	if err := marketplace.ValidateSignatureTrust(trust); err != nil {
		return marketplace.SignatureTrust{}, fmt.Errorf("refresh operation signer provenance is incomplete: %w", err)
	}
	return trust, nil
}

// reconcileTerminalMarketplaceRefreshStaging closes the crash gap between a
// terminal refresh operation and its worker-owned staging cleanup. A running
// operation marker is deliberately retained: the worker may have reserved the
// digest before publishing the verified cache object. Terminal markers have no
// live publisher after restart, so their immutable operation trust is used to
// make GC durable before the marker is removed.
func reconcileTerminalMarketplaceRefreshStaging(ctx context.Context, db *gorm.DB) error {
	type operationKey struct {
		SourceID    string
		OperationID string
	}
	var keys []operationKey
	if err := db.WithContext(ctx).Model(&PluginPackageStagingRow{}).
		Select("source_id, operation_id").Group("source_id, operation_id").Order("source_id, operation_id").Scan(&keys).Error; err != nil {
		return err
	}
	for _, key := range keys {
		if strings.TrimSpace(key.SourceID) == "" || strings.TrimSpace(key.OperationID) == "" {
			return errors.New("marketplace refresh staging ownership is incomplete")
		}
		if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var source MarketplaceSourceRow
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", key.SourceID).First(&source).Error; err != nil {
				return fmt.Errorf("marketplace refresh staging source is unavailable: %w", err)
			}
			var operation MarketplaceRefreshOperationRow
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND source_id = ?", key.OperationID, key.SourceID).First(&operation).Error; err != nil {
				return fmt.Errorf("marketplace refresh staging operation is unavailable: %w", err)
			}
			if err := validateMarketplaceRefreshAuditProvenance(operation); err != nil {
				return err
			}
			if _, err := refreshOperationRowSignatureTrust(operation); err != nil {
				return err
			}
			var staged []PluginPackageStagingRow
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("source_id = ? AND operation_id = ?", key.SourceID, key.OperationID).Order("digest, signer_fingerprint").Find(&staged).Error; err != nil {
				return err
			}
			for _, row := range staged {
				if !marketplace.IsDigest(row.Digest) || !strings.EqualFold(row.SignerFingerprint, operation.SignerFingerprint) {
					return errors.New("marketplace refresh staging signer or digest provenance is invalid")
				}
			}
			if operation.Status == "running" {
				return nil
			}
			if operation.Status != "failed" && operation.Status != "succeeded" {
				return fmt.Errorf("marketplace refresh staging operation has ambiguous terminal status %q", operation.Status)
			}
			now := time.Now().UTC()
			for _, row := range staged {
				if err := scheduleRefreshOperationPackageGCTx(tx, operation, row.Digest, now); err != nil {
					return err
				}
			}
			result := tx.Where("source_id = ? AND operation_id = ?", key.SourceID, key.OperationID).Delete(&PluginPackageStagingRow{})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != int64(len(staged)) {
				return fmt.Errorf("%w: marketplace refresh staging changed during reconciliation", ErrPluginConflict)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *GormStore) PromoteSnapshotAndCompleteRefresh(ctx context.Context, source marketplace.Source, snapshot marketplace.Snapshot, operation marketplace.RefreshOperation) error {
	if snapshot.SourceRevision == 0 {
		snapshot.SourceRevision, snapshot.RefKind, snapshot.RefName = source.ConfigRevision, source.RefKind, source.RefName
	}
	if operation.SourceRevision == 0 {
		operation.SourceRevision, operation.RefKind, operation.RefName = source.ConfigRevision, source.RefKind, source.RefName
	}
	if err := marketplace.ValidateSource(source); err != nil {
		return err
	}
	entriesJSON, err := json.Marshal(snapshot.Entries)
	if err != nil {
		return err
	}
	directJSON := ""
	if snapshot.DirectPlugin != nil {
		encoded, encodeErr := json.Marshal(snapshot.DirectPlugin)
		if encodeErr != nil {
			return encodeErr
		}
		directJSON = string(encoded)
	}
	if (source.Purpose == marketplace.SourcePurposePlugin) != (snapshot.DirectPlugin != nil) || (snapshot.DirectPlugin != nil && len(snapshot.Entries) != 0) {
		return errors.New("marketplace source purpose does not match snapshot projection")
	}
	currentRelative, err := relativeMarketplaceSnapshotDirectoryPath(s.dataRoot, snapshot.Path)
	if err != nil {
		return err
	}
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		if operation.SourceID != source.ID || snapshot.SourceID != source.ID || operation.Commit != snapshot.Commit || operation.SourceRevision != source.ConfigRevision || snapshot.SourceRevision != source.ConfigRevision || operation.RefKind != source.RefKind || snapshot.RefKind != source.RefKind || operation.RefName != source.RefName || snapshot.RefName != source.RefName || operation.Status != "succeeded" || operation.LeaseToken == "" {
			return errors.New("completed refresh operation does not match promoted snapshot")
		}
		var lockedSource MarketplaceSourceRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", source.ID).First(&lockedSource).Error; err != nil {
			return errors.New("marketplace source was deleted or refresh lease expired")
		}
		var lockedOperation MarketplaceRefreshOperationRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", operation.ID).First(&lockedOperation).Error; err != nil {
			return errors.New("refresh operation is stale or already completed")
		}
		now := time.Now().UTC()
		if lockedSource.Deleting || lockedSource.RefreshLeaseToken != operation.LeaseToken || !lockedSource.RefreshLeaseExpiresAt.After(now) || !marketplaceSourcePromotionIdentityEqual(source, lockedSource) {
			return errors.New("marketplace source identity or refresh lease changed")
		}
		if err := validateRefreshOperationSourceBinding(lockedOperation, lockedSource); err != nil {
			return err
		}
		if err := validateRefreshOperationFinalization(operation, lockedOperation, now); err != nil {
			return err
		}
		if !lockedOperation.LeaseExpiresAt.After(now) {
			return errors.New("refresh operation lease expired")
		}
		var trust marketplace.SignatureTrust
		if len(snapshot.Entries) > 0 || snapshot.DirectPlugin != nil {
			trust, err = marketplaceSourceFromRow(lockedSource).SignatureTrust()
			if err != nil {
				return fmt.Errorf("derive promoted snapshot signer trust: %w", err)
			}
			for _, entry := range snapshot.Entries {
				if entry.SignatureKeyID != trust.KeyID {
					return errors.New("marketplace entry signer differs from its source-bound signer")
				}
			}
			if snapshot.DirectPlugin != nil && snapshot.DirectPlugin.SignatureKeyID != trust.KeyID {
				return errors.New("direct plugin signer differs from its source-bound signer")
			}
		}
		var provisional MarketplaceDirectoryCleanupRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("source_id = ? AND operation_id = ? AND path_digest = ? AND state = ?", lockedSource.ID, lockedOperation.ID, pluginStorageDigest(currentRelative), "provisional").First(&provisional).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%w: snapshot cleanup reservation is missing", ErrPluginConflict)
			} else {
				return err
			}
		}
		if provisional.Path != currentRelative || provisional.ClaimToken != "" {
			return fmt.Errorf("%w: snapshot cleanup reservation is claimed", ErrPluginConflict)
		}
		sourceUpdates := map[string]any{"current_snapshot_id": snapshot.ID, "last_result": "succeeded", "last_error": "", "updated_at": snapshot.ValidatedAt, "last_completed_at": *operation.FinishedAt, "refresh_lease_token": "", "refresh_lease_expires_at": time.Time{}}
		if marketplace.IsFullCommitOID(snapshot.Commit) {
			sourceUpdates["current_resolved_oid"] = snapshot.Commit
		}
		sourceResult := tx.Model(&MarketplaceSourceRow{}).Where("id = ? AND deleting = ? AND refresh_lease_token = ? AND refresh_lease_expires_at >= ?", lockedSource.ID, false, lockedOperation.LeaseToken, now).Updates(sourceUpdates)
		if sourceResult.Error != nil {
			return sourceResult.Error
		}
		if sourceResult.RowsAffected != 1 {
			return errors.New("marketplace source was deleted or refresh lease expired")
		}
		var retired []MarketSnapshotRow
		if err := tx.Where("source_id = ? AND id <> ?", lockedSource.ID, snapshot.ID).Find(&retired).Error; err != nil {
			return err
		}
		snapshotRow := MarketSnapshotRow{ID: snapshot.ID, SourceID: lockedSource.ID, Commit: snapshot.Commit, SourceRevision: snapshot.SourceRevision, RefKind: snapshot.RefKind, RefName: snapshot.RefName, Path: snapshot.Path, EntriesJSON: string(entriesJSON), DirectPluginJSON: directJSON, ValidatedAt: snapshot.ValidatedAt}
		if err := tx.Create(&snapshotRow).Error; err != nil {
			return err
		}
		result := tx.Where("id = ? AND claim_token = ?", provisional.ID, "").Delete(&MarketplaceDirectoryCleanupRow{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("%w: snapshot cleanup reservation changed", ErrPluginConflict)
		}
		var oldAcquisitions []PluginPackageAcquisitionRow
		if err := tx.Where("source_id = ?", lockedSource.ID).Order("digest, signature_fingerprint").Find(&oldAcquisitions).Error; err != nil {
			return err
		}
		for _, previous := range retired {
			relative, err := relativeMarketplaceSnapshotDirectoryPath(s.dataRoot, previous.Path)
			if err != nil {
				return err
			}
			if relative != currentRelative {
				work := MarketplaceDirectoryCleanupRow{ID: pluginStorageID("dirgc"), SourceID: lockedSource.ID, OperationID: lockedOperation.ID, Path: relative, State: "retired", UpdatedAt: snapshot.ValidatedAt}
				if err := ensureMarketplaceDirectoryCleanupTx(tx, work); err != nil {
					return err
				}
			}
			if err := tx.Where("snapshot_id = ?", previous.ID).Delete(&MarketEntryRow{}).Error; err != nil {
				return err
			}
			if err := tx.Where("id = ?", previous.ID).Delete(&MarketSnapshotRow{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("source_id = ?", lockedSource.ID).Delete(&PluginPackageAcquisitionRow{}).Error; err != nil {
			return err
		}
		newDigests := make(map[string]struct{}, len(snapshot.Entries))
		for _, entry := range snapshot.Entries {
			capabilities, _ := json.Marshal(entry.Capabilities)
			compatibility, _ := json.Marshal(entry.Compatibility)
			artifacts, _ := json.Marshal(entry.Artifacts)
			row := MarketEntryRow{ID: pluginStorageID("entry"), SnapshotID: snapshot.ID, PluginID: entry.ID, Version: entry.Version, Description: entry.Description, CapabilitiesJSON: string(capabilities), CompatibilityJSON: string(compatibility), RuntimeKind: entry.Runtime.Kind, RuntimeABI: entry.Runtime.ABI, HostScope: entry.Runtime.HostScope, PolicyKind: entry.Runtime.PolicyKind, ArtifactsJSON: string(artifacts), PackagePath: entry.PackagePath, PackageDigest: strings.ToLower(entry.PackageSHA256), SignatureKeyID: entry.SignatureKeyID, Provenance: entry.Provenance, Official: entry.Official}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
			acquisition := PluginPackageAcquisitionRow{SourceID: lockedSource.ID, Digest: row.PackageDigest, SnapshotID: snapshot.ID, SourceRevision: snapshot.SourceRevision, RefKind: snapshot.RefKind, RefName: snapshot.RefName, ResolvedOID: snapshot.Commit, SourceKind: trust.SourceKind, SignatureKeyID: trust.KeyID, SignaturePublicKey: trust.PublicKey, SignatureFingerprint: trust.Fingerprint, Status: "catalog", UpdatedAt: snapshot.ValidatedAt}
			newDigests[row.PackageDigest] = struct{}{}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "source_id"}, {Name: "digest"}}, DoUpdates: clause.AssignmentColumns([]string{"snapshot_id", "source_revision", "ref_kind", "ref_name", "resolved_oid", "source_kind", "signature_key_id", "signature_public_key", "signature_fingerprint", "status", "updated_at"})}).Create(&acquisition).Error; err != nil {
				return err
			}
		}
		if direct := snapshot.DirectPlugin; direct != nil {
			acquisition := PluginPackageAcquisitionRow{SourceID: lockedSource.ID, Digest: strings.ToLower(direct.PackageSHA256), SnapshotID: snapshot.ID, SourceRevision: snapshot.SourceRevision, RefKind: snapshot.RefKind, RefName: snapshot.RefName, ResolvedOID: snapshot.Commit, SourceKind: trust.SourceKind, SignatureKeyID: trust.KeyID, SignaturePublicKey: trust.PublicKey, SignatureFingerprint: trust.Fingerprint, Status: "catalog", UpdatedAt: snapshot.ValidatedAt}
			newDigests[acquisition.Digest] = struct{}{}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "source_id"}, {Name: "digest"}}, DoUpdates: clause.AssignmentColumns([]string{"snapshot_id", "source_revision", "ref_kind", "ref_name", "resolved_oid", "source_kind", "signature_key_id", "signature_public_key", "signature_fingerprint", "status", "updated_at"})}).Create(&acquisition).Error; err != nil {
				return err
			}
		}
		for _, acquisition := range oldAcquisitions {
			if _, retained := newDigests[acquisition.Digest]; !retained {
				if err := schedulePackageGCTx(tx, lockedSource.ID, acquisition.Digest, acquisition.SignatureFingerprint, snapshot.ValidatedAt); err != nil {
					return err
				}
			}
		}
		if err := tx.Where("source_id = ? AND operation_id = ?", lockedSource.ID, lockedOperation.ID).Delete(&PluginPackageStagingRow{}).Error; err != nil {
			return err
		}
		result = tx.Model(&MarketplaceRefreshOperationRow{}).
			Where("id = ? AND source_id = ? AND lease_token = ? AND status = ?", lockedOperation.ID, lockedSource.ID, lockedOperation.LeaseToken, "running").
			Updates(map[string]any{"commit": operation.Commit, "status": operation.Status, "error_class": "", "error": "", "diff_json": pluginDefaultJSON(operation.DiffJSON), "finished_at": operation.FinishedAt})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("refresh operation is stale or already completed")
		}
		lockedOperation.Commit, lockedOperation.Status, lockedOperation.ErrorClass, lockedOperation.Error = operation.Commit, operation.Status, "", ""
		lockedOperation.DiffJSON, lockedOperation.FinishedAt = pluginDefaultJSON(operation.DiffJSON), operation.FinishedAt
		audit, err := marketplaceRefreshAudit(lockedOperation, "success", *operation.FinishedAt)
		if err != nil {
			return err
		}
		return tx.Create(&audit).Error
	})
}

func marketplaceSourcePromotionIdentityEqual(source marketplace.Source, locked MarketplaceSourceRow) bool {
	return source.ID == locked.ID && source.Kind == locked.Kind && source.Purpose == locked.Purpose && source.Name == locked.Name && source.URL == locked.URL && source.RefKind == locked.RefKind && source.RefName == locked.RefName && source.ConfigRevision == locked.ConfigRevision &&
		source.CredentialRef == locked.CredentialRef && source.SignerKeyID == locked.SignerKeyID && source.SignerSecretRef == locked.SignerSecretRef &&
		source.SignerPublicKey == locked.SignerPublicKey && source.SignerFingerprint == locked.SignerFingerprint && int64(source.RefreshInterval) == locked.RefreshIntervalNS && source.RiskLabel == locked.RiskLabel
}

// PromoteSnapshot is retained for internal fixtures that seed a catalog. Real
// refreshes must use PromoteSnapshotAndCompleteRefresh.
func (s *GormStore) PromoteSnapshot(ctx context.Context, source marketplace.Source, snapshot marketplace.Snapshot) error {
	now := snapshot.ValidatedAt
	if snapshot.SourceRevision == 0 {
		snapshot.SourceRevision, snapshot.RefKind, snapshot.RefName = source.ConfigRevision, source.RefKind, source.RefName
	}
	trust, err := source.SignatureTrust()
	if err != nil {
		return err
	}
	op := marketplace.RefreshOperation{
		ID: pluginStorageID("refresh"), SourceID: source.ID, Commit: snapshot.Commit, SourceRevision: snapshot.SourceRevision, RefKind: snapshot.RefKind, RefName: snapshot.RefName,
		SignerSourceKind: trust.SourceKind, SignerKeyID: trust.KeyID, SignerPublicKey: trust.PublicKey, SignerFingerprint: trust.Fingerprint,
		Status: "running", StartedAt: now, LeaseToken: pluginStorageID("lease"), LeaseExpiresAt: now.Add(time.Minute),
		Actor: marketplace.OperationActor{ActorID: "system.marketplace.fixture", SessionID: "internal", CorrelationID: pluginStorageID("request")},
	}
	if _, ok, err := s.GetMarketplaceSource(ctx, source.ID); err != nil {
		return err
	} else if !ok {
		if err := s.SaveMarketplaceSource(ctx, source); err != nil {
			return err
		}
	}
	if err := s.AcquireRefreshLease(ctx, op); err != nil {
		return err
	}
	reservationPath := snapshot.Path
	if !filepath.IsAbs(reservationPath) {
		reservationPath = filepath.Join(s.dataRoot, "marketplace", "snapshots", filepath.FromSlash(reservationPath))
	}
	if err := s.RegisterMarketplaceDirectoryCleanup(ctx, source.ID, op.ID, []string{reservationPath}); err != nil {
		return err
	}
	op.Status, op.FinishedAt = "succeeded", &now
	return s.PromoteSnapshotAndCompleteRefresh(ctx, source, snapshot, op)
}

func (s *GormStore) CurrentSnapshot(ctx context.Context, sourceID string) (marketplace.Snapshot, bool, error) {
	_, snapshot, found, err := s.CurrentMarketplaceCatalog(ctx, sourceID)
	return snapshot, found, err
}

// CurrentMarketplaceCatalog returns the source projection and its current
// snapshot from one stable database view. This prevents an API response from
// pairing source generation N+1 with snapshot generation N during promotion.
func (s *GormStore) CurrentMarketplaceCatalog(ctx context.Context, sourceID string) (marketplace.Source, marketplace.Snapshot, bool, error) {
	var currentSource marketplace.Source
	var result marketplace.Snapshot
	var found bool
	err := s.readSnapshotTransaction(ctx, func(view *GormStore) error {
		var source MarketplaceSourceRow
		if err := view.db.WithContext(ctx).Where("id = ?", sourceID).First(&source).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		currentSource = marketplaceSourceFromRow(source)
		if source.CurrentSnapshotID == "" {
			return nil
		}
		var row MarketSnapshotRow
		if err := view.db.WithContext(ctx).Where("id = ? AND source_id = ?", source.CurrentSnapshotID, source.ID).First(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%w: current snapshot is missing", ErrMarketplaceCatalogStale)
			}
			return err
		}
		if err := validateCurrentMarketplaceSnapshot(source, row); err != nil {
			return err
		}
		var entries []plugins.MarketEntry
		if err := json.Unmarshal([]byte(row.EntriesJSON), &entries); err != nil {
			return err
		}
		var direct *plugins.DirectPluginSnapshot
		if strings.TrimSpace(row.DirectPluginJSON) != "" {
			direct = &plugins.DirectPluginSnapshot{}
			if err := json.Unmarshal([]byte(row.DirectPluginJSON), direct); err != nil {
				return err
			}
		}
		result = marketplace.Snapshot{ID: row.ID, SourceID: row.SourceID, Commit: row.Commit, SourceRevision: row.SourceRevision, RefKind: row.RefKind, RefName: row.RefName, Path: row.Path, ValidatedAt: row.ValidatedAt, Entries: entries, DirectPlugin: direct}
		found = true
		return nil
	})
	return currentSource, result, found, err
}

func validateCurrentMarketplaceSnapshot(source MarketplaceSourceRow, snapshot MarketSnapshotRow) error {
	if source.ConfigRevision == 0 || snapshot.SourceRevision == 0 || source.ConfigRevision != snapshot.SourceRevision ||
		source.RefKind != snapshot.RefKind || source.RefName != snapshot.RefName || !marketplace.IsFullCommitOID(source.CurrentResolvedOID) ||
		!marketplace.IsFullCommitOID(snapshot.Commit) || !strings.EqualFold(source.CurrentResolvedOID, snapshot.Commit) {
		return fmt.Errorf("%w: source revision, ref, or resolved OID differs from the current snapshot", ErrMarketplaceCatalogStale)
	}
	return nil
}

func (s *GormStore) CurrentMarketEntry(ctx context.Context, sourceID, pluginID, version, digest string) (plugins.MarketEntry, bool, error) {
	snapshot, ok, err := s.CurrentSnapshot(ctx, sourceID)
	if err != nil || !ok {
		return plugins.MarketEntry{}, false, err
	}
	for _, entry := range snapshot.Entries {
		if entry.ID == pluginID && entry.Version == version && strings.EqualFold(entry.PackageSHA256, digest) {
			return entry, true, nil
		}
	}
	return plugins.MarketEntry{}, false, nil
}

func (s *GormStore) CurrentDirectPlugin(ctx context.Context, sourceID, pluginID, version, digest string) (plugins.DirectPluginSnapshot, bool, error) {
	snapshot, ok, err := s.CurrentSnapshot(ctx, sourceID)
	if err != nil || !ok || snapshot.DirectPlugin == nil {
		return plugins.DirectPluginSnapshot{}, false, err
	}
	direct := *snapshot.DirectPlugin
	if direct.ID != pluginID || direct.Version != version || !strings.EqualFold(direct.PackageSHA256, digest) {
		return plugins.DirectPluginSnapshot{}, false, nil
	}
	return direct, true, nil
}

func (s *GormStore) CurrentPackageAcquisition(ctx context.Context, sourceID, digest string) (marketplace.PackageAcquisition, bool, error) {
	var result marketplace.PackageAcquisition
	var found bool
	err := s.readSnapshotTransaction(ctx, func(view *GormStore) error {
		var source MarketplaceSourceRow
		if err := view.db.WithContext(ctx).Where("id = ? AND deleting = ?", sourceID, false).First(&source).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if source.CurrentSnapshotID == "" {
			return nil
		}
		var snapshot MarketSnapshotRow
		if err := view.db.WithContext(ctx).Where("id = ? AND source_id = ?", source.CurrentSnapshotID, source.ID).First(&snapshot).Error; err != nil {
			return fmt.Errorf("%w: current snapshot is missing", ErrMarketplaceCatalogStale)
		}
		if err := validateCurrentMarketplaceSnapshot(source, snapshot); err != nil {
			return err
		}
		var row PluginPackageAcquisitionRow
		if err := view.db.WithContext(ctx).Where("source_id = ? AND digest = ? AND snapshot_id = ? AND status = ?", sourceID, strings.ToLower(strings.TrimSpace(digest)), source.CurrentSnapshotID, "catalog").First(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if row.SourceRevision == 0 || row.SourceRevision != source.ConfigRevision || row.RefKind != source.RefKind || row.RefName != source.RefName || !marketplace.IsFullCommitOID(row.ResolvedOID) || !strings.EqualFold(row.ResolvedOID, source.CurrentResolvedOID) {
			return fmt.Errorf("%w: package acquisition differs from the current source", ErrMarketplaceCatalogStale)
		}
		result = marketplace.PackageAcquisition{SourceID: row.SourceID, Digest: row.Digest, SnapshotID: row.SnapshotID, SourceRevision: row.SourceRevision, RefKind: row.RefKind, RefName: row.RefName, ResolvedOID: row.ResolvedOID, Trust: marketplace.SignatureTrust{SourceID: row.SourceID, SourceKind: row.SourceKind, KeyID: row.SignatureKeyID, PublicKey: row.SignaturePublicKey, Fingerprint: row.SignatureFingerprint}}
		if err := marketplace.ValidateSignatureTrust(result.Trust); err != nil {
			return err
		}
		found = true
		return nil
	})
	return result, found, err
}

func (s *GormStore) PackageReferenced(ctx context.Context, digest string) (bool, error) {
	var count int64
	err := s.db.WithContext(ctx).Model(&InstalledPluginRow{}).
		Where("active_package_digest = ? OR staged_package_digest = ? OR rollback_package_digest = ?", digest, digest, digest).
		Count(&count).Error
	return count > 0, err
}

type PluginInstallTransaction struct {
	Package             PluginPackageRow
	Artifacts           []PluginArtifactRow
	Installed           InstalledPluginRow
	Grants              []PluginGrantRow
	Operation           PluginOperationRow
	Audit               AuditEventRow
	RequireAcquisition  bool
	AcquisitionSourceID string
	AcquisitionDigest   string
}

func (s *GormStore) InstallPlugin(ctx context.Context, input PluginInstallTransaction) error {
	err := s.writeTransaction(ctx, func(tx *gorm.DB) error {
		normalizePluginGrantRows(input.Grants)
		if input.RequireAcquisition {
			if err := validatePackageAcquisitionTx(tx, input.AcquisitionSourceID, input.AcquisitionDigest, input.Package); err != nil {
				return err
			}
		}
		var count int64
		if err := tx.Model(&InstalledPluginRow{}).Where("plugin_id = ?", input.Installed.PluginID).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return ErrPluginAlreadyInstalled
		}
		if err := ensurePluginPackageTx(tx, input.Package, input.Artifacts); err != nil {
			return err
		}
		if err := tx.Create(&input.Installed).Error; err != nil {
			return err
		}
		if len(input.Grants) > 0 {
			for _, grant := range input.Grants {
				var retained PluginGrantRow
				err := tx.Where("grant_key = ? OR (plugin_id = ? AND package_digest = ? AND permission = ? AND resource_selector = ?)", grant.GrantKey, grant.PluginID, grant.PackageDigest, grant.Permission, grant.ResourceSelector).First(&retained).Error
				if err == nil {
					if err := tx.Model(&PluginGrantRow{}).Where("id = ?", retained.ID).Updates(map[string]any{"grant_key": grant.GrantKey, "granted_by": grant.GrantedBy, "granted_at": grant.GrantedAt}).Error; err != nil {
						return err
					}
					continue
				}
				if !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
				if err := tx.Create(&grant).Error; err != nil {
					return err
				}
			}
		}
		return createPluginOperationAndAudit(tx, input.Operation, input.Audit)
	})
	if isDuplicateKeyError(err) {
		return ErrPluginAlreadyInstalled
	}
	return err
}

type PluginMutation struct {
	PluginID                   string
	ExpectedActive             string
	ExpectedStateVersion       uint64
	ExpectedPendingOperationID string
	Installed                  *InstalledPluginRow
	Package                    *PluginPackageRow
	Artifacts                  []PluginArtifactRow
	ReplaceGrants              []PluginGrantRow
	ReplaceInstance            *PluginInstanceRow
	ReplaceInstances           []PluginInstanceRow
	DeleteInstanceID           string
	DeleteInstanceHTTPRules    bool
	ExpectedInstanceVersion    uint64
	DeletePlugin               bool
	DeleteInstances            bool
	DeleteGrants               bool
	Operation                  PluginOperationRow
	CompleteOperation          bool
	Audit                      AuditEventRow
	RequireAcquisition         bool
	AcquisitionSourceID        string
	AcquisitionDigest          string
	ValidateInstanceScope      bool
	PromoteInstanceBinding     bool
	SecretWrites               []PluginSecretWrite
}

type PluginSecretWrite struct {
	Secret  SecretRow
	Version SecretVersionRow
	Audit   AuditEventRow
}

func (s *GormStore) MigrateLegacyPluginInstanceSecrets(ctx context.Context, expectedStateVersion uint64, instance PluginInstanceRow, writes []PluginSecretWrite) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var current PluginInstanceRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND plugin_id = ?", instance.ID, instance.PluginID).First(&current).Error; err != nil {
			return err
		}
		if current.StateVersion != expectedStateVersion {
			return ErrPluginConflict
		}
		for _, write := range writes {
			if write.Secret.ID == "" || write.Version.SecretID != write.Secret.ID || write.Version.Version != write.Secret.ActiveVersion {
				return errors.New("legacy plugin secret migration identity is invalid")
			}
			if err := tx.Create(&write.Secret).Error; err != nil {
				return err
			}
			if err := tx.Create(&write.Version).Error; err != nil {
				return err
			}
			if err := tx.Create(&write.Audit).Error; err != nil {
				return err
			}
		}
		result := tx.Model(&PluginInstanceRow{}).Where("id = ? AND state_version = ?", instance.ID, expectedStateVersion).Updates(map[string]any{
			"config_json": instance.ConfigJSON, "secret_handles_json": instance.SecretHandlesJSON,
			"pending_config_json": instance.PendingConfigJSON, "pending_secret_handles_json": instance.PendingSecretHandlesJSON,
			"rollback_config_json": instance.RollbackConfigJSON, "rollback_secret_handles_json": instance.RollbackSecretHandlesJSON,
			"rollback_resource_group_id": instance.RollbackResourceGroupID,
			"state_version":              expectedStateVersion + 1, "updated_at": instance.UpdatedAt,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrPluginConflict
		}
		activeIDs := make(map[string]struct{})
		var activeHandles []PluginInstanceSecretHandle
		if err := json.Unmarshal([]byte(pluginDefaultArrayJSON(instance.SecretHandlesJSON)), &activeHandles); err != nil {
			return err
		}
		for _, handle := range activeHandles {
			activeIDs[handle.ID] = struct{}{}
		}
		migrationDigest := sha256.Sum256([]byte(instance.PluginID + "\x00" + instance.ID))
		operationID := "migration-" + fmt.Sprintf("%x", migrationDigest[:])[:54]
		for _, write := range writes {
			state := "staged"
			if _, active := activeIDs[write.Secret.ID]; active {
				state = "active"
			}
			row := PluginOperationSecretRow{OperationID: operationID, SecretID: write.Secret.ID, InstanceID: instance.ID, Role: state, State: state, CreatedAt: instance.UpdatedAt}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *GormStore) ApplyPluginMutation(ctx context.Context, mutation PluginMutation) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		scoped := s.transactionView(tx)
		agents, err := scoped.pluginMutationPolicyAgents(ctx, mutation)
		if err != nil {
			return err
		}
		if err := scoped.lockPluginPolicyAgentCatalogs(ctx, agents, mutation.Operation.CreatedAt); err != nil {
			return err
		}
		before, err := scoped.capturePluginPolicyCatalogs(ctx, agents)
		if err != nil {
			return err
		}
		if err := scoped.applyPluginMutationTx(ctx, tx, mutation); err != nil {
			return err
		}
		if err := scoped.validateProspectivePluginPolicyTransition(ctx, mutation, agents, before); err != nil {
			return err
		}
		return scoped.reconcilePluginPolicyCatalogRevisions(ctx, agents, before, mutation.Operation.CreatedAt)
	})
}

func (s *GormStore) applyPluginMutationTx(ctx context.Context, tx *gorm.DB, mutation PluginMutation) error {
	for _, write := range mutation.SecretWrites {
		if write.Secret.ID == "" || write.Version.SecretID != write.Secret.ID || write.Version.Version != write.Secret.ActiveVersion || write.Audit.TargetID != write.Secret.ID {
			return errors.New("plugin secret write identity is invalid")
		}
		if err := tx.Create(&write.Secret).Error; err != nil {
			return err
		}
		if err := tx.Create(&write.Version).Error; err != nil {
			return err
		}
		if err := tx.Create(&write.Audit).Error; err != nil {
			return err
		}
		ownership := PluginOperationSecretRow{OperationID: mutation.Operation.ID, SecretID: write.Secret.ID, InstanceID: pluginSecretInstanceID(write.Secret.Purpose), Role: "staged", State: "staged", CreatedAt: mutation.Operation.CreatedAt}
		if ownership.InstanceID == "" || ownership.OperationID == "" {
			return errors.New("plugin staged secret ownership is invalid")
		}
		if err := tx.Create(&ownership).Error; err != nil {
			return err
		}
	}
	if mutation.RequireAcquisition {
		if mutation.Package == nil {
			return errors.New("package acquisition promotion requires package metadata")
		}
		if err := validatePackageAcquisitionTx(tx, mutation.AcquisitionSourceID, mutation.AcquisitionDigest, *mutation.Package); err != nil {
			return err
		}
	}
	var current InstalledPluginRow
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("plugin_id = ?", mutation.PluginID).First(&current).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return ErrPluginNotInstalled
		}
		return err
	}
	secretCandidates, err := pluginSecretIDsForPluginTx(tx, mutation.PluginID)
	if err != nil {
		return err
	}
	for _, write := range mutation.SecretWrites {
		secretCandidates[write.Secret.ID] = pluginSecretInstanceID(write.Secret.Purpose)
	}
	operationScopes, err := pluginOperationScopesForPluginTx(tx, mutation.PluginID, mutation.Operation)
	if err != nil {
		return err
	}
	if mutation.ReplaceInstance != nil {
		operationScopes = mergePluginOperationScope(operationScopes, *mutation.ReplaceInstance, mutation.Operation)
	}
	for _, replacement := range mutation.ReplaceInstances {
		operationScopes = mergePluginOperationScope(operationScopes, replacement, mutation.Operation)
	}
	if mutation.ExpectedActive != "" && current.ActivePackageDigest != mutation.ExpectedActive {
		return fmt.Errorf("%w: active plugin package changed concurrently", ErrPluginConflict)
	}
	if mutation.ExpectedStateVersion != 0 && current.StateVersion != mutation.ExpectedStateVersion {
		return fmt.Errorf("%w: plugin state changed concurrently", ErrPluginConflict)
	}
	if mutation.ExpectedPendingOperationID != "" && current.PendingOperationID != mutation.ExpectedPendingOperationID {
		return fmt.Errorf("%w: plugin operation is stale or out of order", ErrPluginConflict)
	}
	if mutation.Package != nil {
		if err := ensurePluginPackageTx(tx, *mutation.Package, mutation.Artifacts); err != nil {
			return err
		}
	}
	if mutation.DeletePlugin {
		l4RulesRemoved, err := cascadePluginL4RulesTx(tx, mutation.PluginID, "", mutation.Operation.CreatedAt)
		if err != nil {
			return err
		}
		if mutation.DeleteInstances {
			var instanceIDs []string
			if err := tx.Model(&PluginInstanceRow{}).Where("plugin_id = ?", mutation.PluginID).Pluck("id", &instanceIDs).Error; err != nil {
				return err
			}
			if len(instanceIDs) > 0 {
				if err := tx.Where("resource_kind = ? AND resource_id IN ?", "plugin_instance", instanceIDs).Delete(&QuotaAllocationRow{}).Error; err != nil {
					return err
				}
				if err := tx.Where("resource_kind = ? AND resource_id IN ?", "plugin_instance", instanceIDs).Delete(&ResourceBindingRow{}).Error; err != nil {
					return err
				}
			}
			if err := tx.Where("plugin_id = ?", mutation.PluginID).Delete(&PluginInstanceRow{}).Error; err != nil {
				return err
			}
			quotaUpdatedAt := mutation.Operation.CreatedAt
			if quotaUpdatedAt.IsZero() {
				quotaUpdatedAt = time.Now().UTC()
			}
			if err := recomputeCountQuotaUsageTx(tx, quotaUpdatedAt); err != nil {
				return err
			}
		} else if l4RulesRemoved {
			quotaUpdatedAt := mutation.Operation.CreatedAt
			if quotaUpdatedAt.IsZero() {
				quotaUpdatedAt = time.Now().UTC()
			}
			if err := recomputeCountQuotaUsageTx(tx, quotaUpdatedAt); err != nil {
				return err
			}
		}
		if mutation.DeleteGrants {
			if err := tx.Where("plugin_id = ?", mutation.PluginID).Delete(&PluginGrantRow{}).Error; err != nil {
				return err
			}
		}
		result := tx.Where("plugin_id = ? AND state_version = ?", mutation.PluginID, current.StateVersion).Delete(&InstalledPluginRow{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("%w: plugin state changed concurrently", ErrPluginConflict)
		}
	} else {
		if mutation.Installed == nil {
			return errors.New("installed plugin update is required")
		}
		if mutation.Installed.PluginID != mutation.PluginID {
			return errors.New("installed plugin identity differs from mutation target")
		}
		next := *mutation.Installed
		next.StateVersion = current.StateVersion + 1
		result := tx.Model(&InstalledPluginRow{}).Where("plugin_id = ? AND state_version = ?", mutation.PluginID, current.StateVersion).Select("*").Omit("plugin_id").Updates(&next)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("%w: plugin state changed concurrently", ErrPluginConflict)
		}
		mutation.Installed.StateVersion = next.StateVersion
		if mutation.ReplaceGrants != nil {
			normalizePluginGrantRows(mutation.ReplaceGrants)
			if err := tx.Where("plugin_id = ?", mutation.PluginID).Delete(&PluginGrantRow{}).Error; err != nil {
				return err
			}
			if len(mutation.ReplaceGrants) > 0 {
				if err := tx.Create(&mutation.ReplaceGrants).Error; err != nil {
					return err
				}
			}
		}
		if mutation.ReplaceInstance != nil {
			if err := s.replacePluginInstanceTx(ctx, tx, mutation.PluginID, mutation.ReplaceInstance, mutation.ValidateInstanceScope, mutation.PromoteInstanceBinding); err != nil {
				return err
			}
		}
		if len(mutation.ReplaceInstances) > 0 {
			for index := range mutation.ReplaceInstances {
				if err := s.replacePluginInstanceTx(ctx, tx, mutation.PluginID, &mutation.ReplaceInstances[index], false, false); err != nil {
					return err
				}
			}
		}
		if mutation.DeleteInstanceID != "" {
			if err := s.deletePluginInstanceTx(tx, mutation); err != nil {
				return err
			}
		}
	}
	if err := reconcilePluginSecretOwnershipTx(tx, mutation, secretCandidates); err != nil {
		return err
	}
	if mutation.CompleteOperation {
		var storedOperation PluginOperationRow
		if err := tx.Where("id = ? AND plugin_id = ? AND completed_at IS NULL", mutation.Operation.ID, mutation.PluginID).First(&storedOperation).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%w: plugin operation is stale, replayed, or already completed", ErrPluginConflict)
			}
			return err
		}
		if err := preparePluginOperationForWriteTx(tx, &storedOperation); err != nil {
			return err
		}
		if err := preparePluginOperationForWriteTx(tx, &mutation.Operation); err != nil {
			return err
		}
		if !samePluginOperationPackageProvenance(storedOperation, mutation.Operation) {
			return fmt.Errorf("%w: plugin operation package provenance changed during completion", ErrPluginConflict)
		}
		result := tx.Model(&PluginOperationRow{}).Where("id = ? AND plugin_id = ? AND completed_at IS NULL", mutation.Operation.ID, mutation.PluginID).Updates(map[string]any{
			"status": mutation.Operation.Status, "agent_results_json": mutation.Operation.AgentResultsJSON,
			"error_class": mutation.Operation.ErrorClass, "error": mutation.Operation.Error, "completed_at": mutation.Operation.CompletedAt,
			"target_package_identity": mutation.Operation.TargetPackageIdentity, "source_id": mutation.Operation.SourceID,
			"source_kind": mutation.Operation.SourceKind, "source_risk_label": mutation.Operation.SourceRiskLabel,
			"target_signature_key_id": mutation.Operation.TargetSignatureKeyID, "target_signature_public_key": mutation.Operation.TargetSignaturePublicKey,
			"target_signature_fingerprint": mutation.Operation.TargetSignatureFingerprint,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("%w: plugin operation is stale, replayed, or already completed", ErrPluginConflict)
		}
		if err := completePluginAgentRuntimeAuthorityTx(tx, mutation.Operation); err != nil {
			return err
		}
		return tx.Create(&mutation.Audit).Error
	}
	if err := createPluginOperationAndAudit(tx, mutation.Operation, mutation.Audit); err != nil {
		return err
	}
	return persistPluginOperationScopesTx(tx, mutation.Operation, operationScopes)
}

func (s *GormStore) deletePluginInstanceTx(tx *gorm.DB, mutation PluginMutation) error {
	instanceID := strings.TrimSpace(mutation.DeleteInstanceID)
	if instanceID == "" {
		return errors.New("plugin instance identity is required")
	}
	var current PluginInstanceRow
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND plugin_id = ?", instanceID, mutation.PluginID).First(&current).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("%w: plugin instance is unavailable", ErrPluginConflict)
		}
		return err
	}
	if mutation.ExpectedInstanceVersion != 0 && current.StateVersion != mutation.ExpectedInstanceVersion {
		return fmt.Errorf("%w: plugin instance changed concurrently", ErrPluginConflict)
	}
	if mutation.DeleteInstanceHTTPRules {
		if err := cascadePluginInstanceHTTPRulesTx(tx, instanceID, mutation.Operation.CreatedAt); err != nil {
			return err
		}
		if _, err := cascadePluginL4RulesTx(tx, mutation.PluginID, pluginL4RuleInstanceTag(instanceID), mutation.Operation.CreatedAt); err != nil {
			return err
		}
		if err := tx.Where("id = ? AND plugin_id = ?", instanceID, mutation.PluginID).First(&current).Error; err != nil {
			return err
		}
	}
	bindings, err := CanonicalPluginInstanceBindings(current.BindingsJSON)
	if err != nil {
		return fmt.Errorf("plugin instance %s bindings: %w", current.ID, err)
	}
	if len(bindings) > 0 {
		return fmt.Errorf("%w: plugin instance %s still serves %d bound consumer(s)", ErrPluginDependencyConsumerInUse, current.ID, len(bindings))
	}
	if err := tx.Where("resource_kind = ? AND resource_id = ?", "plugin_instance", instanceID).Delete(&QuotaAllocationRow{}).Error; err != nil {
		return err
	}
	if err := tx.Where("resource_kind = ? AND resource_id = ?", "plugin_instance", instanceID).Delete(&ResourceBindingRow{}).Error; err != nil {
		return err
	}
	result := tx.Where("id = ? AND plugin_id = ? AND state_version = ?", instanceID, mutation.PluginID, current.StateVersion).Delete(&PluginInstanceRow{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("%w: plugin instance changed concurrently", ErrPluginConflict)
	}
	now := mutation.Operation.CreatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return recomputeCountQuotaUsageTx(tx, now)
}

func cascadePluginInstanceHTTPRulesTx(tx *gorm.DB, instanceID string, now time.Time) error {
	var rules []HTTPRuleRow
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Order("agent_id, id").Find(&rules).Error; err != nil {
		return err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	for _, rule := range rules {
		matched := false
		for _, backend := range parseHTTPBackendsForIntent(rule.BackendsJSON) {
			if backend.Kind == pluginsdk.HTTPBackendKindPluginProvider && backend.PluginProvider != nil && strings.TrimSpace(backend.PluginProvider.InstanceID) == instanceID {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		resourceID := rule.AgentID + ":" + strconv.Itoa(rule.ID)
		if err := detachPluginConsumerBindingsTx(tx, PluginDependencyConsumerHTTPRule, resourceID, now); err != nil {
			return err
		}
		if err := tx.Where("resource_kind = ? AND resource_id = ?", PluginDependencyConsumerHTTPRule, resourceID).Delete(&QuotaAllocationRow{}).Error; err != nil {
			return err
		}
		if err := tx.Where("resource_kind = ? AND resource_id = ?", PluginDependencyConsumerHTTPRule, resourceID).Delete(&ResourceBindingRow{}).Error; err != nil {
			return err
		}
		if err := tx.Where("agent_id = ? AND id = ?", rule.AgentID, rule.ID).Delete(&HTTPRuleRow{}).Error; err != nil {
			return err
		}
	}
	return nil
}

const (
	pluginL4RuleTagPrefix         = "plugin:"
	pluginL4RuleInstanceTagPrefix = "plugin-instance:"
)

func pluginL4RuleInstanceTag(instanceID string) string {
	instanceID = strings.TrimSpace(instanceID)
	if instanceID == "" {
		return ""
	}
	return pluginL4RuleInstanceTagPrefix + instanceID
}

// cascadePluginL4RulesTx removes host-owned L4 rules attributed to a plugin
// through the attribution tags written by the host l4.rule dispatch. An empty
// instanceTag cascades every rule owned by the plugin (plugin uninstall);
// an instance tag narrows the cascade to rules created by that instance.
func cascadePluginL4RulesTx(tx *gorm.DB, pluginID, instanceTag string, now time.Time) (bool, error) {
	pluginTag := pluginL4RuleTagPrefix + strings.TrimSpace(pluginID)
	if pluginTag == pluginL4RuleTagPrefix {
		return false, errors.New("plugin identity is required")
	}
	var rules []L4RuleRow
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Order("agent_id, id").Find(&rules).Error; err != nil {
		return false, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	removed := false
	for _, rule := range rules {
		if !pluginL4RuleMatchesAttribution(rule.TagsJSON, pluginTag, instanceTag) {
			continue
		}
		resourceID := rule.AgentID + ":" + strconv.Itoa(rule.ID)
		if err := detachPluginConsumerBindingsTx(tx, PluginDependencyConsumerL4Rule, resourceID, now); err != nil {
			return false, err
		}
		if err := tx.Where("resource_kind = ? AND resource_id = ?", PluginDependencyConsumerL4Rule, resourceID).Delete(&QuotaAllocationRow{}).Error; err != nil {
			return false, err
		}
		if err := tx.Where("resource_kind = ? AND resource_id = ?", PluginDependencyConsumerL4Rule, resourceID).Delete(&ResourceBindingRow{}).Error; err != nil {
			return false, err
		}
		if err := tx.Where("agent_id = ? AND id = ?", rule.AgentID, rule.ID).Delete(&L4RuleRow{}).Error; err != nil {
			return false, err
		}
		removed = true
	}
	return removed, nil
}

func pluginL4RuleMatchesAttribution(tagsJSON, pluginTag, instanceTag string) bool {
	pluginMatched := false
	instanceMatched := instanceTag == ""
	for _, tag := range parseStringSlice(tagsJSON) {
		if tag == pluginTag {
			pluginMatched = true
		}
		if tag == instanceTag {
			instanceMatched = true
		}
	}
	return pluginMatched && instanceMatched
}

func completePluginAgentRuntimeAuthorityTx(tx *gorm.DB, operation PluginOperationRow) error {
	var statuses []PluginAgentRuntimeStatusRow
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("operation_id = ? AND authority_slot IN ?", operation.ID, []string{"pending", "active"}).
		Order("agent_id, instance_id").Find(&statuses).Error; err != nil {
		return err
	}
	if operation.Status != "succeeded" {
		return tx.Model(&PluginAgentRuntimeStatusRow{}).
			Where("operation_id = ? AND authority_slot IN ?", operation.ID, []string{"pending", "active"}).
			Update("authority_slot", "retired").Error
	}
	for _, status := range statuses {
		if status.State != "active" {
			if status.AuthoritySlot == "pending" {
				if err := tx.Model(&PluginAgentRuntimeStatusRow{}).
					Where("operation_id = ? AND agent_id = ? AND instance_id = ? AND authority_slot = ?", status.OperationID, status.AgentID, status.InstanceID, "pending").
					Update("authority_slot", "retired").Error; err != nil {
					return err
				}
			}
			continue
		}
		if err := tx.Model(&PluginAgentRuntimeStatusRow{}).
			Where("agent_id = ? AND instance_id = ? AND authority_slot = ? AND operation_id <> ?", status.AgentID, status.InstanceID, "active", status.OperationID).
			Update("authority_slot", "retired").Error; err != nil {
			return err
		}
		if err := tx.Model(&PluginAgentRuntimeStatusRow{}).
			Where("operation_id = ? AND agent_id = ? AND instance_id = ? AND authority_slot IN ?", status.OperationID, status.AgentID, status.InstanceID, []string{"pending", "active"}).
			Update("authority_slot", "active").Error; err != nil {
			return err
		}
	}
	return nil
}

func pluginSecretIDsForPluginTx(tx *gorm.DB, pluginID string) (map[string]string, error) {
	var instances []PluginInstanceRow
	if err := tx.Where("plugin_id = ?", pluginID).Find(&instances).Error; err != nil {
		return nil, err
	}
	result := make(map[string]string)
	for _, instance := range instances {
		for _, raw := range []string{instance.SecretHandlesJSON, instance.PendingSecretHandlesJSON, instance.RollbackSecretHandlesJSON} {
			var handles []PluginInstanceSecretHandle
			if err := json.Unmarshal([]byte(pluginDefaultArrayJSON(raw)), &handles); err != nil {
				return nil, fmt.Errorf("plugin instance %s secret handles are invalid: %w", instance.ID, err)
			}
			for _, handle := range handles {
				if handle.ID == "" {
					return nil, fmt.Errorf("plugin instance %s secret handle identity is empty", instance.ID)
				}
				result[handle.ID] = instance.ID
			}
		}
	}
	return result, nil
}

func pluginSecretInstanceID(purpose string) string {
	const prefix = "plugin-config:"
	if !strings.HasPrefix(purpose, prefix) {
		return ""
	}
	remainder := strings.TrimPrefix(purpose, prefix)
	if separator := strings.IndexByte(remainder, ':'); separator > 0 {
		return remainder[:separator]
	}
	return ""
}

func reconcilePluginSecretOwnershipTx(tx *gorm.DB, mutation PluginMutation, candidates map[string]string) error {
	current, err := pluginSecretIDsForPluginTx(tx, mutation.PluginID)
	if err != nil {
		return err
	}
	active := make(map[string]struct{})
	var instances []PluginInstanceRow
	if err := tx.Where("plugin_id = ?", mutation.PluginID).Find(&instances).Error; err != nil {
		return err
	}
	for _, instance := range instances {
		var handles []PluginInstanceSecretHandle
		if err := json.Unmarshal([]byte(pluginDefaultArrayJSON(instance.SecretHandlesJSON)), &handles); err != nil {
			return err
		}
		for _, handle := range handles {
			active[handle.ID] = struct{}{}
		}
	}
	now := mutation.Operation.CreatedAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	for id, instanceID := range candidates {
		if strings.TrimSpace(id) == "" {
			continue
		}
		if _, referenced := current[id]; referenced {
			state := "staged"
			if _, promoted := active[id]; promoted {
				state = "active"
			}
			if err := tx.Model(&PluginOperationSecretRow{}).Where("operation_id = ? AND secret_id = ?", mutation.Operation.ID, id).Updates(map[string]any{"state": state}).Error; err != nil {
				return err
			}
			continue
		}
		if err := tx.Model(&SecretVersionRow{}).Where("secret_id = ? AND destroyed_at IS NULL", id).Updates(map[string]any{"destroyed_at": now, "nonce": []byte{}, "ciphertext": []byte{}}).Error; err != nil {
			return err
		}
		if err := tx.Model(&SecretRow{}).Where("id = ? AND retired_at IS NULL", id).Update("retired_at", now).Error; err != nil {
			return err
		}
		row := PluginOperationSecretRow{OperationID: mutation.Operation.ID, SecretID: id, InstanceID: instanceID, Role: "retired", State: "retired", CreatedAt: now, RetiredAt: &now}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "operation_id"}, {Name: "secret_id"}}, DoUpdates: clause.AssignmentColumns([]string{"role", "state", "retired_at"})}).Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

func pluginOperationScopesForPluginTx(tx *gorm.DB, pluginID string, operation PluginOperationRow) ([]PluginOperationScopeRow, error) {
	if operation.InstanceID != "" && operation.ResourceGroupID != "" {
		return []PluginOperationScopeRow{{OperationID: operation.ID, InstanceID: operation.InstanceID, PluginID: pluginID, ResourceGroupID: operation.ResourceGroupID, CreatedAt: operation.CreatedAt}}, nil
	}
	var instances []PluginInstanceRow
	if err := tx.Where("plugin_id = ?", pluginID).Find(&instances).Error; err != nil {
		return nil, err
	}
	rows := make([]PluginOperationScopeRow, 0, len(instances)+1)
	for _, instance := range instances {
		rows = mergePluginOperationScope(rows, instance, operation)
	}
	return rows, nil
}

func mergePluginOperationScope(rows []PluginOperationScopeRow, instance PluginInstanceRow, operation PluginOperationRow) []PluginOperationScopeRow {
	if instance.ID == "" || instance.ResourceGroupID == "" || instance.PluginID != operation.PluginID {
		return rows
	}
	for _, row := range rows {
		if row.InstanceID == instance.ID {
			return rows
		}
	}
	return append(rows, PluginOperationScopeRow{OperationID: operation.ID, InstanceID: instance.ID, PluginID: operation.PluginID, ResourceGroupID: instance.ResourceGroupID, CreatedAt: operation.CreatedAt})
}

func persistPluginOperationScopesTx(tx *gorm.DB, operation PluginOperationRow, rows []PluginOperationScopeRow) error {
	if operation.InstanceID != "" && operation.ResourceGroupID != "" {
		rows = mergePluginOperationScope(rows, PluginInstanceRow{ID: operation.InstanceID, PluginID: operation.PluginID, ResourceGroupID: operation.ResourceGroupID}, operation)
	}
	if len(rows) == 0 {
		return nil
	}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error
}

func ensurePluginPackageTx(tx *gorm.DB, candidate PluginPackageRow, artifacts []PluginArtifactRow) error {
	candidate.Digest = strings.ToLower(strings.TrimSpace(candidate.Digest))
	if candidate.Identity == "" {
		candidate.Identity = PluginPackageIdentity(candidate.Digest, candidate.SourceID, candidate.SignatureFingerprint)
	}
	if !marketplace.IsDigest(candidate.Digest) || !marketplace.IsDigest(candidate.Identity) || strings.TrimSpace(candidate.PluginID) == "" || strings.TrimSpace(candidate.Version) == "" || strings.TrimSpace(candidate.CachePath) == "" {
		return errors.New("verified plugin package identity is invalid")
	}
	var existing PluginPackageRow
	lookupErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("identity = ? OR (identity = '' AND digest = ?)", candidate.Identity, candidate.Digest).First(&existing).Error
	if errors.Is(lookupErr, gorm.ErrRecordNotFound) {
		if err := tx.Create(&candidate).Error; err != nil {
			return err
		}
		existing = candidate
	} else if lookupErr != nil {
		return lookupErr
	}
	manifestEqual, err := canonicalJSONEqual(existing.ManifestJSON, candidate.ManifestJSON)
	if err != nil {
		return fmt.Errorf("stored plugin manifest is invalid: %w", err)
	}
	schemaEqual, err := canonicalJSONEqual(existing.ConfigSchemaJSON, candidate.ConfigSchemaJSON)
	if err != nil {
		return fmt.Errorf("stored plugin config schema is invalid: %w", err)
	}
	if existing.PluginID != candidate.PluginID || existing.Version != candidate.Version || existing.RuntimeKind != candidate.RuntimeKind || existing.RuntimeABI != candidate.RuntimeABI || existing.HostScope != candidate.HostScope || existing.PolicyKind != candidate.PolicyKind || existing.EntryPath != candidate.EntryPath || existing.SignatureKeyID != candidate.SignatureKeyID || existing.SignaturePublicKey != candidate.SignaturePublicKey || existing.SignatureFingerprint != candidate.SignatureFingerprint || existing.SignatureVerdict != candidate.SignatureVerdict || existing.ResourceBudgetJSON != candidate.ResourceBudgetJSON || existing.FailurePolicyJSON != candidate.FailurePolicyJSON || filepath.Clean(existing.CachePath) != filepath.Clean(candidate.CachePath) || !manifestEqual || !schemaEqual {
		return fmt.Errorf("%w: verified package metadata differs for digest", ErrPluginConflict)
	}
	if existing.SourceRevision != candidate.SourceRevision || existing.SourceRefKind != candidate.SourceRefKind || existing.SourceRefName != candidate.SourceRefName || !strings.EqualFold(existing.SourceResolvedOID, candidate.SourceResolvedOID) || existing.SourceRiskLabel != candidate.SourceRiskLabel {
		if err := tx.Model(&PluginPackageRow{}).Where("identity = ?", existing.Identity).Updates(map[string]any{
			"source_risk_label": candidate.SourceRiskLabel, "source_revision": candidate.SourceRevision,
			"source_ref_kind": candidate.SourceRefKind, "source_ref_name": candidate.SourceRefName,
			"source_resolved_oid": strings.ToLower(strings.TrimSpace(candidate.SourceResolvedOID)), "verified_at": candidate.VerifiedAt,
		}).Error; err != nil {
			return err
		}
	}
	return ensurePluginArtifactsTx(tx, candidate.Identity, candidate.Digest, artifacts)
}

func ensurePluginArtifactsTx(tx *gorm.DB, packageIdentity, packageDigest string, artifacts []PluginArtifactRow) error {
	packageIdentity = strings.ToLower(strings.TrimSpace(packageIdentity))
	packageDigest = strings.ToLower(strings.TrimSpace(packageDigest))
	for index := range artifacts {
		artifacts[index].PackageIdentity = packageIdentity
		artifacts[index].PackageDigest = packageDigest
		artifacts[index].SHA256 = strings.ToLower(strings.TrimSpace(artifacts[index].SHA256))
		artifacts[index].ID = pluginStorageDigest(packageIdentity, strings.TrimSpace(artifacts[index].Path))
	}
	if len(artifacts) > 0 {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&artifacts).Error; err != nil {
			return err
		}
	}
	var existing []PluginArtifactRow
	if err := tx.Where("package_identity = ?", packageIdentity).Order("path").Find(&existing).Error; err != nil {
		return err
	}
	expected := append([]PluginArtifactRow(nil), artifacts...)
	sort.Slice(expected, func(i, j int) bool { return expected[i].Path < expected[j].Path })
	existing = durablePluginPackageArtifacts(existing, expected)
	if !samePluginArtifactRows(existing, expected) {
		return fmt.Errorf("%w: verified artifact metadata differs for digest", ErrPluginConflict)
	}
	return nil
}

func durablePluginPackageArtifacts(existing, projected []PluginArtifactRow) []PluginArtifactRow {
	ids := make(map[string]struct{}, len(projected))
	for _, artifact := range projected {
		id := strings.TrimSpace(artifact.ID)
		if id == "" {
			continue
		}
		ids[id] = struct{}{}
	}
	matched := make([]PluginArtifactRow, 0, len(projected))
	for _, artifact := range existing {
		if _, ok := ids[strings.TrimSpace(artifact.ID)]; ok {
			matched = append(matched, artifact)
		}
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].Path < matched[j].Path })
	return matched
}

func samePluginArtifactRows(left, right []PluginArtifactRow) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func canonicalJSONEqual(left, right string) (bool, error) {
	decode := func(raw string) (any, error) {
		decoder := json.NewDecoder(bytes.NewReader([]byte(raw)))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		var trailing any
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			if err == nil {
				return nil, errors.New("multiple JSON values")
			}
			return nil, err
		}
		return value, nil
	}
	leftValue, err := decode(left)
	if err != nil {
		return false, err
	}
	rightValue, err := decode(right)
	if err != nil {
		return false, err
	}
	return reflect.DeepEqual(leftValue, rightValue), nil
}

func (s *GormStore) replacePluginInstanceTx(ctx context.Context, tx *gorm.DB, pluginID string, instance *PluginInstanceRow, validateScope, promote bool) error {
	if instance == nil || instance.PluginID != pluginID {
		return errors.New("plugin instance identity differs from mutation target")
	}
	if err := normalizePluginInstancePolicyChains(instance); err != nil {
		return err
	}
	// Resource/agent bindings are the global first lock class. Agent rebind uses
	// the same order before it locks plugin instances, avoiding a DB lock cycle.
	if validateScope {
		if err := s.validatePluginInstanceScopeTx(ctx, tx, *instance, promote); err != nil {
			return err
		}
	}
	if err := resolvePluginInstanceBindingFencesTx(ctx, tx, instance); err != nil {
		return err
	}
	var current PluginInstanceRow
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", instance.ID).First(&current).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if err == nil {
		if current.PluginID != pluginID || instance.StateVersion != current.StateVersion {
			return fmt.Errorf("%w: plugin instance changed concurrently", ErrPluginConflict)
		}
	} else if instance.StateVersion != 0 {
		return fmt.Errorf("%w: plugin instance changed concurrently", ErrPluginConflict)
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		instance.StateVersion = 1
		return tx.Create(instance).Error
	}
	next := *instance
	next.StateVersion = current.StateVersion + 1
	result := tx.Model(&PluginInstanceRow{}).Where("id = ? AND state_version = ?", instance.ID, current.StateVersion).Select("*").Omit("id").Updates(&next)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("%w: plugin instance changed concurrently", ErrPluginConflict)
	}
	instance.StateVersion = next.StateVersion
	return nil
}

func resolvePluginInstanceBindingFencesTx(ctx context.Context, tx *gorm.DB, instance *PluginInstanceRow) error {
	if instance == nil {
		return errors.New("plugin instance is required")
	}
	pendingGroupID := strings.TrimSpace(instance.PendingResourceGroupID)
	if pendingGroupID == "" {
		pendingGroupID = strings.TrimSpace(instance.ResourceGroupID)
	}
	fields := []struct {
		label   string
		raw     *string
		groupID string
	}{
		{label: "bindings", raw: &instance.BindingsJSON, groupID: instance.ResourceGroupID},
		{label: "pending bindings", raw: &instance.PendingBindingsJSON, groupID: pendingGroupID},
		{label: "rollback bindings", raw: &instance.RollbackBindingsJSON, groupID: instance.RollbackResourceGroupID},
	}
	for _, field := range fields {
		bindings, err := CanonicalPluginInstanceBindings(*field.raw)
		if err != nil {
			return fmt.Errorf("plugin instance %s %s: %w", instance.ID, field.label, err)
		}
		resolved, err := resolvePluginInstanceBindingRequestsTx(ctx, tx, PluginInstanceBindingRequests(bindings), field.groupID, true)
		if err != nil {
			return fmt.Errorf("plugin instance %s %s: %w", instance.ID, field.label, err)
		}
		*field.raw, err = EncodePluginInstanceBindings(resolved)
		if err != nil {
			return fmt.Errorf("plugin instance %s %s: %w", instance.ID, field.label, err)
		}
	}
	return nil
}

func normalizePluginInstancePolicyChains(instance *PluginInstanceRow) error {
	if instance == nil {
		return errors.New("plugin instance is required")
	}
	if err := ValidatePluginPolicyIdentity(instance.ID); err != nil {
		return fmt.Errorf("plugin instance identity: %w", err)
	}
	if err := ValidatePluginPolicyIdentity(instance.PluginID); err != nil {
		return fmt.Errorf("plugin identity: %w", err)
	}
	for label, field := range map[string]*string{
		"policy chains":          &instance.PolicyChainsJSON,
		"pending policy chains":  &instance.PendingPolicyChainsJSON,
		"rollback policy chains": &instance.RollbackPolicyChainsJSON,
	} {
		chains, err := CanonicalPluginPolicyChains(*field)
		if err != nil {
			return fmt.Errorf("plugin instance %s %s: %w", instance.ID, label, err)
		}
		canonical, err := EncodePluginPolicyChains(chains)
		if err != nil {
			return fmt.Errorf("plugin instance %s %s: %w", instance.ID, label, err)
		}
		*field = canonical
	}
	for label, field := range map[string]*string{
		"bindings":          &instance.BindingsJSON,
		"pending bindings":  &instance.PendingBindingsJSON,
		"rollback bindings": &instance.RollbackBindingsJSON,
	} {
		bindings, err := CanonicalPluginInstanceBindings(*field)
		if err != nil {
			return fmt.Errorf("plugin instance %s %s: %w", instance.ID, label, err)
		}
		canonical, err := EncodePluginInstanceBindings(bindings)
		if err != nil {
			return fmt.Errorf("plugin instance %s %s: %w", instance.ID, label, err)
		}
		*field = canonical
	}
	return nil
}

func validatePackageAcquisitionTx(tx *gorm.DB, sourceID, digest string, candidate PluginPackageRow) error {
	digest = strings.ToLower(strings.TrimSpace(digest))
	if !marketplace.IsDigest(digest) {
		return errors.New("verified package digest is invalid")
	}
	var source MarketplaceSourceRow
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND deleting = ?", sourceID, false).First(&source).Error; err != nil {
		return errors.New("marketplace package source is unavailable or deleting")
	}
	now := time.Now().UTC()
	fence, err := lockPackageDigestFenceTx(tx, digest, now)
	if err != nil {
		return err
	}
	if fence.ClaimToken != "" {
		return errors.New("verified package cache is being deleted")
	}
	var snapshot MarketSnapshotRow
	if source.CurrentSnapshotID == "" || tx.Where("id = ? AND source_id = ?", source.CurrentSnapshotID, source.ID).First(&snapshot).Error != nil {
		return fmt.Errorf("%w: current snapshot is missing", ErrMarketplaceCatalogStale)
	}
	if err := validateCurrentMarketplaceSnapshot(source, snapshot); err != nil {
		return err
	}
	var acquisition PluginPackageAcquisitionRow
	if err := tx.Where("source_id = ? AND digest = ? AND snapshot_id = ? AND status = ?", sourceID, digest, source.CurrentSnapshotID, "catalog").First(&acquisition).Error; err != nil {
		return errors.New("verified package acquisition is unavailable")
	}
	if acquisition.SourceRevision != source.ConfigRevision || acquisition.RefKind != source.RefKind || acquisition.RefName != source.RefName || !marketplace.IsFullCommitOID(acquisition.ResolvedOID) || !strings.EqualFold(acquisition.ResolvedOID, source.CurrentResolvedOID) {
		return fmt.Errorf("%w: package acquisition differs from the current source", ErrMarketplaceCatalogStale)
	}
	if acquisition.SourceKind != candidate.SourceKind || acquisition.SignatureKeyID != candidate.SignatureKeyID || acquisition.SignaturePublicKey != candidate.SignaturePublicKey || acquisition.SignatureFingerprint != candidate.SignatureFingerprint || candidate.SourceID != sourceID {
		return errors.New("verified package acquisition signer binding differs from package metadata")
	}
	hasCandidateRepositoryProvenance := candidate.SourceRevision != 0 || candidate.SourceRefKind != "" || candidate.SourceRefName != "" || candidate.SourceResolvedOID != ""
	if hasCandidateRepositoryProvenance && (acquisition.SourceRevision != candidate.SourceRevision || acquisition.RefKind != candidate.SourceRefKind || acquisition.RefName != candidate.SourceRefName || !strings.EqualFold(acquisition.ResolvedOID, candidate.SourceResolvedOID)) {
		return errors.New("verified package acquisition repository provenance differs from package metadata")
	}
	return nil
}

func (s *GormStore) validatePluginInstanceScopeTx(ctx context.Context, tx *gorm.DB, instance PluginInstanceRow, promote bool) error {
	groupID := instance.PendingResourceGroupID
	targetJSON := instance.PendingTargetJSON
	if promote || groupID == "" {
		groupID = instance.ResourceGroupID
		targetJSON = instance.TargetJSON
	}
	if strings.TrimSpace(groupID) == "" {
		return fmt.Errorf("%w: resource group is required", ErrPluginInstanceScope)
	}
	var group ResourceGroupRow
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", groupID).First(&group).Error; err != nil {
		return fmt.Errorf("%w: resource group does not exist", ErrPluginInstanceScope)
	}
	targets, err := pluginInstanceTargets(targetJSON, s.LocalAgentID())
	if err != nil {
		return fmt.Errorf("%w: targets are invalid", ErrPluginInstanceScope)
	}
	for _, target := range targets {
		var binding ResourceBindingRow
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("resource_kind = ? AND resource_id = ?", "agent", target).First(&binding).Error
		if errors.Is(err, gorm.ErrRecordNotFound) && groupID == "default" {
			if target == s.LocalAgentID() {
				continue
			}
			var agent AgentRow
			if tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", target).First(&agent).Error == nil {
				continue
			}
		}
		if err != nil || binding.ResourceGroupID != groupID {
			return fmt.Errorf("%w: target is outside the selected resource group", ErrPluginInstanceScope)
		}
	}
	if promote || instance.ConfigVersion == 0 {
		now := time.Now().UTC()
		var current ResourceBindingRow
		currentErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("resource_kind = ? AND resource_id = ?", "plugin_instance", instance.ID).First(&current).Error
		if currentErr != nil && !errors.Is(currentErr, gorm.ErrRecordNotFound) {
			return currentErr
		}
		needsAllocation := currentErr != nil || current.ResourceGroupID != groupID
		var allocations []QuotaAllocationRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("resource_kind = ? AND resource_id = ? AND metric = ?", "plugin_instance", instance.ID, "application_count").Find(&allocations).Error; err != nil {
			return err
		}
		if len(allocations) == 0 {
			needsAllocation = true
		}
		if needsAllocation {
			actor, ok := QuotaActorFromContext(ctx)
			if !ok {
				return ErrQuotaActorRequired
			}
			if err := tx.Where("resource_kind = ? AND resource_id = ? AND metric = ?", "plugin_instance", instance.ID, "application_count").Delete(&QuotaAllocationRow{}).Error; err != nil {
				return err
			}
			var scopes []quotaScope
			if actor.Bootstrap {
				scopes = []quotaScope{{SubjectKind: "resource_group", SubjectID: groupID, ResourceGroupID: groupID}}
			} else {
				var err error
				scopes, err = s.quotaScopesTx(tx, actor.UserID, groupID)
				if err != nil {
					return err
				}
			}
			if _, err := s.consumeQuotaScopesTx(tx, scopes, "application_count", 1, now, true, false); err != nil {
				return err
			}
			for _, scope := range scopes {
				allocation := QuotaAllocationRow{ID: quotaAllocationID("plugin_instance", instance.ID, "application_count", scope), ResourceKind: "plugin_instance", ResourceID: instance.ID, Metric: "application_count", SubjectKind: scope.SubjectKind, SubjectID: scope.SubjectID, ResourceGroupID: scope.ResourceGroupID, Amount: 1, CreatedAt: now}
				if err := tx.Create(&allocation).Error; err != nil {
					return err
				}
			}
		}
		binding := ResourceBindingRow{ID: pluginStorageID("binding"), ResourceKind: "plugin_instance", ResourceID: instance.ID, ResourceGroupID: groupID, UpdatedAt: now}
		if currentErr == nil {
			binding.ID = current.ID
		}
		if len(targets) == 1 {
			binding.ParentResourceKind, binding.ParentResourceID = "agent", targets[0]
		}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "resource_kind"}, {Name: "resource_id"}}, DoUpdates: clause.AssignmentColumns([]string{"resource_group_id", "parent_resource_kind", "parent_resource_id", "updated_at"})}).Create(&binding).Error; err != nil {
			return err
		}
		return recomputeCountQuotaUsageTx(tx, now)
	}
	return nil
}

func (s *GormStore) ListPluginInstances(ctx context.Context, pluginID string) ([]PluginInstanceRow, error) {
	var rows []PluginInstanceRow
	err := s.db.WithContext(ctx).Where("plugin_id = ?", pluginID).Order("id").Find(&rows).Error
	return rows, err
}

func (s *GormStore) RecordPluginOperation(ctx context.Context, operation PluginOperationRow, audit AuditEventRow) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error { return createPluginOperationAndAudit(tx, operation, audit) })
}

func (s *GormStore) GetInstalledPlugin(ctx context.Context, pluginID string) (InstalledPluginRow, bool, error) {
	var row InstalledPluginRow
	db := s.db.WithContext(ctx)
	if s.transactionScoped {
		db = db.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	err := db.Where("plugin_id = ?", pluginID).First(&row).Error
	if err == gorm.ErrRecordNotFound {
		return InstalledPluginRow{}, false, nil
	}
	return row, err == nil, err
}

func (s *GormStore) ListInstalledPlugins(ctx context.Context) ([]InstalledPluginRow, error) {
	var rows []InstalledPluginRow
	err := s.db.WithContext(ctx).Order("plugin_id").Find(&rows).Error
	return rows, err
}

func (s *GormStore) ListPluginPackages(ctx context.Context) ([]PluginPackageRow, error) {
	var rows []PluginPackageRow
	err := s.db.WithContext(ctx).Order("plugin_id, version, digest").Find(&rows).Error
	return rows, err
}

func (s *GormStore) GetPluginPackage(ctx context.Context, digest string) (PluginPackageRow, bool, error) {
	var row PluginPackageRow
	err := s.db.WithContext(ctx).Where("digest = ?", strings.ToLower(digest)).Order("identity").First(&row).Error
	if err == gorm.ErrRecordNotFound {
		return PluginPackageRow{}, false, nil
	}
	return row, err == nil, err
}

func (s *GormStore) GetPluginPackageByIdentity(ctx context.Context, identity string) (PluginPackageRow, bool, error) {
	var row PluginPackageRow
	err := s.db.WithContext(ctx).Where("identity = ?", strings.ToLower(strings.TrimSpace(identity))).First(&row).Error
	if err == gorm.ErrRecordNotFound {
		return PluginPackageRow{}, false, nil
	}
	return row, err == nil, err
}

func (s *GormStore) ListPluginArtifacts(ctx context.Context, digest string) ([]PluginArtifactRow, error) {
	var rows []PluginArtifactRow
	err := s.db.WithContext(ctx).Where("package_digest = ?", strings.ToLower(strings.TrimSpace(digest))).Order("path").Find(&rows).Error
	return rows, err
}

func (s *GormStore) ListPluginArtifactsByIdentity(ctx context.Context, identity string) ([]PluginArtifactRow, error) {
	var rows []PluginArtifactRow
	err := s.db.WithContext(ctx).Where("package_identity = ?", strings.ToLower(strings.TrimSpace(identity))).Order("path").Find(&rows).Error
	return rows, err
}

func (s *GormStore) ListPluginGrants(ctx context.Context, pluginID string) ([]PluginGrantRow, error) {
	var rows []PluginGrantRow
	err := s.db.WithContext(ctx).Where("plugin_id = ?", pluginID).Order("permission, resource_selector").Find(&rows).Error
	return rows, err
}

func (s *GormStore) GetPluginInstance(ctx context.Context, id string) (PluginInstanceRow, bool, error) {
	var row PluginInstanceRow
	db := s.db.WithContext(ctx)
	if s.transactionScoped {
		db = db.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	err := db.Where("id = ?", id).First(&row).Error
	if err == gorm.ErrRecordNotFound {
		return PluginInstanceRow{}, false, nil
	}
	return row, err == nil, err
}

func (s *GormStore) ListPluginOperations(ctx context.Context, pluginID string) ([]PluginOperationRow, error) {
	var rows []PluginOperationRow
	err := s.db.WithContext(ctx).Where("plugin_id = ?", pluginID).Order("created_at, id").Find(&rows).Error
	return rows, err
}

func (s *GormStore) ListPluginOperationScopes(ctx context.Context, pluginID string) ([]PluginOperationScopeRow, error) {
	var rows []PluginOperationScopeRow
	err := s.db.WithContext(ctx).Where("plugin_id = ?", pluginID).Order("operation_id, instance_id").Find(&rows).Error
	return rows, err
}

func createPluginOperationAndAudit(tx *gorm.DB, operation PluginOperationRow, audit AuditEventRow) error {
	if err := preparePluginOperationForWriteTx(tx, &operation); err != nil {
		return err
	}
	if err := tx.Create(&operation).Error; err != nil {
		return err
	}
	if err := persistPluginOperationScopesTx(tx, operation, nil); err != nil {
		return err
	}
	return tx.Create(&audit).Error
}

func marketplaceSourceToRow(source marketplace.Source) MarketplaceSourceRow {
	return MarketplaceSourceRow{ID: source.ID, Kind: source.Kind, Purpose: source.Purpose, Name: source.Name, NameKey: strings.ToLower(strings.TrimSpace(source.Name)), URL: source.URL, RefKind: source.RefKind, RefName: source.RefName, ConfigRevision: source.ConfigRevision, CurrentResolvedOID: source.CurrentResolvedOID, CredentialRef: source.CredentialRef, SignerKeyID: source.SignerKeyID, SignerSecretRef: source.SignerSecretRef, SignerPublicKey: source.SignerPublicKey, SignerFingerprint: source.SignerFingerprint, RefreshIntervalNS: int64(source.RefreshInterval), RiskLabel: source.RiskLabel, CurrentSnapshotID: source.CurrentSnapshot, LastResult: source.LastResult, LastError: source.LastError, UpdatedAt: source.UpdatedAt, LastCompletedAt: source.LastCompletedAt, RefreshLeaseExpiresAt: source.LeaseExpiresAt, Deleting: source.Deleting}
}

func marketplaceRefreshOperationToRow(operation marketplace.RefreshOperation) MarketplaceRefreshOperationRow {
	return MarketplaceRefreshOperationRow{ID: operation.ID, SourceID: operation.SourceID, Commit: operation.Commit, SourceRevision: operation.SourceRevision, RefKind: operation.RefKind, RefName: operation.RefName, SignerSourceKind: operation.SignerSourceKind, SignerKeyID: operation.SignerKeyID, SignerPublicKey: operation.SignerPublicKey, SignerFingerprint: operation.SignerFingerprint, Status: operation.Status, ErrorClass: operation.ErrorClass, Error: operation.Error, DiffJSON: pluginDefaultJSON(operation.DiffJSON), StartedAt: operation.StartedAt, FinishedAt: operation.FinishedAt, ActorID: operation.Actor.ActorID, SessionID: operation.Actor.SessionID, CorrelationID: operation.Actor.CorrelationID, LeaseToken: operation.LeaseToken, LeaseExpiresAt: operation.LeaseExpiresAt}
}

func marketplaceRefreshOperationFromRow(row MarketplaceRefreshOperationRow) marketplace.RefreshOperation {
	return marketplace.RefreshOperation{ID: row.ID, SourceID: row.SourceID, Commit: row.Commit, SourceRevision: row.SourceRevision, RefKind: row.RefKind, RefName: row.RefName, SignerSourceKind: row.SignerSourceKind, SignerKeyID: row.SignerKeyID, SignerPublicKey: row.SignerPublicKey, SignerFingerprint: row.SignerFingerprint, Status: row.Status, ErrorClass: row.ErrorClass, Error: row.Error, DiffJSON: row.DiffJSON, StartedAt: row.StartedAt, FinishedAt: row.FinishedAt, Actor: marketplace.OperationActor{ActorID: row.ActorID, SessionID: row.SessionID, CorrelationID: row.CorrelationID}, LeaseToken: row.LeaseToken, LeaseExpiresAt: row.LeaseExpiresAt}
}

func marketplaceSourceFromRow(row MarketplaceSourceRow) marketplace.Source {
	return marketplace.Source{ID: row.ID, Kind: row.Kind, Purpose: row.Purpose, Name: row.Name, URL: row.URL, RefKind: row.RefKind, RefName: row.RefName, ConfigRevision: row.ConfigRevision, CurrentResolvedOID: row.CurrentResolvedOID, CredentialRef: row.CredentialRef, CredentialConfigured: row.CredentialRef != "", SignerKeyID: row.SignerKeyID, SignerSecretRef: row.SignerSecretRef, SignerPublicKey: row.SignerPublicKey, SignerFingerprint: row.SignerFingerprint, RefreshInterval: time.Duration(row.RefreshIntervalNS), RiskLabel: row.RiskLabel, CurrentSnapshot: row.CurrentSnapshotID, LastResult: row.LastResult, LastError: row.LastError, UpdatedAt: row.UpdatedAt, LastCompletedAt: row.LastCompletedAt, LeaseExpiresAt: row.RefreshLeaseExpiresAt, Deleting: row.Deleting}
}

func backfillMarketplaceSignatureTrust(ctx context.Context, db *gorm.DB) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var sources []MarketplaceSourceRow
		if err := tx.Find(&sources).Error; err != nil {
			return err
		}
		trustBySource := make(map[string]marketplace.SignatureTrust, len(sources)+1)
		for index := range sources {
			row := &sources[index]
			if row.Kind == marketplace.SourceKindCustom && row.SignerPublicKey != "" && row.SignerFingerprint == "" {
				fingerprint, err := marketplace.SourceSignerFingerprint(row.SignerPublicKey)
				if err != nil {
					return fmt.Errorf("backfill marketplace signer fingerprint for %s: %w", row.ID, err)
				}
				row.SignerFingerprint = fingerprint
				if err := tx.Model(&MarketplaceSourceRow{}).Where("id = ? AND signer_fingerprint = ?", row.ID, "").Update("signer_fingerprint", fingerprint).Error; err != nil {
					return err
				}
			}
			trust, err := marketplaceSourceFromRow(*row).SignatureTrust()
			if err == nil {
				trustBySource[row.ID] = trust
			}
		}
		if officialTrust, err := marketplace.OfficialSource().SignatureTrust(); err == nil {
			trustBySource[marketplace.OfficialSourceID] = officialTrust
		}
		var acquisitions []PluginPackageAcquisitionRow
		if err := tx.Find(&acquisitions).Error; err != nil {
			return err
		}
		for _, row := range acquisitions {
			trust, ok := trustBySource[row.SourceID]
			if !ok {
				continue
			}
			if err := validatePluginAcquisitionTrust(row, trust); err != nil {
				return err
			}
			if row.SourceKind == trust.SourceKind && row.SignatureKeyID == trust.KeyID && row.SignaturePublicKey == trust.PublicKey && strings.EqualFold(row.SignatureFingerprint, trust.Fingerprint) {
				continue
			}
			if err := tx.Model(&PluginPackageAcquisitionRow{}).Where("source_id = ? AND digest = ?", row.SourceID, row.Digest).Updates(map[string]any{"source_kind": trust.SourceKind, "signature_key_id": trust.KeyID, "signature_public_key": trust.PublicKey, "signature_fingerprint": trust.Fingerprint}).Error; err != nil {
				return err
			}
		}
		var packages []PluginPackageRow
		if err := tx.Where("signature_public_key = ? OR signature_fingerprint = ? OR source_id = ?", "", "", "").Find(&packages).Error; err != nil {
			return err
		}
		for _, row := range packages {
			query := tx.Where("digest = ? AND signature_public_key <> ?", row.Digest, "")
			if row.SourceID != "" {
				query = query.Where("source_id = ?", row.SourceID)
			}
			var candidateAcquisitions []PluginPackageAcquisitionRow
			if err := query.Order("source_id").Find(&candidateAcquisitions).Error; err != nil || len(candidateAcquisitions) != 1 {
				continue
			}
			acquisition := candidateAcquisitions[0]
			if row.SignatureKeyID != "" && row.SignatureKeyID != acquisition.SignatureKeyID {
				continue
			}
			var source MarketplaceSourceRow
			_ = tx.Where("id = ?", acquisition.SourceID).First(&source).Error
			if err := tx.Model(&PluginPackageRow{}).Where("identity = ?", row.Identity).Updates(map[string]any{"source_id": acquisition.SourceID, "source_kind": acquisition.SourceKind, "source_risk_label": source.RiskLabel, "signature_key_id": acquisition.SignatureKeyID, "signature_public_key": acquisition.SignaturePublicKey, "signature_fingerprint": acquisition.SignatureFingerprint}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func pluginDefaultJSON(value string) string {
	if strings.TrimSpace(value) == "" {
		return "{}"
	}
	return value
}

func pluginDefaultArrayJSON(value string) string {
	if strings.TrimSpace(value) == "" {
		return "[]"
	}
	return value
}

func pluginStorageID(prefix string) string { return securityID(prefix) }

func marketplaceRefreshAudit(operation MarketplaceRefreshOperationRow, result string, now time.Time) (AuditEventRow, error) {
	if err := validateMarketplaceRefreshAuditProvenance(operation); err != nil {
		return AuditEventRow{}, err
	}
	metadataValues := map[string]any{
		"operation_id": operation.ID, "commit": operation.Commit, "source_revision": operation.SourceRevision,
		"ref_kind": operation.RefKind, "ref_name": operation.RefName, "resolved_oid": operation.Commit,
		"diff":          json.RawMessage(pluginDefaultJSON(operation.DiffJSON)),
		"signer_key_id": operation.SignerKeyID, "signer_fingerprint": operation.SignerFingerprint,
	}
	metadata, _ := json.Marshal(metadataValues)
	return AuditEventRow{ID: pluginStorageID("audit"), ActorID: operation.ActorID, SessionID: operation.SessionID, Action: "marketplace.source.refresh", TargetKind: "marketplace_source", TargetID: operation.SourceID, CorrelationID: operation.CorrelationID, Result: result, ErrorClass: operation.ErrorClass, MetadataJSON: string(metadata), CreatedAt: now}, nil
}

func validateMarketplaceRefreshAuditProvenance(operation MarketplaceRefreshOperationRow) error {
	if operation.SourceRevision == 0 || (operation.RefKind != marketplace.GitRefKindBranch && operation.RefKind != marketplace.GitRefKindTag) || strings.TrimSpace(operation.RefName) == "" || strings.TrimSpace(operation.SignerKeyID) == "" || !marketplace.IsDigest(operation.SignerFingerprint) {
		return errors.New("refresh operation repository or signer provenance is incomplete")
	}
	if operation.Commit != "" && !marketplace.IsFullCommitOID(operation.Commit) {
		return errors.New("refresh operation resolved OID is invalid")
	}
	return nil
}

func marketplaceIncompleteRefreshAudit(operation MarketplaceRefreshOperationRow, now time.Time) AuditEventRow {
	metadata, _ := json.Marshal(map[string]any{"operation_id": operation.ID, "provenance_incomplete": true})
	return AuditEventRow{ID: pluginStorageID("audit"), ActorID: operation.ActorID, SessionID: operation.SessionID, Action: "marketplace.source.refresh", TargetKind: "marketplace_source", TargetID: operation.SourceID, CorrelationID: operation.CorrelationID, Result: "failure", ErrorClass: "provenance_incomplete", MetadataJSON: string(metadata), CreatedAt: now}
}

func pluginAudit(id, actor, action, pluginID, result, errorClass string, metadata any, now time.Time) AuditEventRow {
	encoded, _ := json.Marshal(metadata)
	return AuditEventRow{ID: id, ActorID: actor, Action: action, TargetKind: "plugin", TargetID: pluginID, Result: result, ErrorClass: errorClass, MetadataJSON: string(encoded), CreatedAt: now}
}
