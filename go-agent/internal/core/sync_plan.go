package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/control"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	pluginprocess "github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/process"
)

func (c *SyncController) BuildSyncRequest(ctx context.Context, applied model.Snapshot) (control.SyncRequest, error) {
	plan, err := c.BuildSyncPlan(ctx, applied)
	if err != nil {
		return control.SyncRequest{}, err
	}
	return plan.Request, nil
}

func (c *SyncController) BuildSyncPlan(ctx context.Context, applied model.Snapshot) (SyncPlan, error) {
	plan := SyncPlan{Request: control.SyncRequest{CurrentRevision: boundedInt64Revision(applied.Revision)}}

	state, err := c.Store.LoadRuntimeState()
	if err != nil {
		return SyncPlan{}, err
	}
	meta := ensureMetadata(state.Metadata)
	state.Metadata = meta
	logsChanged := false
	if c.Runtime != nil {
		statuses := reconcilePluginRuntimeStatuses(state.PluginStatuses, c.Runtime.State().PluginStatuses)
		if !reflect.DeepEqual(statuses, state.PluginStatuses) {
			state.PluginStatuses = statuses
			logsChanged = true
		}
		plan.Request.PluginStatuses = clonePluginRuntimeStatuses(statuses)
	}
	if logsChanged {
		if err := c.Store.SaveRuntimeState(state); err != nil {
			return SyncPlan{}, err
		}
	}
	outbox, ok := c.Store.(PluginLogOutboxStore)
	logEvents := pluginprocess.SnapshotRuntimeLogEvents()
	if len(logEvents) > 0 && !ok {
		return SyncPlan{}, errors.New("plugin runtime log outbox store is unavailable")
	}
	if ok {
		pending, err := outbox.PendingPluginLogReports()
		if err != nil {
			return SyncPlan{}, err
		}
		drafts, consumed := pluginRuntimeLogDrafts(logEvents, model.MaxPendingPluginLogReports-len(pending))
		if len(drafts) > 0 {
			batchID, err := pluginRuntimeLogCaptureBatchID(logEvents[:consumed])
			if err != nil {
				return SyncPlan{}, err
			}
			if _, err := outbox.EnqueuePluginLogReports(batchID, drafts); err != nil {
				return SyncPlan{}, err
			}
			captureIDs := make([]string, consumed)
			for index := 0; index < consumed; index++ {
				captureIDs[index] = logEvents[index].CaptureID
			}
			pluginprocess.CommitRuntimeLogEvents(captureIDs)
			pending, err = outbox.PendingPluginLogReports()
			if err != nil {
				return SyncPlan{}, err
			}
		}
		plan.Request.PluginLogs = model.ClonePluginRuntimeLogReports(pending)
	}
	if len(plan.Request.PluginLogs) > 0 {
		sent := model.ClonePluginRuntimeLogReports(plan.Request.PluginLogs)
		plan.Request.PluginLogsAcknowledged = func() error { return outbox.AcknowledgePluginLogReports(sent) }
	}
	plan.Request.LastApplyRevision = boundedInt64Revision(parseInt64Default(meta["last_apply_revision"], applied.Revision))
	plan.Request.LastApplyStatus = strings.TrimSpace(meta["last_apply_status"])
	plan.Request.LastApplyMessage = meta["last_apply_message"]
	if plan.Request.LastApplyStatus == "" {
		plan.Request.LastApplyStatus = "success"
	} else if hasLegacyHeartbeatApplyError(meta) {
		plan.Request.LastApplyStatus = "success"
		plan.Request.LastApplyMessage = ""
	}

	if c.Traffic != nil {
		report, err := c.Traffic.TrafficReport(ctx, meta)
		if err != nil {
			return SyncPlan{}, err
		}
		if report.StatsPresent || report.Stats != nil {
			plan.Request.Stats = report.Stats
			plan.Request.StatsPresent = report.StatsPresent
		}
		if len(report.RuntimeMetadata) > 0 {
			plan.RuntimeMetadata = cloneStringMap(report.RuntimeMetadata)
		}
	}

	if c.HostMetrics != nil {
		report, err := c.HostMetrics.HostMetricsReport(ctx)
		if err != nil {
			return SyncPlan{}, err
		}
		if report.StatsPresent || report.Stats != nil {
			plan.Request.Stats = mergeStats(plan.Request.Stats, report.Stats)
			plan.Request.StatsPresent = plan.Request.StatsPresent || report.StatsPresent
		}
	}

	if c.CertReports != nil {
		reports, err := c.CertReports.ManagedCertificateReports(ctx)
		if err != nil {
			return SyncPlan{}, err
		}
		plan.Request.ManagedCertificateReports = reports
	}

	if c.DDNSReporter != nil {
		plan.Request.LastSeenIPv4, plan.Request.LastSeenIPv6 = c.DDNSReporter.LastSeenIPs(ctx)
	}

	return plan, nil
}

