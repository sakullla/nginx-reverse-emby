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
	"path"
	"sort"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrPluginGenerationConflict = errors.New("plugin generation identity conflict")
	ErrPluginGenerationStale    = errors.New("plugin generation report is stale")
)

const PluginGenerationCapability = "plugin_generation_v1"

type PluginGenerationReport struct {
	OperationID    string          `json:"operation_id"`
	AgentID        string          `json:"agent_id"`
	InstanceID     string          `json:"instance_id"`
	PluginID       string          `json:"plugin_id"`
	Revision       int64           `json:"revision"`
	GenerationID   string          `json:"generation_id"`
	PackageDigest  string          `json:"package_digest"`
	ArtifactDigest string          `json:"artifact_digest"`
	State          string          `json:"state"`
	Sequence       uint64          `json:"sequence"`
	ErrorCode      string          `json:"error_code,omitempty"`
	SafeDetail     string          `json:"safe_detail,omitempty"`
	Details        json.RawMessage `json:"details,omitempty"`
	Budget         json.RawMessage `json:"budget,omitempty"`
	ReportedAt     time.Time       `json:"reported_at"`
}

// LoadAgentPluginGenerations returns the effective candidate projection for
// one Agent. It can be used independently by revision/report wiring; snapshot
// loads call the transaction-scoped helper so the whole graph is coherent.
func (s *GormStore) LoadAgentPluginGenerations(ctx context.Context, agentID, platform string) ([]PluginGeneration, error) {
	if s.transactionScoped {
		return s.loadAgentPluginGenerations(ctx, agentID, platform)
	}
	var generations []PluginGeneration
	err := s.readSnapshotTransaction(ctx, func(scoped *GormStore) error {
		var err error
		generations, err = scoped.loadAgentPluginGenerations(ctx, agentID, platform)
		return err
	})
	return generations, err
}

func (s *GormStore) loadAgentPluginGenerations(ctx context.Context, agentID, platform string) ([]PluginGeneration, error) {
	agentID = s.resolveAgentID(agentID)
	var installed []InstalledPluginRow
	if err := s.db.WithContext(ctx).Where("desired_lifecycle = ?", "enabled").Order("plugin_id").Find(&installed).Error; err != nil {
		return nil, err
	}
	result := make([]PluginGeneration, 0)
	for _, plugin := range installed {
		var instances []PluginInstanceRow
		if err := s.db.WithContext(ctx).Where("plugin_id = ? AND desired_enabled = ?", plugin.PluginID, true).Order("id").Find(&instances).Error; err != nil {
			return nil, err
		}
		targetedInstances := make([]PluginInstanceRow, 0, len(instances))
		for _, instance := range instances {
			hasPendingGeneration := instance.PendingOperationID != "" && instance.PendingOperationID == plugin.PendingOperationID && instance.PendingVersion > 0
			if instance.ConfigVersion == 0 && !hasPendingGeneration {
				// A failed first deployment has no committed runtime identity.
				// Keeping a zero-version generation in the snapshot makes the
				// next valid configure operation fail validation before it can
				// replace that abandoned candidate.
				continue
			}
			targetJSON := instance.TargetJSON
			if instance.PendingOperationID == plugin.PendingOperationID && strings.TrimSpace(instance.PendingTargetJSON) != "" {
				targetJSON = instance.PendingTargetJSON
			}
			targets, err := pluginInstanceTargets(targetJSON, s.LocalAgentID())
			if err != nil {
				return nil, fmt.Errorf("plugin instance %s targets: %w", instance.ID, err)
			}
			if pluginGenerationContainsString(targets, agentID) {
				targetedInstances = append(targetedInstances, instance)
			}
		}
		if len(targetedInstances) == 0 {
			continue
		}
		packageIdentity, packageDigest := plugin.ActivePackageIdentity, plugin.ActivePackageDigest
		if plugin.PendingOperationID != "" && plugin.StagedPackageIdentity != "" && plugin.StagedPackageDigest != "" {
			packageIdentity, packageDigest = plugin.StagedPackageIdentity, plugin.StagedPackageDigest
		}
		var packageRow PluginPackageRow
		query := s.db.WithContext(ctx).Where("identity = ? AND digest = ?", packageIdentity, packageDigest)
		if packageIdentity == "" {
			query = s.db.WithContext(ctx).Where("digest = ?", packageDigest)
		}
		if err := query.Order("identity").First(&packageRow).Error; err != nil {
			return nil, fmt.Errorf("load plugin %s generation package: %w", plugin.PluginID, err)
		}
		if packageRow.PluginID != plugin.PluginID {
			return nil, fmt.Errorf("%w: package plugin identity differs from installed plugin", ErrPluginGenerationConflict)
		}
		var manifest plugins.Manifest
		if err := json.Unmarshal([]byte(packageRow.ManifestJSON), &manifest); err != nil {
			return nil, fmt.Errorf("decode plugin %s generation manifest: %w", plugin.PluginID, err)
		}
		if manifest.ID != plugin.PluginID || manifest.Version != packageRow.Version {
			return nil, fmt.Errorf("%w: package manifest identity differs from durable projection", ErrPluginGenerationConflict)
		}
		if err := pluginsdk.ValidateHTTPBackendProviderManifest(manifest); err != nil {
			return nil, fmt.Errorf("%w: plugin %s HTTP backend provider manifest: %v", ErrPluginGenerationConflict, plugin.PluginID, err)
		}
		// WASM execution remains owned by the existing PluginPolicies projection.
		// Publishing it through both contracts would instantiate it twice.
		if !pluginManifestProjectsAgentRPC(manifest) {
			continue
		}
		artifact, err := s.selectPluginGenerationArtifact(ctx, packageRow, manifest, platform)
		if err != nil {
			return nil, fmt.Errorf("select plugin %s generation artifact: %w", plugin.PluginID, err)
		}
		grants, err := s.loadPluginGenerationGrants(ctx, plugin, packageRow)
		if err != nil {
			return nil, err
		}
		for _, instance := range targetedInstances {
			generation, err := BuildPluginGeneration(plugin, instance, packageRow, manifest, artifact, grants, agentID)
			if err != nil {
				return nil, err
			}
			result = append(result, generation)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].InstanceID != result[j].InstanceID {
			return result[i].InstanceID < result[j].InstanceID
		}
		return result[i].ID < result[j].ID
	})
	return result, nil
}

