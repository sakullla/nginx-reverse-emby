package localagent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type Snapshot = storage.Snapshot

type SyncRequest struct {
	CurrentRevision           int
	LastApplyRevision         int
	LastApplyStatus           string
	LastApplyMessage          string
	Stats                     map[string]any
	StatsPresent              bool
	ManagedCertificateReports []storage.ManagedCertificateReport
	LastSeenIPv4              string
	LastSeenIPv6              string
	PluginStatuses            []storage.PluginRuntimeStatus
	PluginLogs                []storage.PluginRuntimeLogReport
}

type SnapshotStore interface {
	LoadLocalSnapshot(context.Context, string) (storage.Snapshot, error)
}

type trafficSummaryService interface {
	IngestHeartbeat(context.Context, string, service.AgentStats) error
	Summary(context.Context, string) (service.TrafficSummary, error)
	BlockState(context.Context, string) (bool, string, error)
}

type SyncSource struct {
	store               SnapshotStore
	agentID             string
	bridge              *syncRequestBridge
	tunnelPKI           TunnelPKIService
	trafficService      trafficSummaryService
	trafficStatsEnabled bool
	ddnsReconcile       func(context.Context, string)
	pluginSecrets       interface {
		RedeemAgentPluginSecrets(context.Context, string, service.PluginSecretRedemptionRequest) (service.PluginSecretRedemptionResponse, error)
	}
}

func (s *SyncSource) SetPluginSecretSource(source interface {
	RedeemAgentPluginSecrets(context.Context, string, service.PluginSecretRedemptionRequest) (service.PluginSecretRedemptionResponse, error)
}) {
	s.pluginSecrets = source
}

type localDDNSHeartbeatStore interface {
	ListAgents(context.Context) ([]storage.AgentRow, error)
	SaveAgentHeartbeat(context.Context, storage.AgentRow) error
}

type localPluginRuntimeReportStore interface {
	RecordPluginAgentRuntimeReport(context.Context, storage.PluginGenerationReport) (storage.PluginAgentRuntimeStatusRow, bool, error)
}

type localPluginRuntimeLogStore interface {
	RecordPluginRuntimeLogReport(context.Context, string, storage.PluginRuntimeLogReport) (bool, error)
}

func NewSyncSource(store SnapshotStore, agentID string) *SyncSource {
	return newSyncSourceWithBridge(store, agentID, nil)
}

func newSyncSourceWithBridge(store SnapshotStore, agentID string, bridge *syncRequestBridge) *SyncSource {
	return &SyncSource{
		store:               store,
		agentID:             agentID,
		bridge:              bridge,
		trafficStatsEnabled: true,
	}
}

func (s *SyncSource) SetTrafficService(enabled bool, trafficService trafficSummaryService) {
	s.trafficStatsEnabled = enabled
	s.trafficService = trafficService
}

func (s *SyncSource) SetDDNSReconciler(reconcile func(context.Context, string)) {
	s.ddnsReconcile = reconcile
}

func (s *SyncSource) SetTunnelPKI(pki TunnelPKIService) {
	s.tunnelPKI = pki
}

func (s *SyncSource) Sync(ctx context.Context, request SyncRequest) (Snapshot, error) {
	// Embedded-agent reconciliation is an explicit system principal. It is not
	// associated with an interactive user, but must still participate in group
	// quota/audit and dependency authorization rather than relying on an absent
	// context value to bypass those controls.
	ctx = service.WithSystemMutationPrincipal(ctx, "system:local-agent-sync")
	if s.bridge != nil {
		s.bridge.Store(request)
	}
	if err := s.persistDDNSAddresses(ctx, request.LastSeenIPv4, request.LastSeenIPv6); err != nil {
		return Snapshot{}, err
	}
	if reportStore, ok := s.store.(localPluginRuntimeReportStore); ok {
		for _, status := range request.PluginStatuses {
			if _, _, err := reportStore.RecordPluginAgentRuntimeReport(ctx, pluginGenerationReportFromRuntimeStatus(s.agentID, status)); err != nil {
				if discardPluginTelemetryError(err) {
					continue
				}
				return Snapshot{}, err
			}
		}
	}
	if logStore, ok := s.store.(localPluginRuntimeLogStore); ok {
		for _, report := range request.PluginLogs {
			if _, err := logStore.RecordPluginRuntimeLogReport(ctx, s.agentID, report); err != nil {
				if discardPluginTelemetryError(err) {
					continue
				}
				return Snapshot{}, err
			}
		}
	} else if len(request.PluginLogs) > 0 {
		return Snapshot{}, errors.New("plugin runtime log ingestion is unavailable")
	}
	snapshot, err := s.store.LoadLocalSnapshot(ctx, s.agentID)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot, err = service.OverlayPendingManagedCertificateGenerationsForConfig(ctx, config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     s.agentID,
	}, s.store, s.agentID, snapshot)
	if err != nil {
		return Snapshot{}, err
	}
	if s.tunnelPKI != nil {
		projected, projectionErr := s.tunnelPKI.PrepareRelayListeners(ctx, s.agentID, snapshot.RelayListeners)
		if projectionErr != nil {
			// PKI control state is independent from ordinary configuration. Strip
			// every relay listener on degradation so a restart cannot resurrect a
			// stale tunnel while HTTP/L4/control synchronization keeps running.
			snapshot.RelayListeners = []storage.RelayListener{}
		} else {
			snapshot.RelayListeners = projected
		}
	}
	snapshot.AgentConfig.TrafficStatsEnabled = boolPtr(s.trafficStatsEnabled)
	if !s.trafficStatsEnabled || s.trafficService == nil {
		snapshot.AgentConfig.TrafficBlocked = false
		snapshot.AgentConfig.TrafficBlockReason = ""
		return snapshot, nil
	}
	if len(request.Stats) > 0 {
		_ = s.trafficService.IngestHeartbeat(ctx, s.agentID, service.AgentStats(request.Stats))
	}
	blocked, reason, err := s.trafficService.BlockState(ctx, s.agentID)
	if err != nil {
		// Preserve the durable last-known state on transient quota/storage
		// failures. Only a successful explicit unblocked result may reopen
		// traffic.
		return snapshot, nil
	}
	snapshot.AgentConfig.TrafficBlocked = blocked
	snapshot.AgentConfig.TrafficBlockReason = reason
	return snapshot, nil
}

