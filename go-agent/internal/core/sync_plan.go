package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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
	logEvents := pluginprocess.DrainRuntimeLogEvents()
	consumedLogEvents, logErr := appendPluginRuntimeLogEvents(&state, logEvents)
	if logErr != nil {
		pluginprocess.RestoreRuntimeLogEvents(logEvents)
		return SyncPlan{}, logErr
	}
	if consumedLogEvents < len(logEvents) {
		pluginprocess.RestoreRuntimeLogEvents(logEvents[consumedLogEvents:])
	}
	logsChanged := consumedLogEvents > 0
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
			pluginprocess.RestoreRuntimeLogEvents(logEvents[:consumedLogEvents])
			return SyncPlan{}, err
		}
	}
	plan.Request.PluginLogs = model.ClonePluginRuntimeLogReports(state.PluginLogReports)
	if len(plan.Request.PluginLogs) > 0 {
		sent := model.ClonePluginRuntimeLogReports(plan.Request.PluginLogs)
		plan.Request.PluginLogsAcknowledged = func() error { return acknowledgePluginRuntimeLogs(c.Store, sent) }
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

func appendPluginRuntimeLogEvents(state *model.RuntimeState, events []pluginprocess.RuntimeLogEvent) (int, error) {
	if state == nil || len(events) == 0 {
		return 0, nil
	}
	state.Metadata = ensureMetadata(state.Metadata)
	consumed := 0
	for consumed < len(events) && len(state.PluginLogReports) < model.MaxPendingPluginLogReports {
		identity := events[consumed].Identity
		entries := make([]model.PluginRuntimeLogEntry, 0, model.MaxPluginRuntimeLogEntries)
		for consumed < len(events) && len(entries) < model.MaxPluginRuntimeLogEntries && events[consumed].Identity == identity {
			entry := events[consumed].Entry
			entries = append(entries, model.PluginRuntimeLogEntry{Level: entry.Level, Message: entry.Message, Truncated: entry.Truncated})
			consumed++
		}
		sequenceKey := pluginRuntimeLogSequenceMetadataKey(identity)
		sequence := parsePluginRuntimeLogSequence(state.Metadata[sequenceKey])
		for _, pending := range state.PluginLogReports {
			if pluginRuntimeLogEventMatchesReport(identity, pending) && pending.Sequence > sequence {
				sequence = pending.Sequence
			}
		}
		sequence++
		report := model.PluginRuntimeLogReport{
			Revision: identity.Revision, GenerationID: identity.ProviderGenerationID, InstanceID: identity.InstanceID,
			PluginID: identity.PluginID, AgentID: identity.AgentID, PackageDigest: identity.PackageDigest,
			ArtifactDigest: identity.ArtifactDigest, Sequence: sequence, Entries: entries,
		}
		if err := report.Validate(); err != nil {
			return consumed - len(entries), fmt.Errorf("capture plugin runtime logs: %w", err)
		}
		state.PluginLogReports = append(state.PluginLogReports, report)
		state.Metadata[sequenceKey] = strconv.FormatUint(sequence, 10)
	}
	return consumed, nil
}

func pluginRuntimeLogEventMatchesReport(identity pluginprocess.RuntimeLogIdentity, report model.PluginRuntimeLogReport) bool {
	return identity.Revision == report.Revision && identity.ProviderGenerationID == report.GenerationID &&
		identity.InstanceID == report.InstanceID && identity.PluginID == report.PluginID && identity.AgentID == report.AgentID &&
		identity.PackageDigest == report.PackageDigest && identity.ArtifactDigest == report.ArtifactDigest
}

func acknowledgePluginRuntimeLogs(store Store, sent []model.PluginRuntimeLogReport) error {
	if store == nil || len(sent) == 0 {
		return nil
	}
	state, err := store.LoadRuntimeState()
	if err != nil {
		return err
	}
	acknowledged := make(map[string]struct{}, len(sent))
	for _, report := range sent {
		acknowledged[pluginRuntimeLogReportIdentity(report)] = struct{}{}
	}
	kept := state.PluginLogReports[:0]
	for _, report := range state.PluginLogReports {
		if _, ok := acknowledged[pluginRuntimeLogReportIdentity(report)]; !ok {
			kept = append(kept, report)
		}
	}
	if len(kept) == len(state.PluginLogReports) {
		return nil
	}
	state.PluginLogReports = model.ClonePluginRuntimeLogReports(kept)
	return store.SaveRuntimeState(state)
}

func pluginRuntimeLogSequenceMetadataKey(identity pluginprocess.RuntimeLogIdentity) string {
	digest := sha256.Sum256([]byte(strings.Join([]string{
		strconv.FormatInt(identity.Revision, 10), identity.ProviderGenerationID, identity.InstanceID,
		identity.PluginID, identity.AgentID, identity.PackageDigest, identity.ArtifactDigest,
	}, "\x00")))
	return "plugin_log_sequence." + hex.EncodeToString(digest[:])
}

func parsePluginRuntimeLogSequence(raw string) uint64 {
	sequence, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0
	}
	return sequence
}

func pluginRuntimeLogReportIdentity(report model.PluginRuntimeLogReport) string {
	return strings.Join([]string{
		strconv.FormatInt(report.Revision, 10), report.GenerationID, report.InstanceID, report.PluginID,
		report.AgentID, report.PackageDigest, report.ArtifactDigest, strconv.FormatUint(report.Sequence, 10),
	}, "\x00")
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