func (s *GormStore) selectPluginGenerationArtifact(ctx context.Context, packageRow PluginPackageRow, manifest plugins.Manifest, platform string) (PluginArtifactRow, error) {
	var artifacts []PluginArtifactRow
	if err := s.db.WithContext(ctx).Where("package_identity = ? AND package_digest = ?", packageRow.Identity, packageRow.Digest).Order("path").Find(&artifacts).Error; err != nil {
		return PluginArtifactRow{}, err
	}
	return SelectAgentFacePluginArtifact(artifacts, manifest, platform)
}

// SelectAgentFacePluginArtifact returns the durable runtime artifact used by an
// Agent generation. Dual-face packages keep the primary host_scope row; Agent
// snapshots still stamp HostScope=agent and match that primary row at issuance.
func SelectAgentFacePluginArtifact(artifacts []PluginArtifactRow, manifest plugins.Manifest, platform string) (PluginArtifactRow, error) {
	goos, goarch := splitPluginPlatform(platform)
	var fallback PluginArtifactRow
	for _, artifact := range artifacts {
		if !pluginGenerationArtifactMatchesRuntimeEntry(artifact, manifest) {
			continue
		}
		if manifest.Runtime.Kind == "rpc-service" && (artifact.GOOS != goos || artifact.GOARCH != goarch) {
			continue
		}
		if strings.TrimSpace(artifact.HostScope) == pluginsdk.HostScopeAgent {
			return artifact, nil
		}
		if fallback.ID == "" {
			fallback = artifact
		}
	}
	if fallback.ID == "" {
		return PluginArtifactRow{}, fmt.Errorf("%w: target artifact is unavailable for %q", ErrPluginGenerationConflict, platform)
	}
	return fallback, nil
}

// PrimaryPluginPackageArtifacts drops leftover Agent-face copies that older
// dual-face writes persisted in addition to the primary host_scope projection.
func PrimaryPluginPackageArtifacts(manifest plugins.Manifest, artifacts []PluginArtifactRow) []PluginArtifactRow {
	primary := strings.TrimSpace(manifest.Runtime.HostScope)
	if primary == pluginsdk.HostScopeAgent || !pluginsdk.RuntimeDeclaresHostScope(manifest.Runtime, pluginsdk.HostScopeAgent) {
		return artifacts
	}
	filtered := make([]PluginArtifactRow, 0, len(artifacts))
	for _, artifact := range artifacts {
		if strings.TrimSpace(artifact.HostScope) == pluginsdk.HostScopeAgent {
			continue
		}
		filtered = append(filtered, artifact)
	}
	return filtered
}

func pluginGenerationArtifactMatchesRuntimeEntry(artifact PluginArtifactRow, manifest plugins.Manifest) bool {
	if artifact.RuntimeKind != manifest.Runtime.Kind || artifact.RuntimeABI != manifest.Runtime.ABI {
		return false
	}
	if artifact.HostScope != manifest.Runtime.HostScope && !pluginsdk.RuntimeDeclaresHostScope(manifest.Runtime, artifact.HostScope) {
		return false
	}
	artifactPath := path.Clean(strings.TrimSpace(artifact.Path))
	entry := strings.TrimSpace(manifest.Runtime.Entry)
	if artifactPath == path.Clean(entry) {
		return true
	}
	logicalEntry := strings.TrimSuffix(path.Base(artifactPath), ".exe")
	return logicalEntry == entry
}

