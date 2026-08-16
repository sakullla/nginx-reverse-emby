package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/authz"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/marketplace"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/revision"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/secrets"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

var (
	ErrPluginNotInstalled           = errors.New("plugin is not installed")
	ErrPluginInstanceNotFound       = errors.New("plugin instance is not found")
	ErrPluginPermissionConfirmation = errors.New("plugin permissions require exact administrator confirmation")
	ErrPluginRiskConfirmation       = errors.New("unofficial plugin source risk requires administrator confirmation")
	ErrPluginUninstallBlocked       = errors.New("plugin runtime must be disabled and drained before uninstall")
	ErrPluginResourceAuthorization  = errors.New("plugin target resource authorization denied")
	ErrPluginReadProjection         = errors.New("plugin read projection is invalid")
	ErrPluginConflict               = storage.ErrPluginConflict
	ErrPluginArtifactUnavailable    = errors.New("plugin artifact is unavailable for agent")
)

type PluginPackageCandidate struct {
	Package            plugins.ValidatedPackage
	Runtime            plugins.Runtime
	Artifacts          []plugins.Artifact
	SignatureTrust     marketplace.SignatureTrust
	CachePath          string
	validator          *plugins.Validator
	sourceID           string
	sourceKind         string
	sourceRiskLabel    string
	sourceRevision     uint64
	sourceRefKind      string
	sourceRefName      string
	sourceResolvedOID  string
	requireAcquisition bool
}

type PluginInstallRequest struct {
	Package              PluginPackageCandidate
	ActorID              string
	ConfirmedPermissions []string
	RiskAccepted         bool
}

type PluginConfigureRequest struct {
	PluginID           string
	InstanceID         string
	ResourceGroupID    string
	Targets            any
	PolicyChains       *[]string
	Bindings           *[]storage.PluginInstanceBindingRequest
	Config             json.RawMessage
	SecretReplacements map[string]json.RawMessage
	ActorID            string
	Actor              authz.Actor
}

type PluginDeleteInstanceRequest struct {
	PluginID   string
	InstanceID string
	ActorID    string
	Actor      authz.Actor
}

type PluginUpgradeRequest struct {
	PluginID             string
	Package              PluginPackageCandidate
	ActorID              string
	ConfirmedPermissions []string
	RiskAccepted         bool
}

type PluginRollbackRequest struct {
	PluginID             string
	ActorID              string
	ConfirmedPermissions []string
}

type PluginUninstallRequest struct {
	PluginID string
	ActorID  string
	// Drained is retained for wire compatibility only. Uninstall authority is
	// the durable, trusted lifecycle state produced by revision reconciliation.
	Drained bool
}

type PluginPermissionDiff struct {
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
}

type PluginPackageDetail struct {
	Digest         string                         `json:"digest"`
	Version        string                         `json:"version"`
	Runtime        plugins.Runtime                `json:"runtime"`
	Artifacts      []PluginArtifactDetail         `json:"artifacts"`
	ResourceBudget plugins.ResourceBudget         `json:"resource_budget"`
	FailurePolicy  plugins.FailurePolicy          `json:"failure_policy"`
	Signature      plugins.Signature              `json:"signature"`
	Manifest       plugins.Manifest               `json:"manifest"`
	ConfigSchema   map[string]any                 `json:"config_schema"`
	Permissions    []string                       `json:"permissions"`
	PermissionDiff PluginPermissionDiff           `json:"permission_diff"`
	DeclarativeUI  *plugins.DeclarativeUIDocument `json:"declarative_ui,omitempty"`
}

type PluginArtifactDetail struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Mode   string `json:"mode"`
	GOOS   string `json:"goos,omitempty"`
	GOARCH string `json:"goarch,omitempty"`
}

// AgentPluginArtifact is a revision-bound immutable artifact. Payload comes
// from the revision ledger rather than the mutable package cache.
type AgentPluginArtifact struct {
	Payload   []byte
	SHA256    string
	SizeBytes int64
}

type PluginAgentStatus struct {
	InstanceID        string          `json:"instance_id"`
	AgentID           string          `json:"agent_id"`
	TargetScope       string          `json:"target_scope"`
	Available         bool            `json:"available"`
	CurrentState      string          `json:"current_state"`
	StatusSummary     json.RawMessage `json:"status_summary"`
	OperationID       string          `json:"operation_id,omitempty"`
	OperationKind     string          `json:"operation_kind,omitempty"`
	OperationStatus   string          `json:"operation_status,omitempty"`
	TargetRevision    int64           `json:"target_revision,omitempty"`
	DesiredRevision   int             `json:"desired_revision,omitempty"`
	CurrentRevision   int             `json:"current_revision,omitempty"`
	LastApplyRevision int             `json:"last_apply_revision,omitempty"`
	LastApplyStatus   string          `json:"last_apply_status,omitempty"`
	LastApplyMessage  string          `json:"last_apply_message,omitempty"`
	GenerationID      string          `json:"generation_id,omitempty"`
	PackageDigest     string          `json:"package_digest,omitempty"`
	ArtifactDigest    string          `json:"artifact_digest,omitempty"`
	RuntimeState      string          `json:"runtime_state,omitempty"`
	RuntimeErrorCode  string          `json:"runtime_error_code,omitempty"`
	RuntimeDetails    json.RawMessage `json:"runtime_details,omitempty"`
	RuntimeBudget     json.RawMessage `json:"runtime_budget,omitempty"`
	ReportedAt        *time.Time      `json:"reported_at,omitempty"`
}

type PluginSummary struct {
	PluginID                  string    `json:"plugin_id"`
	ActivePackageDigest       string    `json:"active_package_digest"`
	RuntimeKind               string    `json:"runtime_kind"`
	RuntimeABI                string    `json:"runtime_abi"`
	HostScope                 string    `json:"host_scope"`
	ActiveSourceID            string    `json:"active_source_id,omitempty"`
	ActiveSourceKind          string    `json:"active_source_kind,omitempty"`
	ActiveSourceRiskLabel     string    `json:"active_source_risk_label,omitempty"`
	ActiveSourceRevision      uint64    `json:"active_source_revision,omitempty"`
	ActiveSourceRefKind       string    `json:"active_source_ref_kind,omitempty"`
	ActiveSourceRefName       string    `json:"active_source_ref_name,omitempty"`
	ActiveSourceResolvedOID   string    `json:"active_source_resolved_oid,omitempty"`
	StagedPackageDigest       string    `json:"staged_package_digest,omitempty"`
	StagedSourceID            string    `json:"staged_source_id,omitempty"`
	StagedSourceKind          string    `json:"staged_source_kind,omitempty"`
	StagedSourceRiskLabel     string    `json:"staged_source_risk_label,omitempty"`
	StagedSourceRevision      uint64    `json:"staged_source_revision,omitempty"`
	StagedSourceRefKind       string    `json:"staged_source_ref_kind,omitempty"`
	StagedSourceRefName       string    `json:"staged_source_ref_name,omitempty"`
	StagedSourceResolvedOID   string    `json:"staged_source_resolved_oid,omitempty"`
	RollbackPackageDigest     string    `json:"rollback_package_digest,omitempty"`
	RollbackSourceID          string    `json:"rollback_source_id,omitempty"`
	RollbackSourceKind        string    `json:"rollback_source_kind,omitempty"`
	RollbackSourceRiskLabel   string    `json:"rollback_source_risk_label,omitempty"`
	RollbackSourceRevision    uint64    `json:"rollback_source_revision,omitempty"`
	RollbackSourceRefKind     string    `json:"rollback_source_ref_kind,omitempty"`
	RollbackSourceRefName     string    `json:"rollback_source_ref_name,omitempty"`
	RollbackSourceResolvedOID string    `json:"rollback_source_resolved_oid,omitempty"`
	DesiredLifecycle          string    `json:"desired_lifecycle"`
	CurrentLifecycle          string    `json:"current_lifecycle"`
	LastOperationID           string    `json:"last_operation_id"`
	StateVersion              uint64    `json:"state_version"`
	PendingOperationID        string    `json:"pending_operation_id,omitempty"`
	PendingKind               string    `json:"pending_kind,omitempty"`
	PendingTargetDigest       string    `json:"pending_target_digest,omitempty"`
	PendingRevision           int64     `json:"pending_revision,omitempty"`
	InstalledAt               time.Time `json:"installed_at"`
	UpdatedAt                 time.Time `json:"updated_at"`
}

type PluginInstanceDetail struct {
	ID                     string                          `json:"id"`
	PluginID               string                          `json:"plugin_id"`
	ResourceGroupID        string                          `json:"resource_group_id"`
	Targets                []string                        `json:"targets"`
	PolicyChains           []string                        `json:"policy_chains"`
	Bindings               []storage.PluginInstanceBinding `json:"bindings"`
	Config                 json.RawMessage                 `json:"config"`
	SecretFields           []PluginSecretFieldState        `json:"secret_fields"`
	ConfigVersion          uint64                          `json:"config_version"`
	PendingConfig          json.RawMessage                 `json:"pending_config,omitempty"`
	PendingSecretFields    []PluginSecretFieldState        `json:"pending_secret_fields,omitempty"`
	PendingVersion         uint64                          `json:"pending_version,omitempty"`
	PendingOperationID     string                          `json:"pending_operation_id,omitempty"`
	PendingResourceGroupID string                          `json:"pending_resource_group_id,omitempty"`
	PendingTargets         []string                        `json:"pending_targets,omitempty"`
	PendingPolicyChains    []string                        `json:"pending_policy_chains,omitempty"`
	PendingBindings        []storage.PluginInstanceBinding `json:"pending_bindings,omitempty"`
	DesiredEnabled         bool                            `json:"desired_enabled"`
	CurrentState           string                          `json:"current_state"`
	StatusSummary          json.RawMessage                 `json:"status_summary"`
	StateVersion           uint64                          `json:"state_version"`
	UpdatedAt              time.Time                       `json:"updated_at"`
}

type PluginGrantDetail struct {
	PackageDigest    string    `json:"package_digest"`
	Permission       string    `json:"permission"`
	ResourceSelector string    `json:"resource_selector,omitempty"`
	GrantedBy        string    `json:"granted_by"`
	GrantedAt        time.Time `json:"granted_at"`
}

type PluginOperationDetail struct {
	ID                  string                       `json:"id"`
	PluginID            string                       `json:"plugin_id"`
	InstanceID          string                       `json:"instance_id,omitempty"`
	ResourceGroupID     string                       `json:"resource_group_id,omitempty"`
	Scopes              []PluginOperationScopeDetail `json:"scopes"`
	Kind                string                       `json:"kind"`
	Status              string                       `json:"status"`
	TargetPackageDigest string                       `json:"target_package_digest,omitempty"`
	TargetRevision      int64                        `json:"target_revision,omitempty"`
	AgentResults        json.RawMessage              `json:"agent_results"`
	ErrorClass          string                       `json:"error_class,omitempty"`
	Error               string                       `json:"error,omitempty"`
	ActorID             string                       `json:"actor_id"`
	SessionID           string                       `json:"session_id,omitempty"`
	CorrelationID       string                       `json:"correlation_id,omitempty"`
	SourceID            string                       `json:"source_id,omitempty"`
	SourceKind          string                       `json:"source_kind,omitempty"`
	SourceRiskLabel     string                       `json:"source_risk_label,omitempty"`
	CreatedAt           time.Time                    `json:"created_at"`
	CompletedAt         *time.Time                   `json:"completed_at,omitempty"`
}

type PluginOperationScopeDetail struct {
	InstanceID      string `json:"instance_id"`
	ResourceGroupID string `json:"resource_group_id"`
}

type PluginDetail struct {
	Plugin        PluginSummary          `json:"plugin"`
	Package       PluginPackageDetail    `json:"package"`
	Instances     []PluginInstanceDetail `json:"instances"`
	Grants        []PluginGrantDetail    `json:"grants"`
	AgentStatuses []PluginAgentStatus    `json:"agent_statuses"`
}

// PluginApplyResult is supplied only by the trusted revision/Agent reporting
// path. Every identity field must match the pending durable operation.
type PluginApplyResult struct {
	PluginID       string
	InstanceID     string
	OperationID    string
	TargetRevision int64
	TargetDigest   string
	ConfigVersion  uint64
	ActorID        string
	Applied        bool
	AgentResults   any
}

type pluginLifecycleStore interface {
	InstallPlugin(context.Context, storage.PluginInstallTransaction) error
	ApplyPluginMutation(context.Context, storage.PluginMutation) error
	RecordPluginOperation(context.Context, storage.PluginOperationRow, storage.AuditEventRow) error
	GetInstalledPlugin(context.Context, string) (storage.InstalledPluginRow, bool, error)
	ListInstalledPlugins(context.Context) ([]storage.InstalledPluginRow, error)
	GetPluginPackage(context.Context, string) (storage.PluginPackageRow, bool, error)
	GetPluginPackageByIdentity(context.Context, string) (storage.PluginPackageRow, bool, error)
	ListPluginArtifacts(context.Context, string) ([]storage.PluginArtifactRow, error)
	ListPluginArtifactsByIdentity(context.Context, string) ([]storage.PluginArtifactRow, error)
	ListPluginGrants(context.Context, string) ([]storage.PluginGrantRow, error)
	GetPluginInstance(context.Context, string) (storage.PluginInstanceRow, bool, error)
	ListPluginInstances(context.Context, string) ([]storage.PluginInstanceRow, error)
	ListPluginOperations(context.Context, string) ([]storage.PluginOperationRow, error)
	ListAgents(context.Context) ([]storage.AgentRow, error)
	LocalAgentBuild(context.Context) (string, string, bool, error)
}

type pluginRuntimeStatusStore interface {
	ListPluginAgentRuntimeStatuses(context.Context, string) ([]storage.PluginAgentRuntimeStatusRow, error)
}

type pluginDependencyConsumerStore interface {
	ResolvePluginInstanceBindingRequests(context.Context, []storage.PluginInstanceBindingRequest, string) ([]storage.PluginInstanceBinding, error)
}

type PluginService struct {
	store            pluginLifecycleStore
	validator        *plugins.Validator
	cacheRoot        string
	now              func() time.Time
	cfg              config.Config
	mutationExecutor *revision.Executor
	revisionMutation bool
	revisionNumbers  map[string]int64
	secretVault      *secrets.Vault
}

func (s *PluginService) SetSecretVault(vault *secrets.Vault) {
	if s != nil {
		s.secretVault = vault
	}
}

type controlPlanePluginRuntimePlan struct {
	Controlled      bool
	Candidates      []pluginhost.Candidate
	StopInstanceIDs []string
}

func NewPluginService(store pluginLifecycleStore, cacheRoot string) *PluginService {
	return NewPluginServiceWithValidator(store, plugins.NewValidator(plugins.ValidatorOptions{HostVersion: "0.0.0-dev"}), cacheRoot)

}

func NewPluginServiceWithValidator(store pluginLifecycleStore, validator *plugins.Validator, cacheRoot string) *PluginService {
	return &PluginService{store: store, validator: validator, cacheRoot: cacheRoot, now: func() time.Time { return time.Now().UTC() }}
}

func (s *PluginService) ConfigureRevisionMutations(cfg config.Config, store revision.Store) {
	if s == nil {
		return
	}
	s.cfg = cfg
	s.mutationExecutor = newMutationExecutor(cfg, store)
}

func (s *PluginService) installedPlugin(ctx context.Context, pluginID string) (storage.InstalledPluginRow, error) {
	installed, ok, err := s.store.GetInstalledPlugin(ctx, pluginID)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	if !ok {
		return storage.InstalledPluginRow{}, ErrPluginNotInstalled
	}
	return installed, nil
}

func (s *PluginService) List(ctx context.Context) ([]PluginSummary, error) {
	rows, err := s.store.ListInstalledPlugins(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]PluginSummary, 0, len(rows))
	for _, row := range rows {
		result = append(result, pluginSummary(row))
	}
	return result, nil
}

func (s *PluginService) Detail(ctx context.Context, pluginID string) (PluginDetail, error) {
	installed, err := s.installedPlugin(ctx, pluginID)
	if err != nil {
		return PluginDetail{}, err
	}
	packageRow, ok, err := s.storedPackage(ctx, installed.ActivePackageIdentity, installed.ActivePackageDigest)
	if err != nil {
		return PluginDetail{}, err
	}
	if !ok {
		return PluginDetail{}, errors.New("active plugin package is unavailable")
	}
	if err := s.validateStoredPackageIntegrity(ctx, packageRow); err != nil {
		return PluginDetail{}, err
	}
	instances, err := s.store.ListPluginInstances(ctx, pluginID)
	if err != nil {
		return PluginDetail{}, err
	}
	grants, err := s.store.ListPluginGrants(ctx, pluginID)
	if err != nil {
		return PluginDetail{}, err
	}
	artifacts, err := s.storedArtifacts(ctx, installed.ActivePackageIdentity, installed.ActivePackageDigest)
	if err != nil {
		return PluginDetail{}, err
	}
	packageDetail, err := pluginPackageDetail(packageRow, artifacts, grants, installed.ActivePackageDigest)
	if err != nil {
		return PluginDetail{}, err
	}
	if packageDetail.DeclarativeUI == nil && packageDetail.Manifest.UISchema != "" {
		validated, err := s.loadValidatedCapabilityPackage(ctx, packageRow)
		if err != nil {
			return PluginDetail{}, err
		}
		packageDetail.DeclarativeUI = validated.DeclarativeUI
	}
	instanceDetails, err := s.pluginInstanceDetails(ctx, instances)
	if err != nil {
		return PluginDetail{}, err
	}
	grantDetails := pluginGrantDetails(grants)
	operations, err := s.store.ListPluginOperations(ctx, pluginID)
	if err != nil {
		return PluginDetail{}, err
	}
	agentStatuses, err := s.pluginAgentStatuses(ctx, installed, instances, operations)
	if err != nil {
		return PluginDetail{}, err
	}
	return PluginDetail{Plugin: pluginSummary(installed), Package: packageDetail, Instances: instanceDetails, Grants: grantDetails, AgentStatuses: agentStatuses}, nil
}

