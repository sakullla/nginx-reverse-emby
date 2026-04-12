package storage

import (
	"strconv"
	"strings"
)

type LocalApplyOutcome struct {
	Revision int64
	Status   string
	Message  string
}

func NormalizeLocalApplyOutcome(state RuntimeState) LocalApplyOutcome {
	outcome := LocalApplyOutcome{
		Revision: normalizeLocalApplyRevision(state),
	}
	if outcome.Revision <= 0 {
		outcome.Revision = state.CurrentRevision
	}

	status, statusFromMetadata := normalizeLocalApplyStatus(state)
	outcome.Status = status
	outcome.Message = normalizeLocalApplyMessage(state, outcome.Status, statusFromMetadata)
	return outcome
}

func normalizeLocalApplyStatus(state RuntimeState) (string, bool) {
	if state.Metadata != nil {
		if lastSyncError := strings.TrimSpace(state.Metadata["last_sync_error"]); lastSyncError != "" {
			return "error", true
		}
		if status := normalizeLocalApplyStatusValue(state.Metadata["last_apply_status"]); status != "" {
			return status, true
		}
	}
	if status := normalizeLocalApplyStatusValue(state.LastApplyStatus); status != "" {
		return status, false
	}
	switch normalizeLocalApplyStatusValue(state.Status) {
	case "success", "error":
		return normalizeLocalApplyStatusValue(state.Status), false
	default:
		return "", false
	}
}

func normalizeLocalApplyMessage(state RuntimeState, status string, statusFromMetadata bool) string {
	if status != "error" {
		return ""
	}

	if state.Metadata != nil {
		if lastSyncError := strings.TrimSpace(state.Metadata["last_sync_error"]); lastSyncError != "" {
			return lastSyncError
		}
		if metadataMessage := strings.TrimSpace(state.Metadata["last_apply_message"]); metadataMessage != "" {
			return metadataMessage
		}
	}

	if statusFromMetadata {
		return ""
	}
	return strings.TrimSpace(state.LastApplyMessage)
}

func normalizeLocalApplyRevision(state RuntimeState) int64 {
	if revision := parsePositiveInt64FromMetadata(state.Metadata, "last_apply_revision"); revision > 0 {
		return revision
	}
	if state.LastApplyRevision > 0 {
		return state.LastApplyRevision
	}
	return 0
}

func normalizeLocalApplyStatusValue(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "active", "success":
		return "success"
	case "error":
		return "error"
	default:
		return ""
	}
}

func parsePositiveInt64FromMetadata(metadata map[string]string, key string) int64 {
	if metadata == nil {
		return 0
	}
	raw := strings.TrimSpace(metadata[key])
	if raw == "" {
		return 0
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return 0
	}
	return value
}