func (s *GormStore) loadPluginGenerationGrants(ctx context.Context, plugin InstalledPluginRow, packageRow PluginPackageRow) ([]PluginGenerationGrant, error) {
	if plugin.PendingOperationID != "" && plugin.StagedPackageIdentity == packageRow.Identity && plugin.StagedPackageDigest == packageRow.Digest {
		return decodePluginGenerationList[PluginGenerationGrant](plugin.PendingGrantsJSON)
	}
	var rows []PluginGrantRow
	if err := s.db.WithContext(ctx).Where("plugin_id = ? AND package_digest = ?", plugin.PluginID, packageRow.Digest).Order("permission, resource_selector").Find(&rows).Error; err != nil {
		return nil, err
	}
	grants := make([]PluginGenerationGrant, 0, len(rows))
	for _, row := range rows {
		if row.PackageIdentity != "" && row.PackageIdentity != packageRow.Identity {
			continue
		}
		resourceKind, resourceID := splitPluginGrantSelector(row.ResourceSelector)
		grants = append(grants, PluginGenerationGrant{Name: strings.TrimSpace(row.Permission), ResourceKind: resourceKind, ResourceID: resourceID})
	}
	return grants, nil
}

func BuildPluginGeneration(installed InstalledPluginRow, instance PluginInstanceRow, packageRow PluginPackageRow, manifest plugins.Manifest, artifact PluginArtifactRow, grants []PluginGenerationGrant, agentID string) (PluginGeneration, error) {
	config := instance.ConfigJSON
	configVersion := instance.ConfigVersion
	resourceGroupID := instance.ResourceGroupID
	secretHandlesJSON := instance.SecretHandlesJSON
	operationID := installed.PendingOperationID
	if operationID == "" {
		operationID = installed.LastOperationID
	}
	if instance.PendingOperationID != "" && instance.PendingOperationID == installed.PendingOperationID && instance.PendingVersion != 0 {
		config, configVersion = instance.PendingConfigJSON, instance.PendingVersion
		secretHandlesJSON = instance.PendingSecretHandlesJSON
		if instance.PendingResourceGroupID != "" {
			resourceGroupID = instance.PendingResourceGroupID
		}
	}
	canonicalConfig, err := canonicalPluginGenerationJSON(json.RawMessage(config), true)
	if err != nil {
		return PluginGeneration{}, fmt.Errorf("plugin instance %s config: %w", instance.ID, err)
	}
	secretHandles, err := decodePluginGenerationList[PluginGenerationSecretHandle](secretHandlesJSON)
	if err != nil {
		return PluginGeneration{}, fmt.Errorf("plugin instance %s secret handles: %w", instance.ID, err)
	}
	generation := PluginGeneration{
		OperationID: operationID, InstanceID: instance.ID, PluginID: packageRow.PluginID, PluginVersion: packageRow.Version, PackageDigest: packageRow.Digest,
		Artifact:             PluginGenerationArtifact{ArtifactID: artifact.ID, PackageIdentity: packageRow.Identity, RelativePath: artifact.Path, SHA256: artifact.SHA256, SizeBytes: artifact.SizeBytes, Mode: artifact.Mode, GOOS: artifact.GOOS, GOARCH: artifact.GOARCH, SignatureVerified: packageRow.SignatureVerdict == "verified", SignerKeyID: packageRow.SignatureKeyID, SignerFingerprint: packageRow.SignatureFingerprint},
		Runtime:              PluginGenerationRuntime{Kind: manifest.Runtime.Kind, ABI: manifest.Runtime.ABI, HostScope: pluginsdk.RuntimeAgentFaceHostScope(manifest.Runtime), Entry: artifact.Path},
		ExtensionPoints:      canonicalPluginGenerationStrings(manifest.ExtensionPoints),
		RequiredFeatures:     canonicalPluginGenerationStrings(pluginGenerationRequiredFeatures(grants, manifest.ExtensionPoints)),
		HTTPBackendProviders: append([]pluginsdk.HTTPBackendProviderDescriptor(nil), manifest.HTTPBackendProviders...),
		ConfigVersion:        configVersion, Config: canonicalConfig, Grants: append([]PluginGenerationGrant(nil), grants...), SecretHandles: secretHandles,
		ResourceBudget: PluginGenerationResourceBudget{TimeoutMS: manifest.ResourceBudget.TimeoutMS, MemoryBytes: manifest.ResourceBudget.MemoryBytes, Concurrency: manifest.ResourceBudget.Concurrency, InputBytes: manifest.ResourceBudget.InputBytes, OutputBytes: manifest.ResourceBudget.OutputBytes, CPUMillis: manifest.ResourceBudget.CPUMillis, Restarts: manifest.ResourceBudget.Restarts},
		Target:         PluginGenerationTarget{Kind: "agent", ID: agentID, ResourceGroupID: resourceGroupID, Version: configVersion},
		FailurePolicy:  PluginGenerationFailurePolicy{OnError: manifest.FailurePolicy.OnError, OnBudget: manifest.FailurePolicy.OnBudget, Restart: manifest.FailurePolicy.Restart, CoreFallback: manifest.FailurePolicy.CoreFallback},
	}
	canonicalizePluginGeneration(&generation, false)
	generation.ID, err = PluginGenerationIdentity(generation)
	if err != nil {
		return PluginGeneration{}, err
	}
	return generation, nil
}