func (s *PluginService) PackageDetail(ctx context.Context, candidate PluginPackageCandidate, pluginID string) (PluginPackageDetail, error) {
	if err := s.validatePackageCandidate(candidate); err != nil {
		return PluginPackageDetail{}, err
	}
	if pluginID != "" && pluginID != candidate.Package.Manifest.ID {
		return PluginPackageDetail{}, fmt.Errorf("%w: package plugin identity does not match requested plugin", ErrInvalidArgument)
	}
	grants, activeDigest := []storage.PluginGrantRow(nil), ""
	if strings.TrimSpace(pluginID) != "" {
		installed, ok, err := s.store.GetInstalledPlugin(ctx, pluginID)
		if err != nil {
			return PluginPackageDetail{}, err
		}
		if ok {
			activeDigest = installed.ActivePackageDigest
			grants, err = s.store.ListPluginGrants(ctx, pluginID)
			if err != nil {
				return PluginPackageDetail{}, err
			}
		}
	}
	manifestJSON, err := json.Marshal(candidate.Package.Manifest)
	if err != nil {
		return PluginPackageDetail{}, err
	}
	schemaJSON, err := json.Marshal(candidate.Package.ConfigSchema)
	if err != nil {
		return PluginPackageDetail{}, err
	}
	uiJSON, err := json.Marshal(candidate.Package.DeclarativeUI)
	if err != nil {
		return PluginPackageDetail{}, err
	}
	if candidate.Package.DeclarativeUI == nil {
		uiJSON = nil
	}
	packageRow, artifacts, err := storage.ProjectPluginPackage(storage.PluginPackageRow{Digest: candidate.Package.Digest, Version: candidate.Package.Manifest.Version, ManifestJSON: string(manifestJSON), ConfigSchemaJSON: string(schemaJSON), UISchemaJSON: string(uiJSON)}, candidate.Package.Manifest)
	if err != nil {
		return PluginPackageDetail{}, err
	}
	return pluginPackageDetail(packageRow, artifacts, grants, activeDigest)
}

func (s *PluginService) Operations(ctx context.Context, pluginID string) ([]PluginOperationDetail, error) {
	rows, err := s.store.ListPluginOperations(ctx, pluginID)
	if err != nil {
		return nil, err
	}
	scopesByOperation := make(map[string][]PluginOperationScopeDetail)
	if scopeStore, ok := s.store.(interface {
		ListPluginOperationScopes(context.Context, string) ([]storage.PluginOperationScopeRow, error)
	}); ok {
		scopes, err := scopeStore.ListPluginOperationScopes(ctx, pluginID)
		if err != nil {
			return nil, err
		}
		for _, scope := range scopes {
			scopesByOperation[scope.OperationID] = append(scopesByOperation[scope.OperationID], PluginOperationScopeDetail{InstanceID: scope.InstanceID, ResourceGroupID: scope.ResourceGroupID})
		}
	}
	result := make([]PluginOperationDetail, 0, len(rows))
	for _, row := range rows {
		agentResults, err := pluginReadStatusObject(row.AgentResultsJSON)
		if err != nil {
			return nil, err
		}
		scopes := scopesByOperation[row.ID]
		if scopes == nil && row.InstanceID != "" && row.ResourceGroupID != "" {
			scopes = []PluginOperationScopeDetail{{InstanceID: row.InstanceID, ResourceGroupID: row.ResourceGroupID}}
		}
		if scopes == nil {
			scopes = []PluginOperationScopeDetail{}
		}
		result = append(result, PluginOperationDetail{ID: row.ID, PluginID: row.PluginID, InstanceID: row.InstanceID, ResourceGroupID: row.ResourceGroupID, Scopes: scopes, Kind: row.Kind, Status: row.Status, TargetPackageDigest: row.TargetPackageDigest, TargetRevision: row.TargetRevision, AgentResults: agentResults, ErrorClass: row.ErrorClass, Error: row.Error, ActorID: row.ActorID, SessionID: row.SessionID, CorrelationID: row.CorrelationID, SourceID: row.SourceID, SourceKind: row.SourceKind, SourceRiskLabel: row.SourceRiskLabel, CreatedAt: row.CreatedAt, CompletedAt: row.CompletedAt})
	}
	return result, nil
}

type agentRevisionPluginArtifactStore interface {
	ResolveAgentRevisionPolicyArtifact(context.Context, string, int64, string, string) (storage.GenerationArtifactRow, bool, error)
}

// ResolveAgentPluginArtifact binds an opaque artifact ID to the requesting
// Agent's immutable issued revision. Later disable, retarget, upgrade or
// uninstall operations cannot revoke a valid retry while that revision is
// retained.
func (s *PluginService) ResolveAgentPluginArtifact(ctx context.Context, agentID string, revision int64, snapshotDigest, artifactID string) (AgentPluginArtifact, error) {
	artifactID = strings.TrimSpace(artifactID)
	if revision <= 0 || len(strings.TrimSpace(snapshotDigest)) != 64 {
		return AgentPluginArtifact{}, ErrPluginArtifactUnavailable
	}
	if err := storage.ValidatePluginPolicyIdentity(artifactID); err != nil {
		return AgentPluginArtifact{}, ErrPluginArtifactUnavailable
	}
	revisionStore, ok := s.store.(agentRevisionPluginArtifactStore)
	if !ok {
		return AgentPluginArtifact{}, ErrPluginArtifactUnavailable
	}
	artifact, found, err := revisionStore.ResolveAgentRevisionPolicyArtifact(ctx, agentID, revision, snapshotDigest, artifactID)
	if err != nil {
		return AgentPluginArtifact{}, err
	}
	if !found {
		return AgentPluginArtifact{}, ErrPluginArtifactUnavailable
	}
	return AgentPluginArtifact{Payload: append([]byte(nil), artifact.Payload...), SHA256: strings.ToLower(artifact.SHA256), SizeBytes: artifact.SizeBytes}, nil
}

func (s *PluginService) InstallMutation(ctx context.Context, request PluginInstallRequest) (PluginSummary, error) {
	row, err := s.Install(ctx, request)
	return pluginSummary(row), err
}

func (s *PluginService) EnableMutation(ctx context.Context, pluginID, actorID string) (PluginSummary, error) {
	if s.mutationExecutor == nil || s.revisionMutation {
		row, err := s.Enable(ctx, pluginID, actorID)
		return pluginSummary(row), err
	}
	var row storage.InstalledPluginRow
	err := s.executeRevisionLifecycleMutation(ctx, pluginID, "plugin.enable", struct {
		PluginID string `json:"plugin_id"`
	}{pluginID}, nil, func(txService *PluginService, mutationCtx context.Context) error {
		var err error
		row, err = txService.Enable(mutationCtx, pluginID, actorID)
		return err
	})
	return pluginSummary(row), err
}

func (s *PluginService) DisableMutation(ctx context.Context, pluginID, actorID string) (PluginSummary, error) {
	if s.mutationExecutor == nil || s.revisionMutation {
		row, err := s.Disable(ctx, pluginID, actorID)
		return pluginSummary(row), err
	}
	var row storage.InstalledPluginRow
	err := s.executeRevisionLifecycleMutation(ctx, pluginID, "plugin.disable", struct {
		PluginID string `json:"plugin_id"`
	}{pluginID}, nil, func(txService *PluginService, mutationCtx context.Context) error {
		var err error
		row, err = txService.Disable(mutationCtx, pluginID, actorID)
		return err
	})
	return pluginSummary(row), err
}

func (s *PluginService) ConfigureMutation(ctx context.Context, request PluginConfigureRequest) (PluginInstanceDetail, error) {
	if s.mutationExecutor != nil && !s.revisionMutation {
		var prospective PluginInstanceDetail
		err := s.executeRevisionLifecycleMutation(ctx, request.PluginID, "plugin.configure", request, &request, func(txService *PluginService, mutationCtx context.Context) error {
			_, err := txService.configureWithProspectiveDetail(mutationCtx, request, &prospective)
			return err
		})
		return prospective, err
	}
	var prospective PluginInstanceDetail
	_, err := s.configureWithProspectiveDetail(ctx, request, &prospective)
	if err != nil {
		return PluginInstanceDetail{}, err
	}
	return prospective, nil
}

func (s *PluginService) DeleteInstanceMutation(ctx context.Context, request PluginDeleteInstanceRequest) error {
	if s.mutationExecutor == nil || s.revisionMutation {
		return s.DeleteInstance(ctx, request)
	}
	return s.executeRevisionLifecycleMutation(ctx, request.PluginID, "plugin.delete-instance", request, nil, func(txService *PluginService, mutationCtx context.Context) error {
		return txService.DeleteInstance(mutationCtx, request)
	})
}

func (s *PluginService) UpgradeMutation(ctx context.Context, request PluginUpgradeRequest) (PluginSummary, error) {
	if s.mutationExecutor == nil || s.revisionMutation {
		row, err := s.Upgrade(ctx, request)
		return pluginSummary(row), err
	}
	var row storage.InstalledPluginRow
	err := s.executeRevisionLifecycleMutation(ctx, request.PluginID, "plugin.upgrade", request, nil, func(txService *PluginService, mutationCtx context.Context) error {
		var err error
		row, err = txService.Upgrade(mutationCtx, request)
		return err
	})
	return pluginSummary(row), err
}

func (s *PluginService) RollbackMutation(ctx context.Context, request PluginRollbackRequest) (PluginSummary, error) {
	if s.mutationExecutor == nil || s.revisionMutation {
		row, err := s.Rollback(ctx, request)
		return pluginSummary(row), err
	}
	var row storage.InstalledPluginRow
	err := s.executeRevisionLifecycleMutation(ctx, request.PluginID, "plugin.rollback", request, nil, func(txService *PluginService, mutationCtx context.Context) error {
		var err error
		row, err = txService.Rollback(mutationCtx, request)
		return err
	})
	return pluginSummary(row), err
}

type pluginLifecycleOperationContextKey struct{}

func (s *PluginService) executeRevisionLifecycleMutation(
	ctx context.Context,
	pluginID, kind string,
	request any,
	configure *PluginConfigureRequest,
	mutate func(*PluginService, context.Context) error,
) error {
	if s == nil || s.mutationExecutor == nil || mutate == nil {
		return errors.New("plugin revision mutation is unavailable")
	}
	operationID := lifecycleID("pluginop")
	targetIDs, err := s.pluginLifecycleTargetIDs(ctx, pluginID, configure)
	if err != nil {
		return err
	}
	dependencyAction := revision.DependencyActionApply
	if strings.HasSuffix(kind, ".disable") || strings.HasSuffix(kind, ".delete-instance") {
		dependencyAction = revision.DependencyActionDelete
	}
	mutationCtx := context.WithValue(ctx, pluginLifecycleOperationContextKey{}, operationID)
	_, err = s.mutationExecutor.Execute(mutationCtx, revision.MutationRequest{
		OperationID:      operationID,
		Kind:             kind,
		DependencyAction: dependencyAction,
		Request:          request,
		Targets:          configMutationTargets(s.cfg, targetIDs, nil),
		ResourceState: func(ctx context.Context, tx *storage.GormStore, _ revision.Target) (any, error) {
			installed, found, err := tx.GetInstalledPlugin(ctx, pluginID)
			if err != nil {
				return nil, err
			}
			instances, err := tx.ListPluginInstances(ctx, pluginID)
			if err != nil {
				return nil, err
			}
			return struct {
				Found     bool                        `json:"found"`
				Installed storage.InstalledPluginRow  `json:"installed"`
				Instances []storage.PluginInstanceRow `json:"instances"`
			}{Found: found, Installed: installed, Instances: instances}, nil
		},
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			txService := &PluginService{
				store: tx, validator: s.validator, cacheRoot: s.cacheRoot, now: s.now, cfg: s.cfg,
				revisionMutation: true, revisionNumbers: revisions,
			}
			return mutate(txService, ctx)
		},
	})
	return err
}

func (s *PluginService) pluginLifecycleTargetIDs(ctx context.Context, pluginID string, configure *PluginConfigureRequest) ([]string, error) {
	defaultTargetID, err := s.defaultPluginTargetID(ctx)
	if err != nil {
		return nil, err
	}
	instances, err := s.store.ListPluginInstances(ctx, pluginID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0)
	for _, instance := range instances {
		active, err := pluginTargetIDs(json.RawMessage(instance.TargetJSON), defaultTargetID)
		if err != nil {
			return nil, err
		}
		ids = append(ids, active...)
		if strings.TrimSpace(instance.PendingTargetJSON) != "" {
			pending, err := pluginTargetIDs(json.RawMessage(instance.PendingTargetJSON), defaultTargetID)
			if err != nil {
				return nil, err
			}
			ids = append(ids, pending...)
		}
	}
	if configure != nil {
		payload, err := json.Marshal(configure.Targets)
		if err != nil {
			return nil, err
		}
		requested, err := pluginTargetIDs(payload, defaultTargetID)
		if err != nil {
			return nil, err
		}
		ids = append(ids, requested...)
	}
	ids = uniqueAgentIDs(ids)
	if len(ids) == 0 {
		ids = []string{defaultTargetID}
	}
	return ids, nil
}

func pluginLifecycleTargetRevision(revisions map[string]int64, fallback int64) int64 {
	if len(revisions) == 0 {
		return fallback
	}
	result := int64(0)
	for _, revision := range revisions {
		if revision > result {
			result = revision
		}
	}
	return result
}

func (s *PluginService) Install(ctx context.Context, request PluginInstallRequest) (storage.InstalledPluginRow, error) {
	manifest := request.Package.Package.Manifest
	operation := s.operation(ctx, manifest.ID, "install", request.Package.Package.Digest, request.ActorID)
	if err := bindOperationCandidate(&operation, request.Package); err != nil {
		return storage.InstalledPluginRow{}, err
	}
	if err := s.validatePackageCandidate(request.Package); err != nil {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
	}
	if err := confirmCandidateSource(request.Package, request.RiskAccepted); err != nil {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
	}
	if err := confirmPermissions(manifest.Permissions, request.ConfirmedPermissions, true); err != nil {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
	}
	now := s.now()
	operation.Status = "succeeded"
	operation.CompletedAt = &now
	manifestJSON, _ := json.Marshal(manifest)
	schemaJSON, _ := json.Marshal(request.Package.Package.ConfigSchema)
	cleanupJSON, _ := json.Marshal(manifest.Cleanup)
	packageRow, artifacts, projectionErr := pluginPackageRows(request.Package, manifestJSON, schemaJSON, now)
	if projectionErr != nil {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, request.ActorID, projectionErr)
	}
	installed := storage.InstalledPluginRow{PluginID: manifest.ID, ActivePackageDigest: strings.ToLower(request.Package.Package.Digest), ActivePackageIdentity: packageRow.Identity, RuntimeKind: manifest.Runtime.Kind, RuntimeABI: manifest.Runtime.ABI, HostScope: manifest.Runtime.HostScope, ActiveSourceID: packageRow.SourceID, ActiveSourceKind: packageRow.SourceKind, ActiveSourceRiskLabel: packageRow.SourceRiskLabel, ActiveSourceRevision: packageRow.SourceRevision, ActiveSourceRefKind: packageRow.SourceRefKind, ActiveSourceRefName: packageRow.SourceRefName, ActiveSourceResolvedOID: packageRow.SourceResolvedOID, ActiveSignatureKeyID: packageRow.SignatureKeyID, ActiveSignaturePublicKey: packageRow.SignaturePublicKey, ActiveSignatureFingerprint: packageRow.SignatureFingerprint, DesiredLifecycle: "disabled", CurrentLifecycle: "disabled", CleanupPolicyJSON: string(cleanupJSON), LastOperationID: operation.ID, StateVersion: 1, InstalledAt: now, UpdatedAt: now}
	if err := storage.BindPluginOperationPackage(&operation, packageRow); err != nil {
		return storage.InstalledPluginRow{}, err
	}
	grants := grantRows(manifest.ID, installed.ActivePackageDigest, installed.ActivePackageIdentity, manifest.Permissions, operation.ActorID, now)
	transaction := storage.PluginInstallTransaction{Package: packageRow, Artifacts: artifacts, Installed: installed, Grants: grants, Operation: operation, Audit: pluginLifecycleAudit(operation, request.ActorID, "success", "", now)}
	transaction.RequireAcquisition, transaction.AcquisitionSourceID, transaction.AcquisitionDigest = request.Package.requireAcquisition, request.Package.sourceID, request.Package.Package.Digest
	if err := s.store.InstallPlugin(ctx, transaction); err != nil {
		return storage.InstalledPluginRow{}, err
	}
	return installed, nil
}

func (s *PluginService) Enable(ctx context.Context, pluginID, actorID string) (storage.InstalledPluginRow, error) {
	return s.setLifecycle(ctx, pluginID, actorID, "enable", "enabled", "active")
}

func (s *PluginService) Disable(ctx context.Context, pluginID, actorID string) (storage.InstalledPluginRow, error) {
	return s.setLifecycle(ctx, pluginID, actorID, "disable", "disabled", "disabled")
}

func (s *PluginService) setLifecycle(ctx context.Context, pluginID, actorID, kind, desired, current string) (storage.InstalledPluginRow, error) {
	installed, ok, err := s.store.GetInstalledPlugin(ctx, pluginID)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	operation := s.operation(ctx, pluginID, kind, installed.ActivePackageDigest, actorID)
	if !ok {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, ErrPluginNotInstalled)
	}
	if err := bindInstalledActiveOperation(&operation, installed); err != nil {
		return storage.InstalledPluginRow{}, err
	}
	var packageRow storage.PluginPackageRow
	if kind != "disable" {
		var exists bool
		packageRow, exists, err = s.storedPackage(ctx, installed.ActivePackageIdentity, installed.ActivePackageDigest)
		if err != nil {
			return storage.InstalledPluginRow{}, err
		}
		if !exists {
			return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, errors.New("active plugin package is unavailable"))
		}
		if err := s.revalidateInstalledPackageVariant(ctx, installed.ActivePackageIdentity, installed.ActivePackageDigest); err != nil {
			return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, err)
		}
	}
	instances, err := s.store.ListPluginInstances(ctx, pluginID)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	if kind == "enable" {
		var manifest plugins.Manifest
		if err := json.Unmarshal([]byte(packageRow.ManifestJSON), &manifest); err != nil {
			return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, err)
		}
		for _, instance := range instances {
			if err := s.validateAgentTargets(ctx, manifest.Compatibility.Agent, json.RawMessage(instance.TargetJSON)); err != nil {
				return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, err)
			}
		}
	}
	if installed.DesiredLifecycle == desired && (installed.CurrentLifecycle == current || installed.CurrentLifecycle == "applying") {
		return installed, nil
	}
	if err := ensureNoPendingOperation(installed); err != nil {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, err)
	}
	now := s.now()
	for index := range instances {
		instances[index].DesiredEnabled = desired == "enabled"
		instances[index].CurrentState = "applying"
		instances[index].UpdatedAt = now
	}
	installed.DesiredLifecycle, installed.CurrentLifecycle, installed.UpdatedAt = desired, "applying", now
	installed.LastOperationID = operation.ID
	operation.TargetRevision = pluginLifecycleTargetRevision(s.revisionNumbers, int64(installed.StateVersion+1))
	setPendingOperation(&installed, operation)
	operation.Status = "applying"
	err = s.store.ApplyPluginMutation(ctx, storage.PluginMutation{PluginID: pluginID, ExpectedActive: installed.ActivePackageDigest, ExpectedStateVersion: installed.StateVersion, Installed: &installed, ReplaceInstances: instances, Operation: operation, Audit: pluginLifecycleAudit(operation, actorID, "accepted", "", now)})
	return installed, err
}