func pluginRuntimeLogCaptureBatchID(events []pluginprocess.RuntimeLogEvent) (string, error) {
	identities := make([]string, len(events))
	for index, event := range events {
		if !validPluginLogOutboxID(event.CaptureID) {
			return "", errors.New("plugin runtime log capture identity is invalid")
		}
		identities[index] = event.CaptureID
	}
	encoded, err := json.Marshal(identities)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func pluginRuntimeLogDrafts(events []pluginprocess.RuntimeLogEvent, capacity int) ([]model.PluginRuntimeLogReport, int) {
	if len(events) == 0 || capacity <= 0 {
		return nil, 0
	}
	reports := make([]model.PluginRuntimeLogReport, 0, min(capacity, len(events)))
	consumed := 0
	for consumed < len(events) && len(reports) < capacity {
		identity := events[consumed].Identity
		entries := make([]model.PluginRuntimeLogEntry, 0, model.MaxPluginRuntimeLogEntries)
		for consumed < len(events) && len(entries) < model.MaxPluginRuntimeLogEntries && events[consumed].Identity == identity {
			entry := events[consumed].Entry
			entries = append(entries, model.PluginRuntimeLogEntry{Level: entry.Level, Message: entry.Message, Truncated: entry.Truncated})
			consumed++
		}
		report := model.PluginRuntimeLogReport{
			Revision: identity.Revision, GenerationID: identity.ProviderGenerationID, InstanceID: identity.InstanceID,
			PluginID: identity.PluginID, AgentID: identity.AgentID, PackageDigest: identity.PackageDigest,
			ArtifactDigest: identity.ArtifactDigest, Entries: entries,
		}
		reports = append(reports, report)
	}
	return reports, consumed
}

func reconcilePluginRuntimeStatuses(previous, current []model.PluginRuntimeStatus) []model.PluginRuntimeStatus {
	if current == nil {
		return nil
	}
	prior := make(map[string]model.PluginRuntimeStatus, len(previous))
	for _, status := range previous {
		prior[pluginRuntimeStatusIdentity(status)] = status
	}
	result := clonePluginRuntimeStatuses(current)
	for index := range result {
		result[index].Sequence = 1
		if old, ok := prior[pluginRuntimeStatusIdentity(result[index])]; ok {
			oldObservation, nextObservation := old, result[index]
			oldObservation.Sequence, nextObservation.Sequence = 0, 0
			if reflect.DeepEqual(oldObservation, nextObservation) {
				result[index].Sequence = max(old.Sequence, 1)
			} else {
				result[index].Sequence = max(old.Sequence, 1) + 1
			}
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return pluginRuntimeStatusIdentity(result[i]) < pluginRuntimeStatusIdentity(result[j])
	})
	return result
}

func pluginRuntimeStatusIdentity(status model.PluginRuntimeStatus) string {
	return strings.Join([]string{status.OperationID, status.InstanceID, status.PluginID, status.GenerationID,
		status.PackageDigest, status.ArtifactDigest, status.RuntimeKind, strconv.FormatInt(status.Revision, 10),
		strconv.FormatUint(status.ConfigVersion, 10)}, "\x00")
}

func clonePluginRuntimeStatuses(statuses []model.PluginRuntimeStatus) []model.PluginRuntimeStatus {
	if statuses == nil {
		return nil
	}
	cloned := append([]model.PluginRuntimeStatus(nil), statuses...)
	for index := range cloned {
		cloned[index].Details = append([]byte(nil), statuses[index].Details...)
		cloned[index].Budget = append([]byte(nil), statuses[index].Budget...)
	}
	return cloned
}

func mergeStats(base, extra map[string]any) map[string]any {
	if extra == nil {
		return base
	}
	if base == nil {
		base = map[string]any{}
	}
	for key, value := range extra {
		base[key] = value
	}
	return base
}

func boundedInt64Revision(value int64) int {
	if value <= 0 {
		return 0
	}
	if value > int64(math.MaxInt) {
		return math.MaxInt
	}
	return int(value)
}