func pluginManifestProjectsAgentRPC(manifest plugins.Manifest) bool {
	return strings.TrimSpace(manifest.Runtime.Kind) == pluginsdk.RuntimeRPCService && pluginsdk.RuntimeDeclaresHostScope(manifest.Runtime, pluginsdk.HostScopeAgent)
}

func pluginGenerationRequiredFeatures(grants []PluginGenerationGrant, extensionPoints []string) []string {
	scopes := make([]string, 0, len(grants))
	for _, grant := range grants {
		scopes = append(scopes, grant.Name)
	}
	return pluginsdk.RequiredRPCFeaturesForExtensions(scopes, extensionPoints)
}

func splitPluginGrantSelector(selector string) (string, string) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return "", ""
	}
	parts := strings.SplitN(selector, ":", 2)
	if len(parts) == 1 {
		return "", parts[0]
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}

func splitPluginPlatform(platform string) (string, string) {
	parts := strings.SplitN(strings.ToLower(strings.TrimSpace(platform)), "-", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return parts[0], parts[1]
}

func pluginGenerationContainsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func decodePluginGenerationList[T any](raw string) ([]T, error) {
	if strings.TrimSpace(raw) == "" {
		return []T{}, nil
	}
	var result []T
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return nil, err
	}
	if result == nil {
		result = []T{}
	}
	return result, nil
}

func stripPluginConfigGenerationKeys(raw json.RawMessage) json.RawMessage {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return raw
	}
	encoded, err := json.Marshal(stripPluginConfigGenerationValue(value))
	if err != nil {
		return raw
	}
	return encoded
}

func stripPluginConfigGenerationValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			if key == "generation" {
				continue
			}
			out[key] = stripPluginConfigGenerationValue(child)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, child := range typed {
			out[index] = stripPluginConfigGenerationValue(child)
		}
		return out
	default:
		return value
	}
}

func canonicalPluginGenerationJSON(raw json.RawMessage, requireObject bool) (json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		if requireObject {
			return nil, errors.New("JSON object is required")
		}
		return nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("trailing JSON value")
	}
	if requireObject {
		if _, ok := value.(map[string]any); !ok {
			return nil, errors.New("JSON object is required")
		}
	}
	return json.Marshal(value)
}

func canonicalPluginGenerationStrings(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			result = append(result, value)
		}
	}
	sort.Strings(result)
	if len(result) == 0 {
		return []string{}
	}
	write := 1
	for _, value := range result[1:] {
		if value != result[write-1] {
			result[write] = value
			write++
		}
	}
	return result[:write]
}

func canonicalizePluginGeneration(generation *PluginGeneration, stripRevision bool) {
	if stripRevision {
		generation.Revision = 0
	}
	generation.ExtensionPoints = canonicalPluginGenerationStrings(generation.ExtensionPoints)
	generation.RequiredFeatures = canonicalPluginGenerationStrings(generation.RequiredFeatures)
	sort.Slice(generation.HTTPBackendProviders, func(i, j int) bool {
		return generation.HTTPBackendProviders[i].ID < generation.HTTPBackendProviders[j].ID
	})
	if generation.HTTPBackendProviders == nil {
		generation.HTTPBackendProviders = []pluginsdk.HTTPBackendProviderDescriptor{}
	}
	sort.Slice(generation.Grants, func(i, j int) bool {
		if generation.Grants[i].Name != generation.Grants[j].Name {
			return generation.Grants[i].Name < generation.Grants[j].Name
		}
		if generation.Grants[i].ResourceKind != generation.Grants[j].ResourceKind {
			return generation.Grants[i].ResourceKind < generation.Grants[j].ResourceKind
		}
		return generation.Grants[i].ResourceID < generation.Grants[j].ResourceID
	})
	if generation.Grants == nil {
		generation.Grants = []PluginGenerationGrant{}
	}
	sort.Slice(generation.SecretHandles, func(i, j int) bool {
		if generation.SecretHandles[i].ID != generation.SecretHandles[j].ID {
			return generation.SecretHandles[i].ID < generation.SecretHandles[j].ID
		}
		return generation.SecretHandles[i].Version < generation.SecretHandles[j].Version
	})
	if generation.SecretHandles == nil {
		generation.SecretHandles = []PluginGenerationSecretHandle{}
	}
}