// CompleteLifecycleApply records the Agent/revision result separately from the
// desired-state mutation. A failed apply never changes the active package.
func (s *PluginService) CompleteLifecycleApply(ctx context.Context, applyResult PluginApplyResult) (storage.InstalledPluginRow, error) {
	installed, ok, err := s.store.GetInstalledPlugin(ctx, applyResult.PluginID)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	if !ok {
		return storage.InstalledPluginRow{}, ErrPluginNotInstalled
	}
	operation, err := s.pendingOperation(ctx, installed, applyResult, "enable", "disable")
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	encodedResults, err := encodePluginResultObject(applyResult.AgentResults)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	now := s.now()
	instances, err := s.store.ListPluginInstances(ctx, installed.PluginID)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	operation.AgentResultsJSON, operation.CompletedAt = encodedResults, &now
	if applyResult.Applied {
		operation.Status = "succeeded"
		if installed.DesiredLifecycle == "enabled" {
			installed.CurrentLifecycle = "active"
		} else {
			installed.CurrentLifecycle = "disabled"
		}
		for index := range instances {
			instances[index].DesiredEnabled = installed.DesiredLifecycle == "enabled"
			instances[index].CurrentState = lifecycleCurrentState(installed.DesiredLifecycle)
			instances[index].UpdatedAt = now
		}
	} else {
		operation.Status, operation.ErrorClass, operation.Error = "failed", "agent_apply", "one or more target agents failed to apply plugin lifecycle"
		installed.CurrentLifecycle = "degraded"
		for index := range instances {
			instances[index].CurrentState = "degraded"
			instances[index].UpdatedAt = now
		}
	}
	clearPendingOperation(&installed)
	installed.LastOperationID, installed.UpdatedAt = operation.ID, now
	auditResult := "success"
	if !applyResult.Applied {
		auditResult = "failure"
	}
	err = s.store.ApplyPluginMutation(ctx, storage.PluginMutation{PluginID: installed.PluginID, ExpectedActive: installed.ActivePackageDigest, ExpectedStateVersion: installed.StateVersion, ExpectedPendingOperationID: operation.ID, Installed: &installed, ReplaceInstances: instances, Operation: operation, CompleteOperation: true, Audit: pluginLifecycleAudit(operation, applyResult.ActorID, auditResult, operation.ErrorClass, now)})
	return installed, err
}

// CompleteTrustedRevisionOperation closes a plugin operation that has no
// dedicated RPC runtime status rows (for example a WASM policy generation or
// a configuration change while disabled). The caller must have already
// authenticated and reconciled every revision belonging to operation.
func (s *PluginService) CompleteTrustedRevisionOperation(ctx context.Context, operation storage.PluginOperationRow, applied bool, agentResults any) error {
	installed, ok, err := s.store.GetInstalledPlugin(ctx, operation.PluginID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrPluginNotInstalled
	}
	if operation.ID == "" || installed.PendingOperationID != operation.ID || operation.TargetRevision <= 0 {
		return fmt.Errorf("%w: trusted plugin revision operation is stale", ErrPluginConflict)
	}
	result := PluginApplyResult{
		PluginID: operation.PluginID, OperationID: operation.ID,
		TargetRevision: operation.TargetRevision, TargetDigest: operation.TargetPackageDigest,
		ActorID: operation.ActorID, Applied: applied, AgentResults: agentResults,
	}
	switch operation.Kind {
	case "enable", "disable":
		_, err = s.CompleteLifecycleApply(ctx, result)
	case "configure":
		instances, listErr := s.store.ListPluginInstances(ctx, operation.PluginID)
		if listErr != nil {
			return listErr
		}
		for _, instance := range instances {
			if instance.PendingOperationID != operation.ID {
				continue
			}
			if result.InstanceID != "" {
				return storage.ErrPluginGenerationConflict
			}
			result.InstanceID, result.ConfigVersion = instance.ID, instance.PendingVersion
		}
		if result.InstanceID == "" || result.ConfigVersion == 0 {
			return storage.ErrPluginGenerationStale
		}
		_, err = s.CompleteConfigure(ctx, result)
	case "upgrade":
		_, err = s.CompleteUpgrade(ctx, result)
	case "rollback":
		_, err = s.CompleteRollback(ctx, result)
	default:
		return fmt.Errorf("unsupported trusted plugin revision operation %q", operation.Kind)
	}
	return err
}

func (s *PluginService) controlPlaneRuntimePlan(ctx context.Context, operation storage.PluginOperationRow) (controlPlanePluginRuntimePlan, error) {
	installed, ok, err := s.store.GetInstalledPlugin(ctx, operation.PluginID)
	if err != nil || !ok {
		return controlPlanePluginRuntimePlan{}, err
	}
	packageIdentity, packageDigest := installed.ActivePackageIdentity, installed.ActivePackageDigest
	if installed.StagedPackageIdentity != "" && installed.PendingOperationID == operation.ID {
		packageIdentity, packageDigest = installed.StagedPackageIdentity, installed.StagedPackageDigest
	}
	packageRow, found, err := s.storedPackage(ctx, packageIdentity, packageDigest)
	if err != nil || !found {
		return controlPlanePluginRuntimePlan{}, err
	}
	var manifest plugins.Manifest
	if err := json.Unmarshal([]byte(packageRow.ManifestJSON), &manifest); err != nil {
		return controlPlanePluginRuntimePlan{}, err
	}
	if manifest.Runtime.Kind != "rpc-service" || manifest.Runtime.HostScope != "control-plane" {
		return controlPlanePluginRuntimePlan{}, nil
	}
	var configSchema map[string]any
	if err := json.Unmarshal([]byte(packageRow.ConfigSchemaJSON), &configSchema); err != nil || configSchema == nil {
		return controlPlanePluginRuntimePlan{}, ErrPluginReadProjection
	}
	plan := controlPlanePluginRuntimePlan{Controlled: true}
	instances, err := s.store.ListPluginInstances(ctx, operation.PluginID)
	if err != nil {
		return plan, err
	}
	if operation.Kind == "disable" {
		for _, instance := range instances {
			plan.StopInstanceIDs = append(plan.StopInstanceIDs, instance.ID)
		}
		return plan, nil
	}
	if installed.DesiredLifecycle != "enabled" {
		return plan, nil
	}
	if s.validator == nil {
		return plan, errors.New("control-plane plugin runtime validator is unavailable")
	}
	validated, err := s.validator.ValidatePackage(packageRow.CachePath, plugins.PackageExpectation{ID: manifest.ID, Version: manifest.Version, SHA256: packageRow.Digest, SignatureKeyID: manifest.Signature.KeyID})
	if err != nil {
		return plan, err
	}
	requirement, err := pluginhost.SandboxRequirementFromValidatedPackage(validated)
	if err != nil {
		return plan, err
	}
	artifacts, err := s.storedArtifacts(ctx, packageRow.Identity, packageRow.Digest)
	if err != nil {
		return plan, err
	}
	var runtimeArtifact storage.PluginArtifactRow
	for _, artifact := range artifacts {
		if controlPlaneRuntimeArtifactMatches(artifact, manifest) {
			runtimeArtifact = artifact
			break
		}
	}
	if runtimeArtifact.ID == "" {
		return plan, errors.New("control-plane plugin runtime artifact is unavailable for this platform")
	}
	grants, err := s.controlPlaneGenerationGrants(ctx, installed, packageRow)
	if err != nil {
		return plan, err
	}
	declared := make([]string, 0, len(manifest.Permissions))
	for _, permission := range manifest.Permissions {
		declared = append(declared, strings.TrimSpace(permission.Name))
	}
	granted := make([]string, 0, len(grants))
	for _, grant := range grants {
		granted = append(granted, grant.Name)
	}
	sort.Strings(declared)
	sort.Strings(granted)
	for _, instance := range instances {
		if !instance.DesiredEnabled {
			continue
		}
		config, configVersion, resourceGroupID := instance.ConfigJSON, instance.ConfigVersion, instance.ResourceGroupID
		secretHandlesJSON := instance.SecretHandlesJSON
		if instance.PendingOperationID == operation.ID && instance.PendingVersion > 0 {
			config, configVersion = instance.PendingConfigJSON, instance.PendingVersion
			secretHandlesJSON = instance.PendingSecretHandlesJSON
			if instance.PendingResourceGroupID != "" {
				resourceGroupID = instance.PendingResourceGroupID
			}
		}
		var instanceSecretHandles []storage.PluginInstanceSecretHandle
		if err := json.Unmarshal([]byte(pluginDefaultJSONArray(secretHandlesJSON)), &instanceSecretHandles); err != nil {
			return plan, err
		}
		secretHandles := make([]storage.PluginGenerationSecretHandle, 0, len(instanceSecretHandles))
		for _, handle := range instanceSecretHandles {
			secretHandles = append(secretHandles, storage.PluginGenerationSecretHandle{ID: handle.ID, Version: handle.Version, Digest: handle.Digest, Purpose: handle.Purpose})
		}
		generation := storage.PluginGeneration{
			InstanceID: instance.ID, OperationID: operation.ID, Revision: operation.TargetRevision,
			PluginID: manifest.ID, PluginVersion: manifest.Version, PackageDigest: packageRow.Digest,
			Runtime:         storage.PluginGenerationRuntime{Kind: manifest.Runtime.Kind, ABI: manifest.Runtime.ABI, HostScope: manifest.Runtime.HostScope, Entry: manifest.Runtime.Entry},
			Artifact:        storage.PluginGenerationArtifact{ArtifactID: runtimeArtifact.ID, PackageIdentity: packageRow.Identity, RelativePath: runtimeArtifact.Path, SHA256: runtimeArtifact.SHA256, SizeBytes: runtimeArtifact.SizeBytes, Mode: runtimeArtifact.Mode, GOOS: runtimeArtifact.GOOS, GOARCH: runtimeArtifact.GOARCH, SignatureVerified: packageRow.SignatureVerdict == "verified", SignerKeyID: packageRow.SignatureKeyID, SignerFingerprint: packageRow.SignatureFingerprint},
			ExtensionPoints: manifest.ExtensionPoints, ConfigVersion: configVersion, Config: json.RawMessage(config), Grants: grants, SecretHandles: secretHandles,
			ResourceBudget: storage.PluginGenerationResourceBudget{TimeoutMS: manifest.ResourceBudget.TimeoutMS, MemoryBytes: manifest.ResourceBudget.MemoryBytes, Concurrency: manifest.ResourceBudget.Concurrency, InputBytes: manifest.ResourceBudget.InputBytes, OutputBytes: manifest.ResourceBudget.OutputBytes, CPUMillis: manifest.ResourceBudget.CPUMillis, Restarts: manifest.ResourceBudget.Restarts},
			Target:         storage.PluginGenerationTarget{Kind: "control-plane", ID: "control-plane", ResourceGroupID: resourceGroupID, Version: configVersion},
			FailurePolicy:  storage.PluginGenerationFailurePolicy{OnError: manifest.FailurePolicy.OnError, OnBudget: manifest.FailurePolicy.OnBudget, Restart: manifest.FailurePolicy.Restart, CoreFallback: manifest.FailurePolicy.CoreFallback},
		}
		generation.ID, err = storage.PluginGenerationIdentity(generation)
		if err != nil {
			return plan, err
		}
		generationID := generation.ID
		configRaw, handlesRaw, group, actorID, correlationID := config, secretHandlesJSON, resourceGroupID, operation.ActorID, operation.CorrelationID
		plan.Candidates = append(plan.Candidates, pluginhost.Candidate{
			InstanceID: instance.ID, OperationID: operation.ID, ResourceGroupID: resourceGroupID, Revision: operation.TargetRevision,
			Artifact: pluginhost.Artifact{CachePath: filepath.Join(packageRow.CachePath, filepath.FromSlash(runtimeArtifact.Path)), SHA256: runtimeArtifact.SHA256, GOOS: runtimeArtifact.GOOS, GOARCH: runtimeArtifact.GOARCH},
			Identity: pluginhost.Identity{PluginID: manifest.ID, Version: manifest.Version, PackageDigest: packageRow.Digest, Generation: generation.ID, Scopes: append([]string(nil), declared...)},
			Config:   append([]byte(nil), generation.Config...), ResolveConfigAndSecrets: func(resolveCtx context.Context, requestedGeneration string) ([]byte, []string, error) {
				if requestedGeneration != generationID {
					return nil, nil, storage.ErrPluginGenerationStale
				}
				materialized, handles, err := s.materializeStoredPluginConfig(resolveCtx, configSchema, configRaw, handlesRaw, group, actorID, correlationID)
				if err != nil {
					return nil, nil, err
				}
				exact, err := pluginSecretLogValues(materialized, handles)
				return append([]byte(nil), materialized...), exact, err
			}, Endpoint: pluginhost.Endpoint{Network: "unix"}, Requirement: requirement, Grants: append([]string(nil), granted...),
			Deadline: time.Duration(manifest.ResourceBudget.TimeoutMS) * time.Millisecond, GracePeriod: 5 * time.Second,
			Restart: manifest.FailurePolicy.Restart, RestartLimit: manifest.ResourceBudget.Restarts, RestartWindow: time.Minute, InitialBackoff: time.Second, MaximumBackoff: 30 * time.Second,
		})
	}
	return plan, nil
}

func controlPlaneRuntimeArtifactMatches(artifact storage.PluginArtifactRow, manifest plugins.Manifest) bool {
	if artifact.RuntimeKind != manifest.Runtime.Kind || artifact.RuntimeABI != manifest.Runtime.ABI || artifact.HostScope != manifest.Runtime.HostScope || artifact.GOOS != runtime.GOOS || artifact.GOARCH != runtime.GOARCH {
		return false
	}
	logicalEntry := strings.TrimSuffix(path.Base(filepath.ToSlash(artifact.Path)), ".exe")
	return logicalEntry == manifest.Runtime.Entry
}

func (s *PluginService) controlPlaneGenerationGrants(ctx context.Context, installed storage.InstalledPluginRow, packageRow storage.PluginPackageRow) ([]storage.PluginGenerationGrant, error) {
	if installed.PendingOperationID != "" && installed.StagedPackageIdentity == packageRow.Identity && installed.StagedPackageDigest == packageRow.Digest {
		var pending []storage.PluginGenerationGrant
		if err := json.Unmarshal([]byte(pluginDefaultJSONArray(installed.PendingGrantsJSON)), &pending); err != nil {
			return nil, err
		}
		return pending, nil
	}
	rows, err := s.store.ListPluginGrants(ctx, installed.PluginID)
	if err != nil {
		return nil, err
	}
	result := make([]storage.PluginGenerationGrant, 0, len(rows))
	for _, row := range rows {
		if !strings.EqualFold(row.PackageDigest, packageRow.Digest) || (row.PackageIdentity != "" && row.PackageIdentity != packageRow.Identity) {
			continue
		}
		kind, id := splitPluginResourceSelector(row.ResourceSelector)
		result = append(result, storage.PluginGenerationGrant{Name: row.Permission, ResourceKind: kind, ResourceID: id})
	}
	return result, nil
}

func pluginDefaultJSONArray(value string) string {
	if strings.TrimSpace(value) == "" {
		return "[]"
	}
	return value
}

func (s *PluginService) Configure(ctx context.Context, request PluginConfigureRequest) (storage.PluginInstanceRow, error) {
	return s.configureWithProspectiveDetail(ctx, request, nil)
}

func (s *PluginService) DeleteInstance(ctx context.Context, request PluginDeleteInstanceRequest) error {
	request.PluginID = strings.TrimSpace(request.PluginID)
	request.InstanceID = strings.TrimSpace(request.InstanceID)
	if request.PluginID == "" || request.InstanceID == "" {
		return fmt.Errorf("%w: plugin and instance identities are required", ErrInvalidArgument)
	}
	installed, ok, err := s.store.GetInstalledPlugin(ctx, request.PluginID)
	if err != nil {
		return err
	}
	operation := s.operation(ctx, request.PluginID, "delete_instance", installed.ActivePackageDigest, request.ActorID)
	if !ok {
		return s.recordFailure(ctx, operation, request.ActorID, ErrPluginNotInstalled)
	}
	if err := bindInstalledActiveOperation(&operation, installed); err != nil {
		return err
	}
	instance, exists, err := s.store.GetPluginInstance(ctx, request.InstanceID)
	if err != nil {
		return err
	}
	if !exists || instance.PluginID != request.PluginID {
		return s.recordFailure(ctx, operation, request.ActorID, ErrPluginInstanceNotFound)
	}
	if err := authorizePluginInstanceWrite(request.Actor, instance); err != nil {
		return s.recordFailure(ctx, operation, request.ActorID, err)
	}
	if err := ensureNoPendingOperation(installed); err != nil {
		return s.recordFailure(ctx, operation, request.ActorID, err)
	}
	bindings, err := storage.CanonicalPluginInstanceBindings(instance.BindingsJSON)
	if err != nil {
		return s.recordFailure(ctx, operation, request.ActorID, ErrPluginReadProjection)
	}
	if len(bindings) > 0 {
		return s.recordFailure(ctx, operation, request.ActorID, fmt.Errorf("%w: instance %s is still used by %d bound rule(s)", storage.ErrPluginDependencyConsumerInUse, instance.ID, len(bindings)))
	}
	now := s.now()
	operation.InstanceID, operation.ResourceGroupID = instance.ID, instance.ResourceGroupID
	operation.TargetRevision = pluginLifecycleTargetRevision(s.revisionNumbers, int64(installed.StateVersion+1))
	operation.Status, operation.CompletedAt = "succeeded", &now
	installed.LastOperationID, installed.UpdatedAt = operation.ID, now
	return s.store.ApplyPluginMutation(ctx, storage.PluginMutation{
		PluginID: request.PluginID, ExpectedActive: installed.ActivePackageDigest, ExpectedStateVersion: installed.StateVersion,
		Installed: &installed, DeleteInstanceID: instance.ID, ExpectedInstanceVersion: instance.StateVersion,
		Operation: operation, Audit: pluginLifecycleAudit(operation, request.ActorID, "success", "", now),
	})
}

