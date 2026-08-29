package core

import "testing"

func TestRecoverableSyncApplyErrorIncludesFailedHotRestartCutover(t *testing.T) {
	message := "start hot restart child: wait for hot restart child readiness: EOF"
	metadata := map[string]string{
		"last_apply_status":  "error",
		"last_apply_message": message,
	}
	if !isRecoverableSyncApplyError(metadata, message) {
		t.Fatal("failed hot restart was not cleared after a later successful sync")
	}
	if got := recoverableApplyErrorMessage(metadata); got != message {
		t.Fatalf("recoverableApplyErrorMessage() = %q, want %q", got, message)
	}
	if isRecoverableSyncApplyError(metadata, "unrelated runtime failure") {
		t.Fatal("unrelated runtime failure was treated as a recoverable hot restart")
	}
	metadata["last_apply_message"] = "unrelated runtime failure"
	if got := recoverableApplyErrorMessage(metadata); got != "" {
		t.Fatalf("unrelated recoverableApplyErrorMessage() = %q, want empty", got)
	}
}

func TestRecoverableSyncApplyErrorClearsLegacySchemaIdentityMismatch(t *testing.T) {
	message := "durable generation does not match the desired runtime snapshot"
	metadata := map[string]string{
		"last_apply_status":  "error",
		"last_apply_message": message,
	}
	if !isRecoverableSyncApplyError(metadata, message) {
		t.Fatal("legacy schema-derived generation mismatch was not cleared after a later successful sync")
	}
}

func TestRecoverableSyncApplyErrorClearsRevisionReportAckFailure(t *testing.T) {
	message := `revision request /api/agent-revisions/478/report failed: 500 Internal Server Error: {"message":"internal server error","ok":false}`
	metadata := map[string]string{
		"last_apply_status":  "error",
		"last_apply_message": message,
	}
	if !isRecoverableSyncApplyError(metadata, message) {
		t.Fatal("revision report ACK failure was not cleared after a later successful sync")
	}
	if isRevisionReportAckError("nginx config test failed") {
		t.Fatal("unrelated apply failure was treated as a revision report ACK error")
	}
}

func TestRecoverableSyncApplyErrorClearsBareHeartbeatSocketFailure(t *testing.T) {
	message := "read tcp 172.31.143.160:33608->23.138.76.10:443: read: connection reset by peer"
	metadata := map[string]string{"last_apply_status": "error", "last_apply_message": message}
	if !isRecoverableSyncApplyError(metadata, message) {
		t.Fatal("bare heartbeat socket failure was not cleared after a later successful sync")
	}
	if isLegacyHeartbeatTransportError("read tcp backend:443: application protocol failure") {
		t.Fatal("non-transport application error was treated as a recoverable heartbeat failure")
	}
}