// CanonicalizePluginGeneration exposes the single generation canonicalizer to
// revision payload construction without duplicating ordering rules.
func CanonicalizePluginGeneration(generation *PluginGeneration, stripRevision bool) {
	canonicalizePluginGeneration(generation, stripRevision)
}

func PluginGenerationIdentity(generation PluginGeneration) (string, error) {
	generation.ID = ""
	generation.Revision = 0
	canonicalizePluginGeneration(&generation, false)
	config, err := canonicalPluginGenerationJSON(generation.Config, true)
	if err != nil {
		return "", err
	}
	// Config may echo the lifecycle generation after host injection. The
	// identity must stay stable before and after that echo is written.
	config, err = canonicalPluginGenerationJSON(stripPluginConfigGenerationKeys(config), true)
	if err != nil {
		return "", err
	}
	generation.Config = config
	payload, err := json.Marshal(generation)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func BuildPluginAgentRuntimeStatuses(agentID string, revision int64, generations []PluginGeneration) ([]PluginAgentRuntimeStatusRow, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" || revision <= 0 {
		return nil, errors.New("plugin generation target Agent and revision are required")
	}
	rows := make([]PluginAgentRuntimeStatusRow, 0, len(generations))
	for _, generation := range generations {
		row := PluginAgentRuntimeStatusRow{
			OperationID: generation.OperationID, AgentID: agentID, InstanceID: generation.InstanceID, PluginID: generation.PluginID,
			Revision: revision, GenerationID: generation.ID, PackageDigest: generation.PackageDigest,
			ArtifactDigest: generation.Artifact.SHA256, ConfigVersion: generation.ConfigVersion, ResourceGroupID: generation.Target.ResourceGroupID, TargetVersion: generation.Target.Version, AuthoritySlot: "pending",
		}
		if generation.Revision != revision || generation.Target.ID != agentID {
			return nil, ErrPluginGenerationConflict
		}
		if err := validatePluginAgentRuntimeStatusIdentity(row); err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// StagePluginAgentRuntimeStatuses installs the exact report fences before a
// candidate revision is dispatched. Repeating an identical stage is safe.
func (s *GormStore) StagePluginAgentRuntimeStatuses(ctx context.Context, rows []PluginAgentRuntimeStatusRow) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		for _, row := range rows {
			if err := validatePluginAgentRuntimeStatusIdentity(row); err != nil {
				return err
			}
			if row.State == "" {
				row.State = "applying"
			}
			if row.State != "applying" && row.State != "draining" {
				return errors.New("plugin Agent runtime status must start applying or draining")
			}
			row.DetailsJSON, row.BudgetJSON = "{}", "{}"
			row.AuthoritySlot = "pending"
			row.UpdatedAt = time.Now().UTC()
			if err := tx.Model(&PluginAgentRuntimeStatusRow{}).Where("agent_id = ? AND instance_id = ? AND authority_slot = ? AND operation_id <> ?", row.AgentID, row.InstanceID, "pending", row.OperationID).Update("authority_slot", "retired").Error; err != nil {
				return err
			}
			var current PluginAgentRuntimeStatusRow
			err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("operation_id = ? AND agent_id = ? AND instance_id = ?", row.OperationID, row.AgentID, row.InstanceID).First(&current).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				if err := tx.Create(&row).Error; err != nil {
					return err
				}
				continue
			}
			if err != nil {
				return err
			}
			if !samePluginAgentRuntimeFence(current, row) {
				return ErrPluginGenerationConflict
			}
		}
		return nil
	})
}

// RebaseInheritedPluginAgentRuntimeStatuses moves an existing pending runtime
// fence to a newer immutable revision when that revision carries the exact
// same plugin generation. This preserves two-phase plugin configuration across
// a superseding heartbeat or an unrelated configuration mutation without
// changing generation, config, secret, package, or target authority.
func (s *GormStore) RebaseInheritedPluginAgentRuntimeStatuses(ctx context.Context, agentID string, revision int64, generations []PluginGeneration, now time.Time) error {
	if s == nil {
		return gorm.ErrInvalidDB
	}
	if s.transactionScoped {
		return rebaseInheritedPluginAgentRuntimeStatusesTx(ctx, s.db, s.resolveAgentID(agentID), revision, generations, now)
	}
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		return rebaseInheritedPluginAgentRuntimeStatusesTx(ctx, tx, s.resolveAgentID(agentID), revision, generations, now)
	})
}