func (s *PluginService) materializeStoredPluginConfig(ctx context.Context, schema map[string]any, rawConfig, rawHandles, resourceGroupID, actorID, correlationID string) (json.RawMessage, []storage.PluginInstanceSecretHandle, error) {
	var value any
	decoder := json.NewDecoder(strings.NewReader(rawConfig))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, nil, ErrPluginReadProjection
	}
	var handles []storage.PluginInstanceSecretHandle
	if err := json.Unmarshal([]byte(pluginDefaultJSONArray(rawHandles)), &handles); err != nil {
		return nil, nil, ErrPluginReadProjection
	}
	for _, handle := range handles {
		if s.secretVault == nil {
			return nil, nil, errors.New("plugin secret vault is unavailable")
		}
		plaintext, err := s.secretVault.ResolvePluginReference(ctx, secrets.OperationContext{ActorID: actorID, CorrelationID: correlationID, ResourceGroupID: resourceGroupID}, handle.ID, handle.Version, handle.Purpose, handle.Digest)
		if err != nil {
			return nil, nil, err
		}
		var secret any
		secretDecoder := json.NewDecoder(strings.NewReader(string(plaintext)))
		secretDecoder.UseNumber()
		decodeErr := secretDecoder.Decode(&secret)
		clear(plaintext)
		if decodeErr != nil || pluginSetJSONPointer(value, handle.Pointer, secret) != nil {
			return nil, nil, ErrPluginReadProjection
		}
	}
	encoded, err := json.Marshal(value)
	if err != nil || plugins.ValidateConfig(schema, encoded) != nil {
		if err == nil {
			err = plugins.ValidateConfig(schema, encoded)
		}
		return nil, nil, err
	}
	return encoded, handles, nil
}

func (s *PluginService) configureWithProspectiveDetail(ctx context.Context, request PluginConfigureRequest, prospective *PluginInstanceDetail) (storage.PluginInstanceRow, error) {
	installed, ok, err := s.store.GetInstalledPlugin(ctx, request.PluginID)
	if err != nil {
		return storage.PluginInstanceRow{}, err
	}
	operation := s.operation(ctx, request.PluginID, "configure", installed.ActivePackageDigest, request.ActorID)
	if !ok {
		return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, request.ActorID, ErrPluginNotInstalled)
	}
	if err := bindInstalledActiveOperation(&operation, installed); err != nil {
		return storage.PluginInstanceRow{}, err
	}
	packageRow, ok, err := s.storedPackage(ctx, installed.ActivePackageIdentity, installed.ActivePackageDigest)
	if err != nil {
		return storage.PluginInstanceRow{}, err
	}
	if !ok {
		return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, request.ActorID, errors.New("active plugin package is unavailable"))
	}
	schema, err := plugins.DecodeConfigSchema([]byte(packageRow.ConfigSchemaJSON))
	if err != nil {
		return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
	}
	if err := plugins.ValidateConfigWritableInput(schema, request.Config); err != nil {
		return storage.PluginInstanceRow{}, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	var instance storage.PluginInstanceRow
	exists := false
	if strings.TrimSpace(request.InstanceID) != "" {
		instance, exists, err = s.store.GetPluginInstance(ctx, request.InstanceID)
		if err != nil {
			return storage.PluginInstanceRow{}, err
		}
		if exists && instance.PluginID != request.PluginID {
			return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, request.ActorID, fmt.Errorf("%w: plugin instance identity mismatch", ErrInvalidArgument))
		}
	}
	if err := authorizePluginConfigureScope(request, exists, instance); err != nil {
		return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
	}
	if err := s.revalidateInstalledPackageVariant(ctx, installed.ActivePackageIdentity, installed.ActivePackageDigest); err != nil {
		return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
	}
	if err := ensureNoPendingOperation(installed); err != nil {
		return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
	}
	var manifest plugins.Manifest
	if err := json.Unmarshal([]byte(packageRow.ManifestJSON), &manifest); err != nil {
		return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
	}
	requestedTargetJSON, err := json.Marshal(request.Targets)
	if err != nil {
		return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, request.ActorID, fmt.Errorf("%w: invalid plugin targets", ErrInvalidArgument))
	}
	defaultTargetID, err := s.defaultPluginTargetID(ctx)
	if err != nil {
		return storage.PluginInstanceRow{}, err
	}
	targetIDs, err := pluginTargetIDs(requestedTargetJSON, defaultTargetID)
	if err != nil {
		return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, request.ActorID, fmt.Errorf("%w: %v", ErrInvalidArgument, err))
	}
	targetJSON, err := json.Marshal(targetIDs)
	if err != nil {
		return storage.PluginInstanceRow{}, err
	}
	if strings.TrimSpace(request.ResourceGroupID) == "" {
		return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, request.ActorID, fmt.Errorf("%w: plugin instance resource group is required", ErrInvalidArgument))
	}
	if err := s.validateAgentTargets(ctx, manifest.Compatibility.Agent, targetJSON); err != nil {
		return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
	}
	for _, targetID := range targetIDs {
		if err := authorizeReferencedResource(ctx, s.store, "agent", targetID); err != nil {
			return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, request.ActorID, fmt.Errorf("%w: %v", ErrPluginResourceAuthorization, err))
		}
	}
	if strings.TrimSpace(request.InstanceID) == "" {
		request.InstanceID = lifecycleID("instance")
	}
	operation.InstanceID = request.InstanceID
	operation.ResourceGroupID = request.ResourceGroupID
	if err := storage.ValidatePluginPolicyIdentity(request.InstanceID); err != nil {
		return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, request.ActorID, fmt.Errorf("%w: invalid plugin instance identity: %v", ErrInvalidArgument, err))
	}
	var currentHandles []storage.PluginInstanceSecretHandle
	if exists && strings.TrimSpace(instance.SecretHandlesJSON) != "" {
		if err := json.Unmarshal([]byte(pluginDefaultJSONArray(instance.SecretHandlesJSON)), &currentHandles); err != nil {
			return storage.PluginInstanceRow{}, ErrPluginReadProjection
		}
	}
	currentConfig := json.RawMessage(instance.ConfigJSON)
	if !exists || strings.TrimSpace(string(currentConfig)) == "" {
		currentConfig = json.RawMessage(`{}`)
	}
	publicConfig, replacements, retainedHandles, err := pluginPrepareBrokeredConfig(schema, currentConfig, request.Config, currentHandles, request.SecretReplacements)
	if err != nil {
		return storage.PluginInstanceRow{}, err
	}
	var materialized any
	decoder := json.NewDecoder(strings.NewReader(string(publicConfig)))
	decoder.UseNumber()
	if err := decoder.Decode(&materialized); err != nil {
		return storage.PluginInstanceRow{}, ErrPluginReadProjection
	}
	vaultOp := secrets.OperationContext{ActorID: request.ActorID, CorrelationID: operation.CorrelationID, ResourceGroupID: request.ResourceGroupID}
	for _, handle := range retainedHandles {
		if s.secretVault == nil {
			return storage.PluginInstanceRow{}, errors.New("plugin secret vault is unavailable")
		}
		plaintext, err := s.secretVault.Resolve(ctx, vaultOp, handle.ID)
		if err != nil {
			return storage.PluginInstanceRow{}, err
		}
		var value any
		valueDecoder := json.NewDecoder(strings.NewReader(string(plaintext)))
		valueDecoder.UseNumber()
		if err := valueDecoder.Decode(&value); err != nil || pluginSetJSONPointer(materialized, handle.Pointer, value) != nil {
			return storage.PluginInstanceRow{}, ErrPluginReadProjection
		}
	}
	secretWrites := make([]storage.PluginSecretWrite, 0, len(replacements))
	for pointer, raw := range replacements {
		if s.secretVault == nil {
			return storage.PluginInstanceRow{}, errors.New("plugin secret vault is unavailable")
		}
		pointerDigest := sha256.Sum256([]byte(pointer))
		prepared, err := s.secretVault.PreparePluginSecret(vaultOp, "plugin-config-"+operation.ID+"-"+hex.EncodeToString(pointerDigest[:6]), "plugin-config:"+request.InstanceID+":"+pointer, string(raw))
		if err != nil {
			return storage.PluginInstanceRow{}, err
		}
		digest := sha256.Sum256(raw)
		retainedHandles = append(retainedHandles, storage.PluginInstanceSecretHandle{Pointer: pointer, ID: prepared.Metadata.ID, Version: prepared.Metadata.ActiveVersion, Digest: hex.EncodeToString(digest[:]), Purpose: prepared.Metadata.Purpose})
		secretWrites = append(secretWrites, storage.PluginSecretWrite{Secret: prepared.Secret, Version: prepared.Version, Audit: prepared.Audit})
		var value any
		valueDecoder := json.NewDecoder(strings.NewReader(string(raw)))
		valueDecoder.UseNumber()
		if err := valueDecoder.Decode(&value); err != nil || pluginSetJSONPointer(materialized, pointer, value) != nil {
			return storage.PluginInstanceRow{}, ErrInvalidArgument
		}
	}
	materializedConfig, err := json.Marshal(materialized)
	if err != nil || plugins.ValidateConfig(schema, materializedConfig) != nil {
		if err == nil {
			err = plugins.ValidateConfig(schema, materializedConfig)
		}
		return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
	}
	cleanedConfig, _, err := pluginStripWriteOnly(schema, materialized, "")
	if err != nil {
		return storage.PluginInstanceRow{}, err
	}
	publicConfig, err = json.Marshal(cleanedConfig)
	if err != nil {
		return storage.PluginInstanceRow{}, err
	}
	sort.Slice(retainedHandles, func(i, j int) bool { return retainedHandles[i].Pointer < retainedHandles[j].Pointer })
	handlesJSON, err := json.Marshal(retainedHandles)
	if err != nil {
		return storage.PluginInstanceRow{}, err
	}
	request.Config = publicConfig
	if !exists && strings.TrimSpace(request.InstanceID) != "" {
		instance, exists, err = s.store.GetPluginInstance(ctx, request.InstanceID)
		if err != nil {
			return storage.PluginInstanceRow{}, err
		}
	}
	policyChains := []string{}
	if request.PolicyChains != nil {
		policyChains = append(policyChains, (*request.PolicyChains)...)
	} else if exists {
		policyChains, err = storage.CanonicalPluginPolicyChains(instance.PolicyChainsJSON)
		if err != nil {
			return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, request.ActorID, fmt.Errorf("%w: persisted policy chain memberships are invalid", ErrInvalidArgument))
		}
	}
	policyChainsJSON, err := storage.EncodePluginPolicyChains(policyChains)
	if err != nil {
		return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, request.ActorID, fmt.Errorf("%w: invalid policy chain memberships: %v", ErrInvalidArgument, err))
	}
	bindingRequests := []storage.PluginInstanceBindingRequest{}
	if request.Bindings != nil {
		bindingRequests, err = storage.CanonicalPluginInstanceBindingRequests(*request.Bindings)
		if err != nil {
			return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, request.ActorID, fmt.Errorf("%w: invalid plugin bindings: %v", ErrInvalidArgument, err))
		}
	} else if exists {
		var activeBindings []storage.PluginInstanceBinding
		activeBindings, err = storage.CanonicalPluginInstanceBindings(instance.BindingsJSON)
		if err != nil {
			return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, request.ActorID, fmt.Errorf("%w: persisted plugin bindings are invalid", ErrInvalidArgument))
		}
		bindingRequests = storage.PluginInstanceBindingRequests(activeBindings)
	}
	bindings := []storage.PluginInstanceBinding{}
	if len(bindingRequests) > 0 {
		consumerStore, ok := s.store.(pluginDependencyConsumerStore)
		if !ok {
			return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, request.ActorID, errors.New("plugin binding ownership resolver is unavailable"))
		}
		bindings, err = consumerStore.ResolvePluginInstanceBindingRequests(ctx, bindingRequests, request.ResourceGroupID)
		if err != nil {
			return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, request.ActorID, fmt.Errorf("%w: %v", ErrInvalidArgument, err))
		}
	}
	bindingsJSON, err := storage.EncodePluginInstanceBindings(bindings)
	if err != nil {
		return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, request.ActorID, fmt.Errorf("%w: invalid plugin bindings: %v", ErrInvalidArgument, err))
	}
	if len(bindings) > 0 && (manifest.Runtime.Kind != "rpc-service" || manifest.Runtime.HostScope != "agent") {
		return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, request.ActorID, fmt.Errorf("%w: core consumer bindings require an Agent rpc-service plugin", ErrInvalidArgument))
	}
	if err := storage.ValidatePluginInstanceBindingScope(bindings, targetIDs, manifest.ExtensionPoints); err != nil {
		return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, request.ActorID, fmt.Errorf("%w: %v", ErrInvalidArgument, err))
	}
	for _, binding := range bindings {
		if err := authorizeReferencedResource(ctx, s.store, binding.Consumer.Kind, binding.TargetAgentID+":"+binding.Consumer.ID); err != nil {
			return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, request.ActorID, fmt.Errorf("%w: %v", ErrPluginResourceAuthorization, err))
		}
	}
	now := s.now()
	version := instance.ConfigVersion + 1
	if instance.PendingVersion >= version {
		version = instance.PendingVersion + 1
	}
	if !exists {
		instance = storage.PluginInstanceRow{ID: request.InstanceID, PluginID: request.PluginID, ResourceGroupID: request.ResourceGroupID, TargetJSON: string(targetJSON), PolicyChainsJSON: "[]", BindingsJSON: "[]", SecretHandlesJSON: "[]", ConfigJSON: "{}", StatusSummaryJSON: "{}"}
	}
	instance.PendingConfigJSON = string(request.Config)
	instance.PendingSecretHandlesJSON = string(handlesJSON)
	instance.PendingResourceGroupID = request.ResourceGroupID
	instance.PendingTargetJSON = string(targetJSON)
	instance.PendingPolicyChainsJSON = policyChainsJSON
	instance.PendingBindingsJSON = bindingsJSON
	instance.PendingVersion = version
	instance.PendingOperationID = operation.ID
	instance.DesiredEnabled = installed.DesiredLifecycle == "enabled"
	instance.CurrentState = "applying"
	instance.UpdatedAt = now
	installed.LastOperationID, installed.UpdatedAt = operation.ID, now
	operation.TargetRevision = pluginLifecycleTargetRevision(s.revisionNumbers, int64(installed.StateVersion+1))
	setPendingOperation(&installed, operation)
	operation.Status = "applying"
	projectedInstance := instance
	projectedInstance.StateVersion++
	details, err := s.pluginInstanceDetails(ctx, []storage.PluginInstanceRow{projectedInstance})
	if err != nil {
		return storage.PluginInstanceRow{}, err
	}
	err = s.store.ApplyPluginMutation(ctx, storage.PluginMutation{PluginID: request.PluginID, ExpectedActive: installed.ActivePackageDigest, ExpectedStateVersion: installed.StateVersion, Installed: &installed, ReplaceInstance: &instance, ValidateInstanceScope: true, Operation: operation, Audit: pluginLifecycleAudit(operation, request.ActorID, "accepted", "", now), SecretWrites: secretWrites})
	if err == nil && prospective != nil {
		*prospective = details[0]
	}
	return instance, err
}

func (s *PluginService) CompleteConfigure(ctx context.Context, applyResult PluginApplyResult) (storage.PluginInstanceRow, error) {
	installed, ok, err := s.store.GetInstalledPlugin(ctx, applyResult.PluginID)
	if err != nil {
		return storage.PluginInstanceRow{}, err
	}
	if !ok {
		return storage.PluginInstanceRow{}, ErrPluginNotInstalled
	}
	operation, err := s.pendingOperation(ctx, installed, applyResult, "configure")
	if err != nil {
		return storage.PluginInstanceRow{}, err
	}
	instance, exists, err := s.store.GetPluginInstance(ctx, applyResult.InstanceID)
	if err != nil {
		return storage.PluginInstanceRow{}, err
	}
	if !exists || instance.PluginID != applyResult.PluginID || instance.PendingVersion == 0 || instance.PendingOperationID != operation.ID || instance.PendingVersion != applyResult.ConfigVersion {
		return storage.PluginInstanceRow{}, errors.New("pending plugin configuration identity is stale or unavailable")
	}
	encodedResults, err := encodePluginResultObject(applyResult.AgentResults)
	if err != nil {
		return storage.PluginInstanceRow{}, err
	}
	now := s.now()
	operation.AgentResultsJSON, operation.CompletedAt = encodedResults, &now
	if applyResult.Applied {
		instance.ConfigJSON, instance.ConfigVersion = instance.PendingConfigJSON, instance.PendingVersion
		instance.SecretHandlesJSON = instance.PendingSecretHandlesJSON
		instance.ResourceGroupID, instance.TargetJSON = instance.PendingResourceGroupID, instance.PendingTargetJSON
		instance.PolicyChainsJSON = instance.PendingPolicyChainsJSON
		instance.BindingsJSON = instance.PendingBindingsJSON
		clearPendingInstance(&instance)
		if installed.CurrentLifecycle == "active" {
			instance.CurrentState = "active"
		} else {
			instance.CurrentState = "disabled"
		}
		operation.Status = "succeeded"
	} else {
		clearPendingInstance(&instance)
		instance.CurrentState = "degraded"
		operation.Status, operation.ErrorClass, operation.Error = "failed", "agent_apply", "one or more target agents failed to apply plugin configuration"
	}
	instance.StatusSummaryJSON = encodedResults
	instance.UpdatedAt = now
	clearPendingOperation(&installed)
	installed.LastOperationID, installed.UpdatedAt = operation.ID, now
	auditResult := "success"
	if !applyResult.Applied {
		auditResult = "failure"
	}
	err = s.store.ApplyPluginMutation(ctx, storage.PluginMutation{PluginID: applyResult.PluginID, ExpectedActive: installed.ActivePackageDigest, ExpectedStateVersion: installed.StateVersion, ExpectedPendingOperationID: operation.ID, Installed: &installed, ReplaceInstance: &instance, ValidateInstanceScope: applyResult.Applied, PromoteInstanceBinding: applyResult.Applied, Operation: operation, CompleteOperation: true, Audit: pluginLifecycleAudit(operation, applyResult.ActorID, auditResult, operation.ErrorClass, now)})
	return instance, err
}

