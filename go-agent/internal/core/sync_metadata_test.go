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