func rebaseInheritedPluginAgentRuntimeStatusesTx(ctx context.Context, tx *gorm.DB, agentID string, revision int64, generations []PluginGeneration, now time.Time) error {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" || revision <= 0 {
		return errors.New("inherited plugin runtime rebase Agent and revision are required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	rows, err := BuildPluginAgentRuntimeStatuses(agentID, revision, generations)
	if err != nil {
		return err
	}
	for _, expected := range rows {
		var current PluginAgentRuntimeStatusRow
		err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("operation_id = ? AND agent_id = ? AND instance_id = ?", expected.OperationID, expected.AgentID, expected.InstanceID).
			First(&current).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if current.Revision == revision {
			comparable := expected
			comparable.AuthoritySlot = current.AuthoritySlot
			if !samePluginAgentRuntimeFence(current, comparable) {
				return ErrPluginGenerationConflict
			}
			continue
		}
		if current.Revision > revision || current.AuthoritySlot != "pending" || current.State != "applying" {
			continue
		}
		comparable := expected
		comparable.Revision = current.Revision
		if !samePluginAgentRuntimeFence(current, comparable) {
			return ErrPluginGenerationConflict
		}
		var operation PluginOperationRow
		if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", current.OperationID).First(&operation).Error; err != nil {
			return err
		}
		if operation.PluginID != current.PluginID || (operation.Status != "applying" && operation.Status != "staged") || operation.CompletedAt != nil {
			continue
		}
		var installed InstalledPluginRow
		if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("plugin_id = ?", current.PluginID).First(&installed).Error; err != nil {
			return err
		}
		if installed.PendingOperationID != operation.ID || installed.PendingRevision != operation.TargetRevision {
			return ErrPluginGenerationConflict
		}
		var instance PluginInstanceRow
		if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND plugin_id = ?", current.InstanceID, current.PluginID).First(&instance).Error; err != nil {
			return err
		}
		if instance.PendingOperationID != operation.ID || instance.PendingVersion != current.ConfigVersion {
			return ErrPluginGenerationConflict
		}
		result := tx.WithContext(ctx).Model(&PluginAgentRuntimeStatusRow{}).
			Where("operation_id = ? AND agent_id = ? AND instance_id = ? AND revision = ? AND authority_slot = ?", current.OperationID, current.AgentID, current.InstanceID, current.Revision, "pending").
			Updates(map[string]any{
				"revision": revision, "state": "applying", "report_sequence": 0, "report_digest": "",
				"error_code": "", "details_json": "{}", "budget_json": "{}", "reported_at": nil, "updated_at": now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrPluginGenerationConflict
		}
		if operation.TargetRevision < revision {
			result := tx.WithContext(ctx).Model(&PluginOperationRow{}).
				Where("id = ? AND target_revision = ? AND completed_at IS NULL", operation.ID, operation.TargetRevision).
				Update("target_revision", revision)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrPluginGenerationConflict
			}
			operation.TargetRevision = revision
		}
		if installed.PendingRevision < revision {
			result := tx.WithContext(ctx).Model(&InstalledPluginRow{}).
				Where("plugin_id = ? AND pending_operation_id = ? AND pending_revision < ?", installed.PluginID, operation.ID, revision).
				Updates(map[string]any{"pending_revision": revision, "updated_at": now})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrPluginGenerationConflict
			}
		}
	}
	return nil
}