func (s *PluginService) Upgrade(ctx context.Context, request PluginUpgradeRequest) (storage.InstalledPluginRow, error) {
	installed, ok, err := s.store.GetInstalledPlugin(ctx, request.PluginID)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	operation := s.operation(ctx, request.PluginID, "upgrade", request.Package.Package.Digest, request.ActorID)
	if err := bindOperationCandidate(&operation, request.Package); err != nil {
		return storage.InstalledPluginRow{}, err
	}
	if !ok {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, request.ActorID, ErrPluginNotInstalled)
	}
	if err := s.validatePackageCandidate(request.Package); err != nil || request.Package.Package.Manifest.ID != request.PluginID {
		if err == nil {
			err = errors.New("upgrade package identity mismatch")
		}
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
	}
	if err := confirmCandidateSource(request.Package, request.RiskAccepted); err != nil {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
	}
	grants, err := s.store.ListPluginGrants(ctx, request.PluginID)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	if permissionsAdded(grants, installed.ActivePackageDigest, request.Package.Package.Manifest.Permissions) {
		if err := confirmPermissions(request.Package.Package.Manifest.Permissions, request.ConfirmedPermissions, true); err != nil {
			return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
		}
	}
	candidateIdentity := storage.PluginPackageIdentity(request.Package.Package.Digest, request.Package.SignatureTrust.SourceID, request.Package.SignatureTrust.Fingerprint)
	if strings.EqualFold(installed.ActivePackageIdentity, candidateIdentity) {
		return installed, nil
	}
	if err := ensureNoPendingOperation(installed); err != nil {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
	}
	activePackage, exists, err := s.storedPackage(ctx, installed.ActivePackageIdentity, installed.ActivePackageDigest)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	if !exists {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, request.ActorID, errors.New("active plugin package is unavailable"))
	}
	if err := s.validateStoredPackage(ctx, activePackage); err != nil {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
	}
	var activeManifest plugins.Manifest
	if err := json.Unmarshal([]byte(activePackage.ManifestJSON), &activeManifest); err != nil {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
	}
	activeSchema, err := plugins.DecodeConfigSchema([]byte(activePackage.ConfigSchemaJSON))
	if err != nil {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
	}
	instances, err := s.store.ListPluginInstances(ctx, request.PluginID)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	for _, instance := range instances {
		if err := s.validateAgentTargets(ctx, request.Package.Package.Manifest.Compatibility.Agent, json.RawMessage(instance.TargetJSON)); err != nil {
			return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
		}
		if err := s.validateStoredPluginBindings(ctx, instance, request.Package.Package.Manifest); err != nil {
			return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
		}
	}
	operation.TargetRevision = pluginLifecycleTargetRevision(s.revisionNumbers, int64(installed.StateVersion+1))
	for index := range instances {
		staged, handles, err := s.materializeStoredPluginConfig(ctx, activeSchema, instances[index].ConfigJSON, instances[index].SecretHandlesJSON, instances[index].ResourceGroupID, request.ActorID, operation.CorrelationID)
		if err != nil {
			return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
		}
		if len(request.Package.Package.Manifest.Migrations) > 0 {
			staged, err = plugins.ApplyMigrationChain(request.Package.CachePath, request.Package.Package.Manifest, activeManifest.Version, staged)
			if err != nil {
				return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
			}
		}
		if err := plugins.ValidateConfig(request.Package.Package.ConfigSchema, staged); err != nil {
			return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, request.ActorID, fmt.Errorf("candidate config for instance %s is incompatible after migration: %w", instances[index].ID, err))
		}
		stagedValue, err := pluginConfigValue(staged)
		if err != nil {
			return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
		}
		cleaned, _, err := pluginStripWriteOnly(request.Package.Package.ConfigSchema, stagedValue, "")
		if err != nil {
			return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
		}
		for _, handle := range handles {
			if !pluginPointerIsWriteOnly(request.Package.Package.ConfigSchema, cleaned, handle.Pointer) {
				return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, request.ActorID, errors.New("plugin migration changed a brokered secret pointer"))
			}
		}
		publicStaged, err := json.Marshal(cleaned)
		if err != nil {
			return storage.InstalledPluginRow{}, err
		}
		instances[index].PendingConfigJSON = string(publicStaged)
		instances[index].PendingSecretHandlesJSON = instances[index].SecretHandlesJSON
		instances[index].PendingPolicyChainsJSON = instances[index].PolicyChainsJSON
		instances[index].PendingBindingsJSON = instances[index].BindingsJSON
		instances[index].PendingVersion = instances[index].ConfigVersion + 1
		instances[index].PendingOperationID = operation.ID
		instances[index].CurrentState = "applying"
	}
	now := s.now()
	manifestJSON, _ := json.Marshal(request.Package.Package.Manifest)
	schemaJSON, _ := json.Marshal(request.Package.Package.ConfigSchema)
	packageRow, artifacts, projectionErr := pluginPackageRows(request.Package, manifestJSON, schemaJSON, now)
	if projectionErr != nil {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, request.ActorID, projectionErr)
	}
	oldDigest := installed.ActivePackageDigest
	installed.StagedPackageDigest = strings.ToLower(request.Package.Package.Digest)
	installed.StagedPackageIdentity = candidateIdentity
	installed.StagedSourceID, installed.StagedSourceKind, installed.StagedSourceRiskLabel = packageRow.SourceID, packageRow.SourceKind, packageRow.SourceRiskLabel
	installed.StagedSourceRevision, installed.StagedSourceRefKind, installed.StagedSourceRefName, installed.StagedSourceResolvedOID = packageRow.SourceRevision, packageRow.SourceRefKind, packageRow.SourceRefName, packageRow.SourceResolvedOID
	installed.StagedSignatureKeyID, installed.StagedSignaturePublicKey, installed.StagedSignatureFingerprint = packageRow.SignatureKeyID, packageRow.SignaturePublicKey, packageRow.SignatureFingerprint
	pendingGrants, _ := json.Marshal(pluginGenerationGrants(request.Package.Package.Manifest.Permissions))
	installed.PendingGrantsJSON = string(pendingGrants)
	installed.CurrentLifecycle = "upgrading"
	installed.LastOperationID, installed.UpdatedAt = operation.ID, now
	if err := storage.BindPluginOperationPackage(&operation, packageRow); err != nil {
		return storage.InstalledPluginRow{}, err
	}
	setPendingOperation(&installed, operation)
	operation.Status = "staged"
	err = s.store.ApplyPluginMutation(ctx, storage.PluginMutation{PluginID: request.PluginID, ExpectedActive: oldDigest, ExpectedStateVersion: installed.StateVersion, Installed: &installed, Package: &packageRow, Artifacts: artifacts, ReplaceInstances: instances, Operation: operation, Audit: pluginLifecycleAudit(operation, request.ActorID, "accepted", "", now), RequireAcquisition: request.Package.requireAcquisition, AcquisitionSourceID: request.Package.sourceID, AcquisitionDigest: request.Package.Package.Digest})
	return installed, err
}

func (s *PluginService) CompleteUpgrade(ctx context.Context, applyResult PluginApplyResult) (storage.InstalledPluginRow, error) {
	installed, ok, err := s.store.GetInstalledPlugin(ctx, applyResult.PluginID)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	if !ok {
		return storage.InstalledPluginRow{}, ErrPluginNotInstalled
	}
	operation, err := s.pendingOperation(ctx, installed, applyResult, "upgrade")
	if err != nil || installed.StagedPackageDigest == "" || installed.CurrentLifecycle != "upgrading" {
		if err != nil {
			return storage.InstalledPluginRow{}, err
		}
		return storage.InstalledPluginRow{}, errors.New("staged upgrade is unavailable")
	}
	packageRow, exists, err := s.storedPackage(ctx, installed.StagedPackageIdentity, installed.StagedPackageDigest)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	if !exists {
		return storage.InstalledPluginRow{}, errors.New("staged package cache record is unavailable")
	}
	if err := s.validateStoredPackage(ctx, packageRow); err != nil {
		return installed, s.failPendingPackagePromotion(ctx, installed, operation, err)
	}
	var manifest plugins.Manifest
	if err := json.Unmarshal([]byte(packageRow.ManifestJSON), &manifest); err != nil {
		return storage.InstalledPluginRow{}, err
	}
	instances, err := s.store.ListPluginInstances(ctx, applyResult.PluginID)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	for _, instance := range instances {
		if instance.PendingOperationID != operation.ID || instance.PendingVersion == 0 {
			return storage.InstalledPluginRow{}, errors.New("staged instance migration identity is unavailable")
		}
	}
	encodedResults, err := encodePluginResultObject(applyResult.AgentResults)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	now, oldDigest := s.now(), installed.ActivePackageDigest
	operation.AgentResultsJSON, operation.CompletedAt = encodedResults, &now
	var grants []storage.PluginGrantRow
	if applyResult.Applied {
		oldIdentity := installed.ActivePackageIdentity
		installed.ActivePackageDigest, installed.RollbackPackageDigest = installed.StagedPackageDigest, oldDigest
		installed.ActivePackageIdentity, installed.RollbackPackageIdentity = installed.StagedPackageIdentity, oldIdentity
		installed.RuntimeKind, installed.RuntimeABI, installed.HostScope = manifest.Runtime.Kind, manifest.Runtime.ABI, manifest.Runtime.HostScope
		installed.ActiveSourceID, installed.RollbackSourceID = installed.StagedSourceID, installed.ActiveSourceID
		installed.ActiveSourceKind, installed.RollbackSourceKind = installed.StagedSourceKind, installed.ActiveSourceKind
		installed.ActiveSourceRiskLabel, installed.RollbackSourceRiskLabel = installed.StagedSourceRiskLabel, installed.ActiveSourceRiskLabel
		installed.ActiveSourceRevision, installed.RollbackSourceRevision = installed.StagedSourceRevision, installed.ActiveSourceRevision
		installed.ActiveSourceRefKind, installed.RollbackSourceRefKind = installed.StagedSourceRefKind, installed.ActiveSourceRefKind
		installed.ActiveSourceRefName, installed.RollbackSourceRefName = installed.StagedSourceRefName, installed.ActiveSourceRefName
		installed.ActiveSourceResolvedOID, installed.RollbackSourceResolvedOID = installed.StagedSourceResolvedOID, installed.ActiveSourceResolvedOID
		installed.ActiveSignatureKeyID, installed.RollbackSignatureKeyID = installed.StagedSignatureKeyID, installed.ActiveSignatureKeyID
		installed.ActiveSignaturePublicKey, installed.RollbackSignaturePublicKey = installed.StagedSignaturePublicKey, installed.ActiveSignaturePublicKey
		installed.ActiveSignatureFingerprint, installed.RollbackSignatureFingerprint = installed.StagedSignatureFingerprint, installed.ActiveSignatureFingerprint
		clearStagedSource(&installed)
		cleanupJSON, _ := json.Marshal(manifest.Cleanup)
		installed.CleanupPolicyJSON = string(cleanupJSON)
		if installed.DesiredLifecycle == "enabled" {
			installed.CurrentLifecycle = "active"
		} else {
			installed.CurrentLifecycle = "disabled"
		}
		operation.Status = "succeeded"
		grants = grantRows(applyResult.PluginID, installed.ActivePackageDigest, installed.ActivePackageIdentity, manifest.Permissions, operation.ActorID, now)
		for index := range instances {
			instances[index].RollbackConfigJSON, instances[index].RollbackVersion = instances[index].ConfigJSON, instances[index].ConfigVersion
			instances[index].RollbackResourceGroupID = instances[index].ResourceGroupID
			instances[index].RollbackSecretHandlesJSON = instances[index].SecretHandlesJSON
			instances[index].RollbackPolicyChainsJSON = instances[index].PolicyChainsJSON
			instances[index].RollbackBindingsJSON = instances[index].BindingsJSON
			instances[index].ConfigJSON, instances[index].ConfigVersion = instances[index].PendingConfigJSON, instances[index].PendingVersion
			instances[index].SecretHandlesJSON = instances[index].PendingSecretHandlesJSON
			instances[index].PolicyChainsJSON = instances[index].PendingPolicyChainsJSON
			instances[index].BindingsJSON = instances[index].PendingBindingsJSON
			clearPendingInstance(&instances[index])
			instances[index].CurrentState = lifecycleCurrentState(installed.DesiredLifecycle)
			instances[index].UpdatedAt = now
		}
	} else {
		clearStagedSource(&installed)
		installed.CurrentLifecycle = lifecycleCurrentState(installed.DesiredLifecycle)
		operation.Status, operation.ErrorClass, operation.Error = "failed", "agent_apply", "one or more target agents failed to apply staged plugin package"
		for index := range instances {
			clearPendingInstance(&instances[index])
			instances[index].CurrentState = lifecycleCurrentState(installed.DesiredLifecycle)
			instances[index].UpdatedAt = now
		}
	}
	clearPendingOperation(&installed)
	installed.LastOperationID, installed.UpdatedAt = operation.ID, now
	auditResult := "success"
	if !applyResult.Applied {
		auditResult = "failure"
	}
	mutation := storage.PluginMutation{PluginID: applyResult.PluginID, ExpectedActive: oldDigest, ExpectedStateVersion: installed.StateVersion, ExpectedPendingOperationID: operation.ID, Installed: &installed, ReplaceInstances: instances, Operation: operation, CompleteOperation: true, Audit: pluginLifecycleAudit(operation, applyResult.ActorID, auditResult, operation.ErrorClass, now)}
	if applyResult.Applied {
		mutation.ReplaceGrants = grants
	}
	err = s.store.ApplyPluginMutation(ctx, mutation)
	return installed, err
}

func (s *PluginService) Rollback(ctx context.Context, request PluginRollbackRequest) (storage.InstalledPluginRow, error) {
	pluginID, actorID := request.PluginID, request.ActorID
	installed, ok, err := s.store.GetInstalledPlugin(ctx, pluginID)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	operation := s.operation(ctx, pluginID, "rollback", installed.RollbackPackageDigest, actorID)
	if !ok || installed.RollbackPackageDigest == "" {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, errors.New("rollback package is unavailable"))
	}
	if err := bindInstalledRollbackOperation(&operation, installed); err != nil {
		return storage.InstalledPluginRow{}, err
	}
	rollbackPackage, exists, err := s.storedPackage(ctx, installed.RollbackPackageIdentity, installed.RollbackPackageDigest)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	if !exists {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, errors.New("rollback package cache record is unavailable"))
	}
	if err := s.validateStoredPackage(ctx, rollbackPackage); err != nil {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, err)
	}
	if err := ensureNoPendingOperation(installed); err != nil {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, err)
	}
	rollbackSchema, err := plugins.DecodeConfigSchema([]byte(rollbackPackage.ConfigSchemaJSON))
	if err != nil {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, err)
	}
	var rollbackManifest plugins.Manifest
	if err := json.Unmarshal([]byte(rollbackPackage.ManifestJSON), &rollbackManifest); err != nil {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, err)
	}
	grants, err := s.store.ListPluginGrants(ctx, pluginID)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	if permissionsAdded(grants, installed.ActivePackageDigest, rollbackManifest.Permissions) {
		if err := confirmPermissions(rollbackManifest.Permissions, request.ConfirmedPermissions, true); err != nil {
			return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, err)
		}
	}
	instances, err := s.store.ListPluginInstances(ctx, pluginID)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	for _, instance := range instances {
		if err := s.validateAgentTargets(ctx, rollbackManifest.Compatibility.Agent, json.RawMessage(instance.TargetJSON)); err != nil {
			return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, err)
		}
		if err := s.validateStoredPluginBindings(ctx, instance, rollbackManifest); err != nil {
			return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, err)
		}
	}
	operation.TargetRevision = pluginLifecycleTargetRevision(s.revisionNumbers, int64(installed.StateVersion+1))
	for index := range instances {
		rollbackGroupID := strings.TrimSpace(instances[index].RollbackResourceGroupID)
		if rollbackGroupID == "" {
			return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, fmt.Errorf("rollback ownership for instance %s is unavailable", instances[index].ID))
		}
		_, _, configErr := s.materializeStoredPluginConfig(ctx, rollbackSchema, instances[index].RollbackConfigJSON, instances[index].RollbackSecretHandlesJSON, rollbackGroupID, actorID, operation.CorrelationID)
		if instances[index].RollbackVersion == 0 || configErr != nil {
			return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, fmt.Errorf("rollback config for instance %s is unavailable or invalid", instances[index].ID))
		}
		instances[index].PendingConfigJSON = instances[index].RollbackConfigJSON
		instances[index].PendingSecretHandlesJSON = instances[index].RollbackSecretHandlesJSON
		instances[index].PendingResourceGroupID = rollbackGroupID
		instances[index].PendingPolicyChainsJSON = instances[index].RollbackPolicyChainsJSON
		instances[index].PendingBindingsJSON = instances[index].RollbackBindingsJSON
		instances[index].PendingVersion = instances[index].RollbackVersion
		instances[index].PendingOperationID = operation.ID
		instances[index].CurrentState = "applying"
	}
	now, oldDigest := s.now(), installed.ActivePackageDigest
	installed.StagedPackageDigest = installed.RollbackPackageDigest
	installed.StagedPackageIdentity = installed.RollbackPackageIdentity
	installed.StagedSourceID, installed.StagedSourceKind, installed.StagedSourceRiskLabel = installed.RollbackSourceID, installed.RollbackSourceKind, installed.RollbackSourceRiskLabel
	installed.StagedSourceRevision, installed.StagedSourceRefKind, installed.StagedSourceRefName, installed.StagedSourceResolvedOID = installed.RollbackSourceRevision, installed.RollbackSourceRefKind, installed.RollbackSourceRefName, installed.RollbackSourceResolvedOID
	installed.StagedSignatureKeyID, installed.StagedSignaturePublicKey, installed.StagedSignatureFingerprint = installed.RollbackSignatureKeyID, installed.RollbackSignaturePublicKey, installed.RollbackSignatureFingerprint
	pendingGrants, _ := json.Marshal(pluginGenerationGrants(rollbackManifest.Permissions))
	installed.PendingGrantsJSON = string(pendingGrants)
	installed.CurrentLifecycle = "rolling_back"
	installed.LastOperationID, installed.UpdatedAt = operation.ID, now
	setPendingOperation(&installed, operation)
	operation.Status = "staged"
	err = s.store.ApplyPluginMutation(ctx, storage.PluginMutation{PluginID: pluginID, ExpectedActive: oldDigest, ExpectedStateVersion: installed.StateVersion, Installed: &installed, ReplaceInstances: instances, Operation: operation, Audit: pluginLifecycleAudit(operation, actorID, "accepted", "", now)})
	return installed, err
}