func discardPluginTelemetryError(err error) bool {
	return errors.Is(err, storage.ErrPluginGenerationStale) || errors.Is(err, storage.ErrPluginGenerationConflict)
}

func (s *SyncSource) persistDDNSAddresses(ctx context.Context, ipv4, ipv6 string) error {
	ipv4 = strings.TrimSpace(ipv4)
	ipv6 = strings.TrimSpace(ipv6)
	if s == nil || (ipv4 == "" && ipv6 == "") {
		return nil
	}
	store, ok := s.store.(localDDNSHeartbeatStore)
	if !ok {
		return nil
	}
	rows, err := store.ListAgents(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.ID != s.agentID {
			continue
		}
		if ipv4 != "" {
			row.LastSeenIPv4 = ipv4
		}
		if ipv6 != "" {
			row.LastSeenIPv6 = ipv6
		}
		row.LastSeenAt = time.Now().UTC().Format(time.RFC3339)
		if err := store.SaveAgentHeartbeat(ctx, row); err != nil {
			return err
		}
		if s.ddnsReconcile != nil {
			s.ddnsReconcile(ctx, s.agentID)
		}
		return nil
	}
	return nil
}

func boolPtr(value bool) *bool {
	return &value
}

type syncRequestBridge struct {
	mu      sync.RWMutex
	request SyncRequest
}

func newSyncRequestBridge() *syncRequestBridge {
	return &syncRequestBridge{}
}

func (b *syncRequestBridge) Store(request SyncRequest) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.request = cloneSyncRequest(request)
}

func (b *syncRequestBridge) Load() SyncRequest {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return cloneSyncRequest(b.request)
}

func cloneSyncRequest(request SyncRequest) SyncRequest {
	copyValue := request
	copyValue.PluginStatuses = append([]storage.PluginRuntimeStatus(nil), request.PluginStatuses...)
	copyValue.PluginLogs = append([]storage.PluginRuntimeLogReport(nil), request.PluginLogs...)
	if len(request.ManagedCertificateReports) > 0 {
		copyValue.ManagedCertificateReports = append([]storage.ManagedCertificateReport(nil), request.ManagedCertificateReports...)
	}
	if request.Stats != nil {
		data, err := json.Marshal(request.Stats)
		if err == nil {
			var stats map[string]any
			if json.Unmarshal(data, &stats) == nil {
				copyValue.Stats = stats
			}
		}
	}
	return copyValue
}

func pluginGenerationReportFromRuntimeStatus(agentID string, status storage.PluginRuntimeStatus) storage.PluginGenerationReport {
	return storage.PluginGenerationReport{
		OperationID: status.OperationID, AgentID: agentID, InstanceID: status.InstanceID, PluginID: status.PluginID,
		Revision: status.Revision, GenerationID: status.GenerationID, PackageDigest: status.PackageDigest,
		ArtifactDigest: status.ArtifactDigest, State: status.State, Sequence: status.Sequence,
		ErrorCode: status.ErrorCode, SafeDetail: status.SafeDetail, Details: append(json.RawMessage(nil), status.Details...),
		Budget: append(json.RawMessage(nil), status.Budget...), ReportedAt: time.Now().UTC(),
	}
}