func (s *GormStore) RecordPluginAgentRuntimeReport(ctx context.Context, report PluginGenerationReport) (PluginAgentRuntimeStatusRow, bool, error) {
	report.State = strings.ToLower(strings.TrimSpace(report.State))
	report.OperationID = strings.TrimSpace(report.OperationID)
	report.AgentID = s.resolveAgentID(report.AgentID)
	report.InstanceID = strings.TrimSpace(report.InstanceID)
	report.PluginID = strings.TrimSpace(report.PluginID)
	report.GenerationID = strings.TrimSpace(report.GenerationID)
	report.PackageDigest = strings.ToLower(strings.TrimSpace(report.PackageDigest))
	report.ArtifactDigest = strings.ToLower(strings.TrimSpace(report.ArtifactDigest))
	report.ErrorCode = strings.TrimSpace(report.ErrorCode)
	if !validPluginGenerationReportState(report.State) || report.Sequence == 0 {
		return PluginAgentRuntimeStatusRow{}, false, errors.New("plugin generation report state and sequence are required")
	}
	details, err := canonicalPluginGenerationJSON(report.Details, false)
	if err != nil {
		return PluginAgentRuntimeStatusRow{}, false, fmt.Errorf("plugin generation report details: %w", err)
	}
	budget, err := canonicalPluginGenerationJSON(report.Budget, false)
	if err != nil {
		return PluginAgentRuntimeStatusRow{}, false, fmt.Errorf("plugin generation report budget: %w", err)
	}
	if details == nil {
		details = json.RawMessage(`{}`)
	}
	if detail := strings.TrimSpace(report.SafeDetail); detail != "" {
		if len(detail) > 512 || strings.ContainsAny(detail, "\r\n\x00") {
			return PluginAgentRuntimeStatusRow{}, false, errors.New("plugin generation report safe detail is invalid")
		}
		var detailFields map[string]any
		if err := json.Unmarshal(details, &detailFields); err != nil || detailFields == nil {
			return PluginAgentRuntimeStatusRow{}, false, errors.New("plugin generation report details must be an object")
		}
		detailFields["safe_detail"] = detail
		details, err = canonicalPluginGenerationJSON(mustPluginGenerationJSON(detailFields), false)
		if err != nil {
			return PluginAgentRuntimeStatusRow{}, false, err
		}
	}
	if budget == nil {
		budget = json.RawMessage(`{}`)
	}
	digestPayload := report
	digestPayload.Details, digestPayload.Budget = details, budget
	digestPayload.SafeDetail = ""
	digestPayload.ReportedAt = time.Time{}
	digestBytes, _ := json.Marshal(digestPayload)
	reportDigest := sha256.Sum256(digestBytes)
	digest := hex.EncodeToString(reportDigest[:])
	var result PluginAgentRuntimeStatusRow
	replayed := false
	err = s.writeTransaction(ctx, func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("operation_id = ? AND agent_id = ? AND instance_id = ?", report.OperationID, report.AgentID, report.InstanceID).First(&result).Error; err != nil {
			return err
		}
		if result.AuthoritySlot == "retired" {
			rearmed, err := rearmPluginRuntimeStatusForCoordinatorRetryTx(tx, result, time.Now().UTC())
			if err != nil {
				return err
			}
			if rearmed {
				if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("operation_id = ? AND agent_id = ? AND instance_id = ?", report.OperationID, report.AgentID, report.InstanceID).First(&result).Error; err != nil {
					return err
				}
			}
		}
		expected := PluginAgentRuntimeStatusRow{OperationID: report.OperationID, AgentID: report.AgentID, InstanceID: report.InstanceID, PluginID: report.PluginID, Revision: report.Revision, ConfigVersion: result.ConfigVersion, GenerationID: report.GenerationID, PackageDigest: report.PackageDigest, ArtifactDigest: report.ArtifactDigest, ResourceGroupID: result.ResourceGroupID, TargetVersion: result.TargetVersion, AuthoritySlot: result.AuthoritySlot}
		if !samePluginAgentRuntimeFence(result, expected) {
			return ErrPluginGenerationStale
		}
		if result.AuthoritySlot != "pending" && result.AuthoritySlot != "active" {
			return ErrPluginGenerationStale
		}
		if report.Sequence < result.ReportSequence {
			return ErrPluginGenerationStale
		}
		if report.Sequence == result.ReportSequence {
			if result.ReportDigest != digest {
				return ErrPluginGenerationConflict
			}
			replayed = true
			return nil
		}
		reportedAt := report.ReportedAt.UTC()
		if reportedAt.IsZero() {
			reportedAt = time.Now().UTC()
		}
		authoritySlot := result.AuthoritySlot
		if report.State == "active" {
			// An active report proves the candidate is ready, but does not cut
			// authority over before the multi-Agent operation commits. Existing
			// active generations remain authoritative throughout prepare/apply.
			if result.AuthoritySlot == "active" {
				authoritySlot = "active"
			}
		} else if report.State == "drained" {
			if err := tx.Model(&PluginAgentRuntimeStatusRow{}).Where("agent_id = ? AND instance_id = ? AND authority_slot = ?", report.AgentID, report.InstanceID, "active").Update("authority_slot", "retired").Error; err != nil {
				return err
			}
			authoritySlot = "retired"
		}
		updates := map[string]any{"state": report.State, "authority_slot": authoritySlot, "report_sequence": report.Sequence, "report_digest": digest, "error_code": report.ErrorCode, "details_json": string(details), "budget_json": string(budget), "reported_at": reportedAt, "updated_at": time.Now().UTC()}
		if err := tx.Model(&result).Updates(updates).Error; err != nil {
			return err
		}
		if err := tx.Where("operation_id = ? AND agent_id = ? AND instance_id = ?", report.OperationID, report.AgentID, report.InstanceID).First(&result).Error; err != nil {
			return err
		}
		message := report.State
		if report.SafeDetail != "" {
			message += ": " + report.SafeDetail
		} else if report.ErrorCode != "" {
			message += ": " + report.ErrorCode
		}
		message, truncated := sanitizePluginRuntimeLog(message)
		level := "info"
		if report.State == "degraded" {
			level = "warning"
		} else if report.State == "failed" {
			level = "error"
		}
		return appendPluginRuntimeLogTx(tx, &PluginRuntimeLogRow{
			InstanceID: report.InstanceID, PluginID: report.PluginID, AgentID: report.AgentID,
			ResourceGroupID: result.ResourceGroupID, OperationID: result.OperationID, GenerationID: result.GenerationID,
			Revision: result.Revision, PackageDigest: result.PackageDigest, ArtifactDigest: result.ArtifactDigest,
			Level: level, Message: message, Truncated: truncated, CreatedAt: reportedAt,
		})
	})
	return result, replayed, err
}