func (s *PluginService) CompleteRollback(ctx context.Context, applyResult PluginApplyResult) (storage.InstalledPluginRow, error) {
	installed, ok, err := s.store.GetInstalledPlugin(ctx, applyResult.PluginID)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	if !ok {
		return storage.InstalledPluginRow{}, ErrPluginNotInstalled
	}
	operation, err := s.pendingOperation(ctx, installed, applyResult, "rollback")
	if err != nil || installed.StagedPackageDigest == "" || installed.CurrentLifecycle != "rolling_back" {
		if err != nil {
			return storage.InstalledPluginRow{}, err
		}
		return storage.InstalledPluginRow{}, errors.New("staged rollback is unavailable")
	}
	packageRow, exists, err := s.storedPackage(ctx, installed.StagedPackageIdentity, installed.StagedPackageDigest)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	if !exists {
		return storage.InstalledPluginRow{}, errors.New("rollback package cache record is unavailable")
	}
	if err := s.validateStoredPackage(ctx, packageRow); err != nil {
		return installed, s.failPendingPackagePromotion(ctx, installed, operation, err)
	}
	var manifest plugins.Manifest
	if err := json.Unmarshal([]byte(packageRow.ManifestJSON), &manifest); err != nil {
		return storage.InstalledPluginRow{}, err
	}
	instances, err := s.store.ListPluginInstances(ctx, applyResult.PluginID)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	for _, instance := range instances {
		if instance.PendingOperationID != operation.ID || instance.PendingVersion == 0 {
			return storage.InstalledPluginRow{}, errors.New("staged rollback config identity is unavailable")
		}
	}
	encodedResults, err := encodePluginResultObject(applyResult.AgentResults)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	now, oldDigest := s.now(), installed.ActivePackageDigest
	operation.AgentResultsJSON, operation.CompletedAt = encodedResults, &now
	var grants []storage.PluginGrantRow
	if applyResult.Applied {
		oldIdentity := installed.ActivePackageIdentity
		installed.ActivePackageDigest, installed.RollbackPackageDigest = installed.StagedPackageDigest, oldDigest
		installed.ActivePackageIdentity, installed.RollbackPackageIdentity = installed.StagedPackageIdentity, oldIdentity
		installed.RuntimeKind, installed.RuntimeABI, installed.HostScope = manifest.Runtime.Kind, manifest.Runtime.ABI, manifest.Runtime.HostScope
		installed.ActiveSourceID, installed.RollbackSourceID = installed.StagedSourceID, installed.ActiveSourceID
		installed.ActiveSourceKind, installed.RollbackSourceKind = installed.StagedSourceKind, installed.ActiveSourceKind
		installed.ActiveSourceRiskLabel, installed.RollbackSourceRiskLabel = installed.StagedSourceRiskLabel, installed.ActiveSourceRiskLabel
		installed.ActiveSourceRevision, installed.RollbackSourceRevision = installed.StagedSourceRevision, installed.ActiveSourceRevision
		installed.ActiveSourceRefKind, installed.RollbackSourceRefKind = installed.StagedSourceRefKind, installed.ActiveSourceRefKind
		installed.ActiveSourceRefName, installed.RollbackSourceRefName = installed.StagedSourceRefName, installed.ActiveSourceRefName
		installed.ActiveSourceResolvedOID, installed.RollbackSourceResolvedOID = installed.StagedSourceResolvedOID, installed.ActiveSourceResolvedOID
		installed.ActiveSignatureKeyID, installed.RollbackSignatureKeyID = installed.StagedSignatureKeyID, installed.ActiveSignatureKeyID
		installed.ActiveSignaturePublicKey, installed.RollbackSignaturePublicKey = installed.StagedSignaturePublicKey, installed.ActiveSignaturePublicKey
		installed.ActiveSignatureFingerprint, installed.RollbackSignatureFingerprint = installed.StagedSignatureFingerprint, installed.ActiveSignatureFingerprint
		clearStagedSource(&installed)
		cleanupJSON, _ := json.Marshal(manifest.Cleanup)
		installed.CleanupPolicyJSON = string(cleanupJSON)
		if installed.DesiredLifecycle == "enabled" {
			installed.CurrentLifecycle = "active"
		} else {
			installed.CurrentLifecycle = "disabled"
		}
		operation.Status = "succeeded"
		grants = grantRows(applyResult.PluginID, installed.ActivePackageDigest, installed.ActivePackageIdentity, manifest.Permissions, operation.ActorID, now)
		for index := range instances {
			instances[index].RollbackConfigJSON, instances[index].RollbackVersion = instances[index].ConfigJSON, instances[index].ConfigVersion
			instances[index].RollbackResourceGroupID = instances[index].ResourceGroupID
			instances[index].RollbackSecretHandlesJSON = instances[index].SecretHandlesJSON
			instances[index].RollbackPolicyChainsJSON = instances[index].PolicyChainsJSON
			instances[index].RollbackBindingsJSON = instances[index].BindingsJSON
			instances[index].ConfigJSON, instances[index].ConfigVersion = instances[index].PendingConfigJSON, instances[index].PendingVersion
			instances[index].SecretHandlesJSON = instances[index].PendingSecretHandlesJSON
			instances[index].PolicyChainsJSON = instances[index].PendingPolicyChainsJSON
			instances[index].BindingsJSON = instances[index].PendingBindingsJSON
			clearPendingInstance(&instances[index])
			instances[index].CurrentState = lifecycleCurrentState(installed.DesiredLifecycle)
			instances[index].UpdatedAt = now
		}
	} else {
		clearStagedSource(&installed)
		installed.CurrentLifecycle = lifecycleCurrentState(installed.DesiredLifecycle)
		operation.Status, operation.ErrorClass, operation.Error = "failed", "agent_apply", "one or more target agents failed to apply rollback package"
		for index := range instances {
			clearPendingInstance(&instances[index])
			instances[index].CurrentState = lifecycleCurrentState(installed.DesiredLifecycle)
			instances[index].UpdatedAt = now
		}
	}
	clearPendingOperation(&installed)
	installed.LastOperationID, installed.UpdatedAt = operation.ID, now
	auditResult := "success"
	if !applyResult.Applied {
		auditResult = "failure"
	}
	mutation := storage.PluginMutation{PluginID: applyResult.PluginID, ExpectedActive: oldDigest, ExpectedStateVersion: installed.StateVersion, ExpectedPendingOperationID: operation.ID, Installed: &installed, ReplaceInstances: instances, Operation: operation, CompleteOperation: true, Audit: pluginLifecycleAudit(operation, applyResult.ActorID, auditResult, operation.ErrorClass, now)}
	if applyResult.Applied {
		mutation.ReplaceGrants = grants
	}
	err = s.store.ApplyPluginMutation(ctx, mutation)
	return installed, err
}

func (s *PluginService) Uninstall(ctx context.Context, request PluginUninstallRequest) error {
	installed, ok, err := s.store.GetInstalledPlugin(ctx, request.PluginID)
	if err != nil {
		return err
	}
	operation := s.operation(ctx, request.PluginID, "uninstall", installed.ActivePackageDigest, request.ActorID)
	if !ok {
		return s.recordFailure(ctx, operation, request.ActorID, ErrPluginNotInstalled)
	}
	if err := bindInstalledActiveOperation(&operation, installed); err != nil {
		return err
	}
	packageRow, exists, err := s.storedPackage(ctx, installed.ActivePackageIdentity, installed.ActivePackageDigest)
	if err != nil {
		return err
	}
	if !exists {
		return s.recordFailure(ctx, operation, request.ActorID, errors.New("active plugin package is unavailable"))
	}
	if installed.CurrentLifecycle != "disabled" {
		return s.recordFailure(ctx, operation, request.ActorID, ErrPluginUninstallBlocked)
	}
	if err := ensureNoPendingOperation(installed); err != nil {
		return s.recordFailure(ctx, operation, request.ActorID, err)
	}
	if err := s.validateStoredPackageIntegrity(ctx, packageRow); err != nil {
		return s.recordFailure(ctx, operation, request.ActorID, err)
	}
	var manifest plugins.Manifest
	if err := json.Unmarshal([]byte(packageRow.ManifestJSON), &manifest); err != nil {
		return s.recordFailure(ctx, operation, request.ActorID, err)
	}
	var projectedCleanup plugins.CleanupPolicy
	if err := json.Unmarshal([]byte(installed.CleanupPolicyJSON), &projectedCleanup); err != nil || projectedCleanup != manifest.Cleanup {
		return s.recordFailure(ctx, operation, request.ActorID, errors.New("persisted plugin cleanup policy differs from verified package"))
	}
	cleanup := manifest.Cleanup
	now := s.now()
	operation.Status, operation.CompletedAt = "succeeded", &now
	return s.store.ApplyPluginMutation(ctx, storage.PluginMutation{PluginID: request.PluginID, ExpectedActive: installed.ActivePackageDigest, ExpectedStateVersion: installed.StateVersion, DeletePlugin: true, DeleteInstances: cleanup.Instances == "delete", DeleteGrants: cleanup.Grants == "delete", Operation: operation, Audit: pluginLifecycleAudit(operation, request.ActorID, "success", "", now)})
}

func (s *PluginService) operation(ctx context.Context, pluginID, kind, digest, fallbackActorID string) storage.PluginOperationRow {
	id := lifecycleID("pluginop")
	if boundID, ok := ctx.Value(pluginLifecycleOperationContextKey{}).(string); ok && strings.TrimSpace(boundID) != "" {
		id = strings.TrimSpace(boundID)
	}
	actorID, sessionID, correlationID := strings.TrimSpace(fallbackActorID), "", id
	if actor, ok := storage.QuotaActorFromContext(ctx); ok {
		actorID, sessionID = actor.UserID, actor.SessionID
		if strings.TrimSpace(actor.CorrelationID) != "" {
			correlationID = actor.CorrelationID
		}
	}
	return storage.PluginOperationRow{ID: id, PluginID: pluginID, Kind: kind, Status: "running", TargetPackageDigest: strings.ToLower(digest), AgentResultsJSON: "{}", ActorID: actorID, SessionID: sessionID, CorrelationID: correlationID, CreatedAt: s.now()}
}

func ensureNoPendingOperation(installed storage.InstalledPluginRow) error {
	if installed.PendingOperationID != "" || installed.StagedPackageDigest != "" {
		return fmt.Errorf("%w: another plugin operation is already pending", ErrPluginConflict)
	}
	return nil
}

func setPendingOperation(installed *storage.InstalledPluginRow, operation storage.PluginOperationRow) {
	installed.PendingOperationID = operation.ID
	installed.PendingKind = operation.Kind
	installed.PendingTargetDigest = operation.TargetPackageDigest
	installed.PendingTargetIdentity = operation.TargetPackageIdentity
	installed.PendingRevision = operation.TargetRevision
}

func clearPendingOperation(installed *storage.InstalledPluginRow) {
	installed.PendingOperationID = ""
	installed.PendingKind = ""
	installed.PendingTargetDigest = ""
	installed.PendingTargetIdentity = ""
	installed.PendingRevision = 0
}

func clearStagedSource(installed *storage.InstalledPluginRow) {
	installed.StagedPackageIdentity = ""
	installed.StagedPackageDigest = ""
	installed.StagedSourceID = ""
	installed.StagedSourceKind = ""
	installed.StagedSourceRiskLabel = ""
	installed.StagedSourceRevision = 0
	installed.StagedSourceRefKind = ""
	installed.StagedSourceRefName = ""
	installed.StagedSourceResolvedOID = ""
	installed.StagedSignatureKeyID = ""
	installed.StagedSignaturePublicKey = ""
	installed.StagedSignatureFingerprint = ""
	installed.PendingGrantsJSON = "[]"
}

func pluginGenerationGrants(permissions []plugins.Permission) []storage.PluginGenerationGrant {
	result := make([]storage.PluginGenerationGrant, 0, len(permissions))
	for _, permission := range permissions {
		kind, id := splitPluginResourceSelector(permission.Resource)
		result = append(result, storage.PluginGenerationGrant{Name: strings.TrimSpace(permission.Name), ResourceKind: kind, ResourceID: id})
	}
	return result
}

func splitPluginResourceSelector(selector string) (string, string) {
	parts := strings.SplitN(strings.TrimSpace(selector), ":", 2)
	if len(parts) == 1 {
		return "", parts[0]
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}

func lifecycleCurrentState(desired string) string {
	if desired == "enabled" {
		return "active"
	}
	return "disabled"
}

func (s *PluginService) pendingOperation(ctx context.Context, installed storage.InstalledPluginRow, result PluginApplyResult, kinds ...string) (storage.PluginOperationRow, error) {
	if result.OperationID == "" || result.OperationID != installed.PendingOperationID || result.TargetRevision != installed.PendingRevision || !strings.EqualFold(result.TargetDigest, installed.PendingTargetDigest) {
		return storage.PluginOperationRow{}, fmt.Errorf("%w: plugin apply result is stale, replayed, or out of order", ErrPluginConflict)
	}
	allowed := false
	for _, kind := range kinds {
		if installed.PendingKind == kind {
			allowed = true
			break
		}
	}
	if !allowed {
		return storage.PluginOperationRow{}, errors.New("plugin apply result kind does not match pending operation")
	}
	operations, err := s.store.ListPluginOperations(ctx, installed.PluginID)
	if err != nil {
		return storage.PluginOperationRow{}, err
	}
	for _, operation := range operations {
		if operation.ID == result.OperationID {
			if operation.CompletedAt != nil || operation.Kind != installed.PendingKind || operation.TargetRevision != result.TargetRevision || !strings.EqualFold(operation.TargetPackageDigest, result.TargetDigest) {
				return storage.PluginOperationRow{}, errors.New("plugin apply result does not match durable pending operation")
			}
			return operation, nil
		}
	}
	return storage.PluginOperationRow{}, errors.New("durable pending plugin operation is unavailable")
}

func (s *PluginService) recordFailure(ctx context.Context, operation storage.PluginOperationRow, actorID string, cause error) error {
	now := s.now()
	operation.Status, operation.ErrorClass, operation.Error, operation.CompletedAt = "failed", pluginErrorClass(cause), cause.Error(), &now
	if err := s.store.RecordPluginOperation(ctx, operation, pluginLifecycleAudit(operation, actorID, "failure", operation.ErrorClass, now)); err != nil {
		return fmt.Errorf("%w (persist operation failure: %v)", cause, err)
	}
	return cause
}

func (s *PluginService) validatePackageCandidate(candidate PluginPackageCandidate) error {
	digest := strings.ToLower(strings.TrimSpace(candidate.Package.Digest))
	if candidate.Package.Manifest.ID == "" || candidate.Package.Manifest.Version == "" || !isHexDigest(digest) || candidate.CachePath == "" || len(candidate.CachePath) > plugins.MaxPackagePathBytes {
		return errors.New("validated digest-addressed package candidate is required")
	}
	if err := marketplace.ValidateCachePath(s.cacheRoot, candidate.CachePath, digest, candidate.SignatureTrust.Fingerprint); err != nil {
		return fmt.Errorf("validate verified cache path: %w", err)
	}
	if !reflect.DeepEqual(candidate.Runtime, candidate.Package.Manifest.Runtime) || !reflect.DeepEqual(candidate.Artifacts, candidate.Package.Manifest.Artifacts) || candidate.SignatureTrust.KeyID != candidate.Package.Manifest.Signature.KeyID {
		return errors.New("package candidate runtime, artifacts, or signature key binding differs from its verified manifest")
	}
	if err := marketplace.ValidateSignatureTrust(candidate.SignatureTrust); err != nil {
		return err
	}
	if candidate.requireAcquisition && (candidate.SignatureTrust.SourceID != candidate.sourceID || candidate.SignatureTrust.SourceKind != candidate.sourceKind) {
		return errors.New("package candidate signer binding differs from its marketplace acquisition")
	}
	if s.validator == nil {
		return errors.New("plugin compatibility validator is unavailable")
	}
	validator := candidate.validator
	if validator == nil {
		validator = s.validator
	}
	revalidated, err := validator.ValidatePackage(candidate.CachePath, plugins.PackageExpectation{ID: candidate.Package.Manifest.ID, Version: candidate.Package.Manifest.Version, SHA256: digest, SignatureKeyID: candidate.Package.Manifest.Signature.KeyID})
	if err != nil {
		return fmt.Errorf("revalidate cached package: %w", err)
	}
	if !strings.EqualFold(revalidated.Digest, digest) {
		return errors.New("verified cache contents do not match package digest")
	}
	if !reflect.DeepEqual(revalidated.Manifest, candidate.Package.Manifest) || !reflect.DeepEqual(revalidated.ConfigSchema, candidate.Package.ConfigSchema) {
		return errors.New("package candidate projection differs from verified cache")
	}
	return nil
}

func (s *PluginService) revalidateInstalledPackageVariant(ctx context.Context, identity, digest string) error {
	row, ok, err := s.storedPackage(ctx, identity, digest)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("installed plugin package is unavailable")
	}
	return s.validateStoredPackage(ctx, row)
}

func (s *PluginService) storedPackage(ctx context.Context, identity, digest string) (storage.PluginPackageRow, bool, error) {
	if identity != "" {
		return s.store.GetPluginPackageByIdentity(ctx, identity)
	}
	return s.store.GetPluginPackage(ctx, digest)
}

func (s *PluginService) storedArtifacts(ctx context.Context, identity, digest string) ([]storage.PluginArtifactRow, error) {
	if identity != "" {
		return s.store.ListPluginArtifactsByIdentity(ctx, identity)
	}
	return s.store.ListPluginArtifacts(ctx, digest)
}

func (s *PluginService) validateStoredPackage(ctx context.Context, row storage.PluginPackageRow) error {
	if err := marketplace.ValidateCachePath(s.cacheRoot, row.CachePath, row.Digest, row.SignatureFingerprint); err != nil {
		return fmt.Errorf("validate installed package cache path: %w", err)
	}
	validator, err := s.packageBoundValidator(row)
	if err != nil {
		return err
	}
	validated, err := validator.ValidatePackage(row.CachePath, plugins.PackageExpectation{ID: row.PluginID, Version: row.Version, SHA256: row.Digest, SignatureKeyID: row.SignatureKeyID})
	if err != nil {
		return fmt.Errorf("revalidate installed package: %w", err)
	}
	artifacts, err := s.storedArtifacts(ctx, row.Identity, row.Digest)
	if err != nil {
		return err
	}
	return validateStoredPackageProjection(row, artifacts, validated)
}

func (s *PluginService) validateStoredPackageIntegrity(ctx context.Context, row storage.PluginPackageRow) error {
	if err := marketplace.ValidateCachePath(s.cacheRoot, row.CachePath, row.Digest, row.SignatureFingerprint); err != nil {
		return fmt.Errorf("validate installed package cache path: %w", err)
	}
	validator, err := s.packageBoundValidator(row)
	if err != nil {
		return err
	}
	validated, err := validator.ValidatePackageIntegrity(row.CachePath, plugins.PackageExpectation{ID: row.PluginID, Version: row.Version, SHA256: row.Digest, SignatureKeyID: row.SignatureKeyID})
	if err != nil {
		return fmt.Errorf("revalidate installed package integrity: %w", err)
	}
	artifacts, err := s.storedArtifacts(ctx, row.Identity, row.Digest)
	if err != nil {
		return err
	}
	return validateStoredPackageProjection(row, artifacts, validated)
}

func (s *PluginService) packageBoundValidator(row storage.PluginPackageRow) (*plugins.Validator, error) {
	trust := marketplace.SignatureTrust{SourceID: row.SourceID, SourceKind: row.SourceKind, KeyID: row.SignatureKeyID, PublicKey: row.SignaturePublicKey, Fingerprint: row.SignatureFingerprint}
	return marketplace.ValidatorForSignatureTrustWithBase(s.validator, trust)
}

func validateStoredPackageProjection(row storage.PluginPackageRow, artifacts []storage.PluginArtifactRow, validated plugins.ValidatedPackage) error {
	var projectedManifest plugins.Manifest
	if err := json.Unmarshal([]byte(row.ManifestJSON), &projectedManifest); err != nil || !reflect.DeepEqual(projectedManifest, validated.Manifest) {
		return errors.New("persisted plugin manifest differs from verified cache")
	}
	projectedSchema, err := plugins.DecodeConfigSchema([]byte(row.ConfigSchemaJSON))
	if err != nil || !reflect.DeepEqual(projectedSchema, validated.ConfigSchema) {
		return errors.New("persisted plugin config schema differs from verified cache")
	}
	expectedUI, err := json.Marshal(validated.DeclarativeUI)
	if validated.DeclarativeUI == nil {
		expectedUI = nil
	}
	if err != nil || (strings.TrimSpace(row.UISchemaJSON) != "" && string(expectedUI) != row.UISchemaJSON) {
		return errors.New("persisted plugin declarative UI differs from verified cache")
	}
	projectedRow, projectedArtifacts, err := storage.ProjectPluginPackage(row, validated.Manifest)
	sort.Slice(projectedArtifacts, func(i, j int) bool { return projectedArtifacts[i].Path < projectedArtifacts[j].Path })
	if err != nil || projectedRow.RuntimeKind != row.RuntimeKind || projectedRow.RuntimeABI != row.RuntimeABI || projectedRow.HostScope != row.HostScope || projectedRow.PolicyKind != row.PolicyKind || projectedRow.EntryPath != row.EntryPath || projectedRow.SignatureKeyID != row.SignatureKeyID || projectedRow.SignatureVerdict != row.SignatureVerdict || projectedRow.ResourceBudgetJSON != row.ResourceBudgetJSON || projectedRow.FailurePolicyJSON != row.FailurePolicyJSON || !reflect.DeepEqual(projectedArtifacts, artifacts) {
		return errors.New("persisted plugin runtime projection differs from verified cache")
	}
	return nil
}

func bindOperationCandidate(operation *storage.PluginOperationRow, candidate PluginPackageCandidate) error {
	return storage.BindPluginOperationPackage(operation, storage.PluginPackageRow{
		Identity:             storage.PluginPackageIdentity(candidate.Package.Digest, candidate.SignatureTrust.SourceID, candidate.SignatureTrust.Fingerprint),
		Digest:               candidate.Package.Digest,
		SourceID:             candidate.SignatureTrust.SourceID,
		SourceKind:           candidate.SignatureTrust.SourceKind,
		SourceRiskLabel:      candidate.sourceRiskLabel,
		SourceRevision:       candidate.sourceRevision,
		SourceRefKind:        candidate.sourceRefKind,
		SourceRefName:        candidate.sourceRefName,
		SourceResolvedOID:    candidate.sourceResolvedOID,
		SignatureKeyID:       candidate.SignatureTrust.KeyID,
		SignaturePublicKey:   candidate.SignatureTrust.PublicKey,
		SignatureFingerprint: candidate.SignatureTrust.Fingerprint,
	})
}

func bindInstalledActiveOperation(operation *storage.PluginOperationRow, installed storage.InstalledPluginRow) error {
	return storage.BindPluginOperationPackage(operation, storage.PluginPackageRow{
		Identity: installed.ActivePackageIdentity, Digest: installed.ActivePackageDigest,
		SourceID: installed.ActiveSourceID, SourceKind: installed.ActiveSourceKind, SourceRiskLabel: installed.ActiveSourceRiskLabel,
		SourceRevision: installed.ActiveSourceRevision, SourceRefKind: installed.ActiveSourceRefKind, SourceRefName: installed.ActiveSourceRefName, SourceResolvedOID: installed.ActiveSourceResolvedOID,
		SignatureKeyID: installed.ActiveSignatureKeyID, SignaturePublicKey: installed.ActiveSignaturePublicKey, SignatureFingerprint: installed.ActiveSignatureFingerprint,
	})
}

func bindInstalledRollbackOperation(operation *storage.PluginOperationRow, installed storage.InstalledPluginRow) error {
	return storage.BindPluginOperationPackage(operation, storage.PluginPackageRow{
		Identity: installed.RollbackPackageIdentity, Digest: installed.RollbackPackageDigest,
		SourceID: installed.RollbackSourceID, SourceKind: installed.RollbackSourceKind, SourceRiskLabel: installed.RollbackSourceRiskLabel,
		SourceRevision: installed.RollbackSourceRevision, SourceRefKind: installed.RollbackSourceRefKind, SourceRefName: installed.RollbackSourceRefName, SourceResolvedOID: installed.RollbackSourceResolvedOID,
		SignatureKeyID: installed.RollbackSignatureKeyID, SignaturePublicKey: installed.RollbackSignaturePublicKey, SignatureFingerprint: installed.RollbackSignatureFingerprint,
	})
}

func pluginPackageRows(candidate PluginPackageCandidate, manifestJSON, schemaJSON []byte, now time.Time) (storage.PluginPackageRow, []storage.PluginArtifactRow, error) {
	digest := strings.ToLower(candidate.Package.Digest)
	uiJSON, err := json.Marshal(candidate.Package.DeclarativeUI)
	if err != nil {
		return storage.PluginPackageRow{}, nil, err
	}
	if candidate.Package.DeclarativeUI == nil {
		uiJSON = nil
	}
	row := storage.PluginPackageRow{Identity: storage.PluginPackageIdentity(digest, candidate.SignatureTrust.SourceID, candidate.SignatureTrust.Fingerprint), Digest: digest, PluginID: candidate.Package.Manifest.ID, Version: candidate.Package.Manifest.Version, SourceID: candidate.SignatureTrust.SourceID, SourceKind: candidate.SignatureTrust.SourceKind, SourceRiskLabel: candidate.sourceRiskLabel, SourceRevision: candidate.sourceRevision, SourceRefKind: candidate.sourceRefKind, SourceRefName: candidate.sourceRefName, SourceResolvedOID: candidate.sourceResolvedOID, SignatureKeyID: candidate.SignatureTrust.KeyID, SignaturePublicKey: candidate.SignatureTrust.PublicKey, SignatureFingerprint: candidate.SignatureTrust.Fingerprint, CachePath: candidate.CachePath, ManifestJSON: string(manifestJSON), ConfigSchemaJSON: string(schemaJSON), UISchemaJSON: string(uiJSON), VerifiedAt: now}
	return storage.ProjectPluginPackage(row, candidate.Package.Manifest)
}

func confirmCandidateSource(candidate PluginPackageCandidate, accepted bool) error {
	if strings.TrimSpace(candidate.sourceID) == "" {
		return errors.New("verified package source provenance is required")
	}
	switch candidate.sourceKind {
	case marketplace.SourceKindOfficial:
		if candidate.sourceID != marketplace.OfficialSourceID || candidate.sourceRiskLabel != "" {
			return errors.New("official package source provenance is invalid")
		}
	case marketplace.SourceKindCustom:
		if candidate.sourceID == marketplace.OfficialSourceID || candidate.sourceRiskLabel != marketplace.UntrustedRiskLabel {
			return errors.New("custom package source provenance is invalid")
		}
	default:
		return errors.New("unknown marketplace source kind")
	}
	return confirmSourceRisk(candidate.sourceKind, accepted)
}

func clearPendingInstance(instance *storage.PluginInstanceRow) {
	instance.PendingConfigJSON = ""
	instance.PendingResourceGroupID = ""
	instance.PendingTargetJSON = ""
	instance.PendingPolicyChainsJSON = "[]"
	instance.PendingBindingsJSON = "[]"
	instance.PendingSecretHandlesJSON = "[]"
	instance.PendingVersion = 0
	instance.PendingOperationID = ""
}

func (s *PluginService) validateStoredPluginBindings(ctx context.Context, instance storage.PluginInstanceRow, manifest plugins.Manifest) error {
	bindings, err := storage.CanonicalPluginInstanceBindings(instance.BindingsJSON)
	if err != nil {
		return fmt.Errorf("plugin instance %s bindings are invalid: %w", instance.ID, err)
	}
	if len(bindings) == 0 {
		return nil
	}
	if manifest.Runtime.Kind != "rpc-service" || manifest.Runtime.HostScope != "agent" {
		return fmt.Errorf("plugin instance %s core consumer bindings require an Agent rpc-service package", instance.ID)
	}
	defaultTargetID, err := s.defaultPluginTargetID(ctx)
	if err != nil {
		return err
	}
	targetIDs, err := pluginTargetIDs(json.RawMessage(instance.TargetJSON), defaultTargetID)
	if err != nil {
		return err
	}
	if err := storage.ValidatePluginInstanceBindingScope(bindings, targetIDs, manifest.ExtensionPoints); err != nil {
		return fmt.Errorf("plugin instance %s bindings are incompatible: %w", instance.ID, err)
	}
	return nil
}

func (s *PluginService) failPendingPackagePromotion(ctx context.Context, installed storage.InstalledPluginRow, operation storage.PluginOperationRow, cause error) error {
	instances, err := s.store.ListPluginInstances(ctx, installed.PluginID)
	if err != nil {
		return errors.Join(cause, err)
	}
	now := s.now()
	for index := range instances {
		if instances[index].PendingOperationID == operation.ID {
			clearPendingInstance(&instances[index])
			instances[index].CurrentState = lifecycleCurrentState(installed.DesiredLifecycle)
			instances[index].UpdatedAt = now
		}
	}
	clearStagedSource(&installed)
	installed.CurrentLifecycle = lifecycleCurrentState(installed.DesiredLifecycle)
	clearPendingOperation(&installed)
	installed.LastOperationID, installed.UpdatedAt = operation.ID, now
	operation.Status, operation.ErrorClass, operation.Error, operation.CompletedAt = "failed", "package_integrity", cause.Error(), &now
	err = s.store.ApplyPluginMutation(ctx, storage.PluginMutation{PluginID: installed.PluginID, ExpectedActive: installed.ActivePackageDigest, ExpectedStateVersion: installed.StateVersion, ExpectedPendingOperationID: operation.ID, Installed: &installed, ReplaceInstances: instances, Operation: operation, CompleteOperation: true, Audit: pluginLifecycleAudit(operation, operation.ActorID, "failure", operation.ErrorClass, now)})
	if err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

func (s *PluginService) validateAgentTargets(ctx context.Context, constraint string, raw json.RawMessage) error {
	defaultTargetID, err := s.defaultPluginTargetID(ctx)
	if err != nil {
		return err
	}
	targetIDs, err := pluginTargetIDs(raw, defaultTargetID)
	if err != nil {
		return err
	}
	byID, err := s.authoritativePluginAgents(ctx)
	if err != nil {
		return err
	}
	seen := map[string]struct{}{}
	for _, targetID := range targetIDs {
		targetID = strings.TrimSpace(targetID)
		if targetID == "" {
			return errors.New("plugin target agent ID is required")
		}
		if _, duplicate := seen[targetID]; duplicate {
			continue
		}
		seen[targetID] = struct{}{}
		agent, ok := byID[targetID]
		if !ok {
			return fmt.Errorf("plugin target agent %s is unavailable", targetID)
		}
		if err := plugins.CheckAgentCompatibility(agent.Version, constraint); err != nil {
			return fmt.Errorf("plugin target agent %s: %w", targetID, err)
		}
		var capabilities []string
		if err := json.Unmarshal([]byte(agent.CapabilitiesJSON), &capabilities); err != nil {
			return fmt.Errorf("plugin target agent %s capabilities are unavailable", targetID)
		}
		supported := false
		for _, capability := range capabilities {
			if capability == "package_manifest_v1" {
				supported = true
				break
			}
		}
		if !supported {
			return fmt.Errorf("plugin target agent %s lacks package_manifest_v1 capability", targetID)
		}
	}
	return nil
}

func (s *PluginService) authoritativePluginAgents(ctx context.Context) (map[string]storage.AgentRow, error) {
	agents, err := s.store.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]storage.AgentRow, len(agents)+1)
	for _, agent := range agents {
		if agent.IsLocal || strings.EqualFold(strings.TrimSpace(agent.Mode), "local") {
			continue
		}
		byID[agent.ID] = agent
	}
	localID, version, present, err := s.store.LocalAgentBuild(ctx)
	if err != nil {
		return nil, err
	}
	localID = strings.TrimSpace(localID)
	if localID == "" {
		return byID, nil
	}
	if !present {
		delete(byID, localID)
		return byID, nil
	}
	local := byID[localID]
	local.ID = localID
	if buildVersion := strings.TrimSpace(version); buildVersion != "" {
		local.Version = buildVersion
	}
	local.CapabilitiesJSON = marshalStringArray(defaultLocalCapabilities)
	local.IsLocal = true
	local.Mode = "local"
	if stateProvider, ok := s.store.(interface {
		LoadLocalAgentState(context.Context) (storage.LocalAgentStateRow, error)
	}); ok {
		state, err := stateProvider.LoadLocalAgentState(ctx)
		if err != nil {
			return nil, err
		}
		local.DesiredVersion = state.DesiredVersion
		local.DesiredRevision = state.DesiredRevision
		local.CurrentRevision = state.CurrentRevision
		local.LastApplyRevision = state.LastApplyRevision
		local.LastApplyStatus = state.LastApplyStatus
		local.LastApplyMessage = state.LastApplyMessage
		if local.Version == "" {
			local.Version = strings.TrimSpace(state.Version)
		}
	}
	byID[localID] = local
	return byID, nil
}

func (s *PluginService) defaultPluginTargetID(ctx context.Context) (string, error) {
	localID, _, _, err := s.store.LocalAgentBuild(ctx)
	if err != nil {
		return "", err
	}
	localID = strings.TrimSpace(localID)
	if localID == "" {
		return "", errors.New("local plugin target identity is unavailable")
	}
	return localID, nil
}

func pluginTargetIDs(raw json.RawMessage, defaultTargetID string) ([]string, error) {
	var targetIDs []string
	trimmed := strings.TrimSpace(string(raw))
	if trimmed != "" && trimmed != "null" {
		if err := json.Unmarshal(raw, &targetIDs); err != nil {
			return nil, errors.New("plugin targets must be an array of agent IDs")
		}
	}
	if len(targetIDs) == 0 {
		defaultTargetID = strings.TrimSpace(defaultTargetID)
		if defaultTargetID == "" {
			return nil, errors.New("default plugin target agent ID is unavailable")
		}
		targetIDs = []string{defaultTargetID}
	}
	return targetIDs, nil
}

func confirmSourceRisk(kind string, accepted bool) error {
	switch kind {
	case marketplace.SourceKindOfficial:
		return nil
	case marketplace.SourceKindCustom:
		if !accepted {
			return ErrPluginRiskConfirmation
		}
		return nil
	default:
		return errors.New("unknown marketplace source kind")
	}
}

func isHexDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func confirmPermissions(permissions []plugins.Permission, confirmed []string, required bool) error {
	expected := normalizedPermissions(permissions)
	actual := append([]string(nil), confirmed...)
	for index := range actual {
		actual[index] = strings.TrimSpace(actual[index])
	}
	sort.Strings(actual)
	if !required && len(actual) == 0 {
		return nil
	}
	if len(expected) != len(actual) {
		return ErrPluginPermissionConfirmation
	}
	for index := range expected {
		if expected[index] != actual[index] {
			return ErrPluginPermissionConfirmation
		}
	}
	return nil
}