// A manual coordinator retry reissues the same immutable plugin generation.
// Its previous operation result remains historical, but the exact current
// desired revision must be allowed to report a fresh sequence from a restarted
// Agent runtime. The coordinator may commit the revision before asynchronous
// runtime status ingestion arrives, so the applied state remains admissible.
// Reports for any older or non-retrying revision stay retired.
func rearmPluginRuntimeStatusForCoordinatorRetryTx(tx *gorm.DB, status PluginAgentRuntimeStatusRow, now time.Time) (bool, error) {
	var pointer AgentRevisionPointerRow
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("agent_id = ?", status.AgentID).First(&pointer).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	if pointer.DesiredRevision != status.Revision {
		return false, nil
	}
	var revision AgentRevisionRow
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("agent_id = ? AND revision = ?", status.AgentID, status.Revision).First(&revision).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	if revision.RetryCycle <= 0 || (revision.State != AgentRevisionStatePending && revision.State != AgentRevisionStateApplying && revision.State != AgentRevisionStateApplied) {
		return false, nil
	}
	result := tx.Model(&PluginAgentRuntimeStatusRow{}).
		Where("operation_id = ? AND agent_id = ? AND instance_id = ? AND authority_slot = ?", status.OperationID, status.AgentID, status.InstanceID, "retired").
		Updates(map[string]any{
			"authority_slot": "pending", "state": "applying", "report_sequence": 0, "report_digest": "",
			"error_code": "", "details_json": "{}", "budget_json": "{}", "reported_at": nil, "updated_at": now,
		})
	return result.RowsAffected == 1, result.Error
}

func mustPluginGenerationJSON(value any) json.RawMessage {
	payload, _ := json.Marshal(value)
	return payload
}

func (s *GormStore) ListPluginAgentRuntimeStatuses(ctx context.Context, operationID string) ([]PluginAgentRuntimeStatusRow, error) {
	var rows []PluginAgentRuntimeStatusRow
	if err := s.db.WithContext(ctx).Where("operation_id = ?", strings.TrimSpace(operationID)).Order("agent_id, instance_id").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *GormStore) GetPluginAgentRuntimeStatusFence(ctx context.Context, operationID, agentID, instanceID string) (PluginAgentRuntimeStatusRow, bool, error) {
	var row PluginAgentRuntimeStatusRow
	db := s.db.WithContext(ctx)
	if s.transactionScoped {
		db = db.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	err := db.Where("operation_id = ? AND agent_id = ? AND instance_id = ?", operationID, agentID, instanceID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return PluginAgentRuntimeStatusRow{}, false, nil
	}
	return row, err == nil, err
}

// PluginSecretRedemptionTransaction binds generation authorization, secret
// resolution, usage audit, and lifecycle-state reads to one durable snapshot.
func (s *GormStore) PluginSecretRedemptionTransaction(ctx context.Context, redeem func(*GormStore) error) error {
	if redeem == nil {
		return gorm.ErrInvalidDB
	}
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		return redeem(s.transactionView(tx))
	})
}

func (s *GormStore) GetPluginOperation(ctx context.Context, operationID string) (PluginOperationRow, bool, error) {
	var row PluginOperationRow
	db := s.db.WithContext(ctx)
	if s.transactionScoped {
		db = db.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	err := db.Where("id = ?", strings.TrimSpace(operationID)).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return PluginOperationRow{}, false, nil
	}
	return row, err == nil, err
}

func validatePluginAgentRuntimeStatusIdentity(row PluginAgentRuntimeStatusRow) error {
	if strings.TrimSpace(row.OperationID) == "" || strings.TrimSpace(row.AgentID) == "" || strings.TrimSpace(row.InstanceID) == "" || strings.TrimSpace(row.PluginID) == "" || row.Revision <= 0 || !validSHA256(row.GenerationID) || !validSHA256(row.PackageDigest) || !validSHA256(row.ArtifactDigest) {
		return errors.New("complete plugin Agent runtime status identity is required")
	}
	return nil
}

func samePluginAgentRuntimeFence(left, right PluginAgentRuntimeStatusRow) bool {
	return left.OperationID == right.OperationID && left.AgentID == right.AgentID && left.InstanceID == right.InstanceID && left.PluginID == right.PluginID && left.Revision == right.Revision && left.ConfigVersion == right.ConfigVersion && left.ResourceGroupID == right.ResourceGroupID && left.TargetVersion == right.TargetVersion && left.AuthoritySlot == right.AuthoritySlot && strings.EqualFold(left.GenerationID, right.GenerationID) && strings.EqualFold(left.PackageDigest, right.PackageDigest) && strings.EqualFold(left.ArtifactDigest, right.ArtifactDigest)
}

func validPluginGenerationReportState(state string) bool {
	switch state {
	case "prepared", "active", "degraded", "failed", "drained":
		return true
	default:
		return false
	}
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func ValidPluginGenerationDigest(value string) bool { return validSHA256(value) }