func normalizedPermissions(permissions []plugins.Permission) []string {
	result := make([]string, 0, len(permissions))
	for _, permission := range permissions {
		value := strings.TrimSpace(permission.Name)
		if resource := strings.TrimSpace(permission.Resource); resource != "" {
			value += ":" + resource
		}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func pluginSummary(row storage.InstalledPluginRow) PluginSummary {
	return PluginSummary{
		PluginID: row.PluginID, ActivePackageDigest: row.ActivePackageDigest,
		RuntimeKind: row.RuntimeKind, RuntimeABI: row.RuntimeABI, HostScope: row.HostScope,
		ActiveSourceID: row.ActiveSourceID, ActiveSourceKind: row.ActiveSourceKind, ActiveSourceRiskLabel: row.ActiveSourceRiskLabel,
		ActiveSourceRevision: row.ActiveSourceRevision, ActiveSourceRefKind: row.ActiveSourceRefKind, ActiveSourceRefName: row.ActiveSourceRefName, ActiveSourceResolvedOID: row.ActiveSourceResolvedOID,
		StagedPackageDigest: row.StagedPackageDigest, StagedSourceID: row.StagedSourceID, StagedSourceKind: row.StagedSourceKind, StagedSourceRiskLabel: row.StagedSourceRiskLabel,
		StagedSourceRevision: row.StagedSourceRevision, StagedSourceRefKind: row.StagedSourceRefKind, StagedSourceRefName: row.StagedSourceRefName, StagedSourceResolvedOID: row.StagedSourceResolvedOID,
		RollbackPackageDigest: row.RollbackPackageDigest, RollbackSourceID: row.RollbackSourceID, RollbackSourceKind: row.RollbackSourceKind, RollbackSourceRiskLabel: row.RollbackSourceRiskLabel,
		RollbackSourceRevision: row.RollbackSourceRevision, RollbackSourceRefKind: row.RollbackSourceRefKind, RollbackSourceRefName: row.RollbackSourceRefName, RollbackSourceResolvedOID: row.RollbackSourceResolvedOID,
		DesiredLifecycle: row.DesiredLifecycle, CurrentLifecycle: row.CurrentLifecycle, LastOperationID: row.LastOperationID, StateVersion: row.StateVersion,
		PendingOperationID: row.PendingOperationID, PendingKind: row.PendingKind, PendingTargetDigest: row.PendingTargetDigest, PendingRevision: row.PendingRevision,
		InstalledAt: row.InstalledAt, UpdatedAt: row.UpdatedAt,
	}
}

func (s *PluginService) pluginInstanceDetails(ctx context.Context, rows []storage.PluginInstanceRow) ([]PluginInstanceDetail, error) {
	defaultTargetID, err := s.defaultPluginTargetID(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]PluginInstanceDetail, 0, len(rows))
	schemas := make(map[string]map[string]any)
	for _, row := range rows {
		targets, err := pluginTargetIDs(json.RawMessage(row.TargetJSON), defaultTargetID)
		if err != nil {
			return nil, ErrPluginReadProjection
		}
		schema := schemas[row.PluginID]
		if schema == nil {
			installed, err := s.installedPlugin(ctx, row.PluginID)
			if err != nil {
				return nil, err
			}
			packageRow, ok, err := s.storedPackage(ctx, installed.ActivePackageIdentity, installed.ActivePackageDigest)
			if err != nil || !ok {
				return nil, errors.Join(ErrPluginReadProjection, err)
			}
			schema, err = plugins.DecodeConfigSchema([]byte(packageRow.ConfigSchemaJSON))
			if err != nil {
				return nil, ErrPluginReadProjection
			}
			schemas[row.PluginID] = schema
		}
		config, secretFields, err := pluginRedactedConfig(schema, row.ConfigJSON)
		if err != nil {
			return nil, err
		}
		secretFields, err = pluginSecretFieldStates(row.SecretHandlesJSON, secretFields)
		if err != nil {
			return nil, err
		}
		statusSummary, err := pluginReadStatusObject(row.StatusSummaryJSON)
		if err != nil {
			return nil, err
		}
		policyChains, err := storage.CanonicalPluginPolicyChains(row.PolicyChainsJSON)
		if err != nil {
			return nil, ErrPluginReadProjection
		}
		bindings, err := storage.CanonicalPluginInstanceBindings(row.BindingsJSON)
		if err != nil {
			return nil, ErrPluginReadProjection
		}
		detail := PluginInstanceDetail{ID: row.ID, PluginID: row.PluginID, ResourceGroupID: row.ResourceGroupID, Targets: targets, PolicyChains: policyChains, Bindings: bindings, Config: config, SecretFields: secretFields, ConfigVersion: row.ConfigVersion, PendingVersion: row.PendingVersion, PendingOperationID: row.PendingOperationID, PendingResourceGroupID: row.PendingResourceGroupID, DesiredEnabled: row.DesiredEnabled, CurrentState: row.CurrentState, StatusSummary: statusSummary, StateVersion: row.StateVersion, UpdatedAt: row.UpdatedAt}
		if strings.TrimSpace(row.PendingConfigJSON) != "" {
			detail.PendingConfig, detail.PendingSecretFields, err = pluginRedactedConfig(schema, row.PendingConfigJSON)
			if err != nil {
				return nil, err
			}
			detail.PendingSecretFields, err = pluginSecretFieldStates(row.PendingSecretHandlesJSON, detail.PendingSecretFields)
			if err != nil {
				return nil, err
			}
		}
		if strings.TrimSpace(row.PendingTargetJSON) != "" || strings.TrimSpace(row.PendingResourceGroupID) != "" {
			detail.PendingTargets, err = pluginTargetIDs(json.RawMessage(row.PendingTargetJSON), defaultTargetID)
			if err != nil {
				return nil, ErrPluginReadProjection
			}
		}
		if row.PendingOperationID != "" {
			detail.PendingPolicyChains, err = storage.CanonicalPluginPolicyChains(row.PendingPolicyChainsJSON)
			if err != nil {
				return nil, ErrPluginReadProjection
			}
			detail.PendingBindings, err = storage.CanonicalPluginInstanceBindings(row.PendingBindingsJSON)
			if err != nil {
				return nil, ErrPluginReadProjection
			}
		}
		result = append(result, detail)
	}
	return result, nil
}

func pluginGrantDetails(rows []storage.PluginGrantRow) []PluginGrantDetail {
	result := make([]PluginGrantDetail, 0, len(rows))
	for _, row := range rows {
		result = append(result, PluginGrantDetail{PackageDigest: row.PackageDigest, Permission: row.Permission, ResourceSelector: row.ResourceSelector, GrantedBy: row.GrantedBy, GrantedAt: row.GrantedAt})
	}
	return result
}

func pluginReadJSONObject(raw string) (json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &object); err != nil || object == nil {
		return nil, ErrPluginReadProjection
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return nil, ErrPluginReadProjection
	}
	return json.RawMessage(encoded), nil
}

func pluginReadStatusObject(raw string) (json.RawMessage, error) {
	if strings.TrimSpace(raw) == "null" {
		return json.RawMessage(`{}`), nil
	}
	return pluginReadJSONObject(raw)
}

func encodePluginResultObject(value any) (string, error) {
	if value == nil {
		return `{}`, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("%w: plugin agent results must be a JSON object", ErrInvalidArgument)
	}
	if strings.TrimSpace(string(encoded)) == "null" {
		return `{}`, nil
	}
	object, err := pluginReadJSONObject(string(encoded))
	if err != nil {
		return "", fmt.Errorf("%w: plugin agent results must be a JSON object", ErrInvalidArgument)
	}
	return string(object), nil
}

func pluginPackageDetail(row storage.PluginPackageRow, artifactRows []storage.PluginArtifactRow, grants []storage.PluginGrantRow, activeDigest string) (PluginPackageDetail, error) {
	var manifest plugins.Manifest
	if err := json.Unmarshal([]byte(row.ManifestJSON), &manifest); err != nil {
		return PluginPackageDetail{}, err
	}
	schema, err := plugins.DecodeConfigSchema([]byte(row.ConfigSchemaJSON))
	if err != nil {
		return PluginPackageDetail{}, err
	}
	var declarativeUI *plugins.DeclarativeUIDocument
	if strings.TrimSpace(row.UISchemaJSON) != "" {
		if err := json.Unmarshal([]byte(row.UISchemaJSON), &declarativeUI); err != nil || declarativeUI == nil {
			return PluginPackageDetail{}, ErrPluginReadProjection
		}
	}
	permissions := normalizedPermissions(manifest.Permissions)
	current := make([]string, 0, len(grants))
	for _, grant := range grants {
		if activeDigest != "" && !strings.EqualFold(grant.PackageDigest, activeDigest) {
			continue
		}
		value := strings.TrimSpace(grant.Permission)
		if selector := strings.TrimSpace(grant.ResourceSelector); selector != "" {
			value += ":" + selector
		}
		current = append(current, value)
	}
	sort.Strings(current)
	artifacts := make([]PluginArtifactDetail, 0, len(artifactRows))
	for _, artifact := range artifactRows {
		artifacts = append(artifacts, PluginArtifactDetail{Path: artifact.Path, SHA256: artifact.SHA256, Size: artifact.SizeBytes, Mode: artifact.Mode, GOOS: artifact.GOOS, GOARCH: artifact.GOARCH})
	}
	return PluginPackageDetail{Digest: strings.ToLower(row.Digest), Version: row.Version, Runtime: manifest.Runtime, Artifacts: artifacts, ResourceBudget: manifest.ResourceBudget, FailurePolicy: manifest.FailurePolicy, Signature: manifest.Signature, Manifest: manifest, ConfigSchema: pluginPublicConfigSchema(schema), Permissions: permissions, PermissionDiff: permissionDiff(current, permissions), DeclarativeUI: declarativeUI}, nil
}

func permissionDiff(current, desired []string) PluginPermissionDiff {
	currentSet, desiredSet := map[string]struct{}{}, map[string]struct{}{}
	for _, permission := range current {
		currentSet[permission] = struct{}{}
	}
	for _, permission := range desired {
		desiredSet[permission] = struct{}{}
	}
	diff := PluginPermissionDiff{Added: []string{}, Removed: []string{}}
	for _, permission := range desired {
		if _, ok := currentSet[permission]; !ok {
			diff.Added = append(diff.Added, permission)
		}
	}
	for _, permission := range current {
		if _, ok := desiredSet[permission]; !ok {
			diff.Removed = append(diff.Removed, permission)
		}
	}
	return diff
}

func (s *PluginService) pluginAgentStatuses(ctx context.Context, installed storage.InstalledPluginRow, instances []storage.PluginInstanceRow, operations []storage.PluginOperationRow) ([]PluginAgentStatus, error) {
	byID, err := s.authoritativePluginAgents(ctx)
	if err != nil {
		return nil, err
	}
	defaultTargetID, err := s.defaultPluginTargetID(ctx)
	if err != nil {
		return nil, err
	}
	operationsByID := make(map[string]storage.PluginOperationRow, len(operations))
	runtimeStatuses := make(map[string]storage.PluginAgentRuntimeStatusRow)
	for _, operation := range operations {
		operationsByID[operation.ID] = operation
		if runtimeStore, ok := s.store.(pluginRuntimeStatusStore); ok {
			rows, err := runtimeStore.ListPluginAgentRuntimeStatuses(ctx, operation.ID)
			if err != nil {
				return nil, err
			}
			for _, row := range rows {
				runtimeStatuses[operation.ID+"\x00"+row.AgentID+"\x00"+row.InstanceID] = row
			}
		}
	}
	statuses := make([]PluginAgentStatus, 0)
	for _, instance := range instances {
		summary, err := pluginReadStatusObject(instance.StatusSummaryJSON)
		if err != nil {
			return nil, err
		}
		activeTargets, err := pluginTargetIDs(json.RawMessage(instance.TargetJSON), defaultTargetID)
		if err != nil {
			return nil, ErrPluginReadProjection
		}
		pendingScope := instance.PendingOperationID != "" && (strings.TrimSpace(instance.PendingTargetJSON) != "" || strings.TrimSpace(instance.PendingResourceGroupID) != "")
		activeOperationID := ""
		if instance.PendingOperationID != "" && !pendingScope {
			activeOperationID = instance.PendingOperationID
		} else if instance.PendingOperationID == "" && isPluginLifecyclePendingKind(installed.PendingKind) {
			activeOperationID = installed.PendingOperationID
		} else if instance.PendingOperationID == "" {
			activeOperationID = installed.LastOperationID
		}
		statuses = appendPluginAgentStatuses(statuses, byID, operationsByID, runtimeStatuses, installed, instance, activeTargets, "active", activeOperationID, summary)
		if pendingScope {
			pendingTargets, err := pluginTargetIDs(json.RawMessage(instance.PendingTargetJSON), defaultTargetID)
			if err != nil {
				return nil, ErrPluginReadProjection
			}
			statuses = appendPluginAgentStatuses(statuses, byID, operationsByID, runtimeStatuses, installed, instance, pendingTargets, "pending", instance.PendingOperationID, summary)
		}
	}
	sort.Slice(statuses, func(i, j int) bool {
		if statuses[i].InstanceID == statuses[j].InstanceID {
			if statuses[i].TargetScope == statuses[j].TargetScope {
				return statuses[i].AgentID < statuses[j].AgentID
			}
			return statuses[i].TargetScope < statuses[j].TargetScope
		}
		return statuses[i].InstanceID < statuses[j].InstanceID
	})
	return statuses, nil
}

func isPluginLifecyclePendingKind(kind string) bool {
	return kind == "enable" || kind == "disable"
}

func appendPluginAgentStatuses(result []PluginAgentStatus, agents map[string]storage.AgentRow, operations map[string]storage.PluginOperationRow, runtimeStatuses map[string]storage.PluginAgentRuntimeStatusRow, installed storage.InstalledPluginRow, instance storage.PluginInstanceRow, targets []string, scope, operationID string, summary json.RawMessage) []PluginAgentStatus {
	operation := operations[operationID]
	targetRevision := operation.TargetRevision
	if targetRevision == 0 && operationID != "" && operationID == installed.PendingOperationID {
		targetRevision = installed.PendingRevision
	}
	for _, target := range targets {
		target = strings.TrimSpace(target)
		agent, available := agents[target]
		status := PluginAgentStatus{InstanceID: instance.ID, AgentID: target, TargetScope: scope, Available: available, CurrentState: instance.CurrentState, StatusSummary: summary, OperationID: operationID, OperationKind: operation.Kind, OperationStatus: operation.Status, TargetRevision: targetRevision, DesiredRevision: agent.DesiredRevision, CurrentRevision: agent.CurrentRevision, LastApplyRevision: agent.LastApplyRevision, LastApplyStatus: agent.LastApplyStatus, LastApplyMessage: agent.LastApplyMessage}
		if runtimeStatus, found := runtimeStatuses[operationID+"\x00"+target+"\x00"+instance.ID]; found {
			status.GenerationID, status.PackageDigest, status.ArtifactDigest = runtimeStatus.GenerationID, runtimeStatus.PackageDigest, runtimeStatus.ArtifactDigest
			status.RuntimeState, status.RuntimeErrorCode, status.ReportedAt = runtimeStatus.State, runtimeStatus.ErrorCode, runtimeStatus.ReportedAt
			status.RuntimeDetails, status.RuntimeBudget = json.RawMessage(runtimeStatus.DetailsJSON), json.RawMessage(runtimeStatus.BudgetJSON)
		}
		result = append(result, status)
	}
	return result
}

func permissionsAdded(grants []storage.PluginGrantRow, activeDigest string, permissions []plugins.Permission) bool {
	existing := map[string]struct{}{}
	for _, grant := range grants {
		if !strings.EqualFold(grant.PackageDigest, activeDigest) {
			continue
		}
		value := grant.Permission
		if grant.ResourceSelector != "" {
			value += ":" + grant.ResourceSelector
		}
		existing[value] = struct{}{}
	}
	for _, permission := range normalizedPermissions(permissions) {
		if _, ok := existing[permission]; !ok {
			return true
		}
	}
	return false
}

func grantRows(pluginID, digest, identity string, permissions []plugins.Permission, actorID string, now time.Time) []storage.PluginGrantRow {
	rows := make([]storage.PluginGrantRow, 0, len(permissions))
	for _, permission := range permissions {
		rows = append(rows, storage.PluginGrantRow{ID: lifecycleID("grant"), PluginID: pluginID, PackageDigest: digest, PackageIdentity: identity, Permission: strings.TrimSpace(permission.Name), ResourceSelector: strings.TrimSpace(permission.Resource), GrantedBy: actorID, GrantedAt: now})
	}
	return rows
}

func pluginLifecycleAudit(operation storage.PluginOperationRow, _ string, result, errorClass string, now time.Time) storage.AuditEventRow {
	metadata, _ := json.Marshal(map[string]any{
		"operation_id": operation.ID, "operation_kind": operation.Kind, "package_digest": operation.TargetPackageDigest,
		"source_id": operation.SourceID, "source_kind": operation.SourceKind, "source_risk_label": operation.SourceRiskLabel,
		"source_revision": operation.SourceRevision, "ref_kind": operation.SourceRefKind, "ref_name": operation.SourceRefName,
		"resolved_oid": operation.SourceResolvedOID, "signer_key_id": operation.TargetSignatureKeyID,
		"signer_fingerprint": operation.TargetSignatureFingerprint,
	})
	return storage.AuditEventRow{ID: lifecycleID("audit"), ActorID: operation.ActorID, SessionID: operation.SessionID, Action: "plugin." + operation.Kind, TargetKind: "plugin", TargetID: operation.PluginID, CorrelationID: operation.CorrelationID, Result: result, ErrorClass: errorClass, MetadataJSON: string(metadata), CreatedAt: now}
}

func pluginErrorClass(err error) string {
	switch {
	case errors.Is(err, ErrPluginPermissionConfirmation):
		return "permission_confirmation"
	case errors.Is(err, ErrPluginRiskConfirmation):
		return "source_risk_confirmation"
	case errors.Is(err, ErrPluginUninstallBlocked):
		return "runtime_not_drained"
	case errors.Is(err, ErrPluginNotInstalled):
		return "not_installed"
	default:
		return "validation"
	}
}

func lifecycleID(prefix string) string {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		panic(err)
	}
	return prefix + "_" + hex.EncodeToString(value)
}

func authorizePluginConfigureScope(request PluginConfigureRequest, exists bool, instance storage.PluginInstanceRow) error {
	actor := request.Actor
	if actor.ID == "" {
		return nil
	}
	if actor.Bootstrap || actor.Has(authz.PermissionSystemAdmin) || actor.Has(authz.PermissionAll) {
		return nil
	}
	if !actor.Has(authz.PermissionResourceWrite) {
		return authz.ErrForbidden
	}
	if !exists {
		return authz.ErrForbidden
	}
	requestedGroup := strings.TrimSpace(request.ResourceGroupID)
	currentGroup := strings.TrimSpace(instance.ResourceGroupID)
	if requestedGroup == "" || requestedGroup != currentGroup {
		return authz.ErrForbidden
	}
	if !actor.CanAccessGroup(currentGroup) {
		return authz.ErrForbidden
	}
	return nil
}

func authorizePluginInstanceWrite(actor authz.Actor, instance storage.PluginInstanceRow) error {
	if actor.ID == "" || actor.Bootstrap || actor.Has(authz.PermissionSystemAdmin) || actor.Has(authz.PermissionAll) {
		return nil
	}
	if !actor.Has(authz.PermissionResourceWrite) || !actor.CanAccessGroup(strings.TrimSpace(instance.ResourceGroupID)) {
		return authz.ErrForbidden
	}
	return nil
}
