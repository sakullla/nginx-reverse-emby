package acmeflow

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReconcileIsIdempotentAndPreservesCurrent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, time.July, 25, 6, 0, 0, 0, time.UTC)
	root := t.TempDir()
	store, err := OpenStateStore(root, WithStateClock(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("OpenStateStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	input := testGenerationInput(t, 31, now)
	manifest, err := store.StageGeneration(ctx, input)
	if err != nil {
		t.Fatalf("StageGeneration() error = %v", err)
	}
	if err := store.PromoteGeneration(ctx, manifest.ID, nil); err != nil {
		t.Fatalf("PromoteGeneration() error = %v", err)
	}

	orphan := filepath.Join(root, stagingDirectory, "orphan-stage")
	if err := os.MkdirAll(orphan, 0o700); err != nil {
		t.Fatalf("MkdirAll(orphan) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(orphan, generationCertificateFile), []byte("partial"), 0o600); err != nil {
		t.Fatalf("WriteFile(orphan) error = %v", err)
	}

	pending, err := NewChallengeIntent("example.com", "_acme-challenge.example.com", "pending-secret")
	if err != nil {
		t.Fatalf("NewChallengeIntent(pending) error = %v", err)
	}
	if err := store.SaveChallengeIntent(ctx, pending); err != nil {
		t.Fatalf("SaveChallengeIntent(pending) error = %v", err)
	}
	completed, err := NewChallengeIntent("example.net", "_acme-challenge.example.net", "completed-secret")
	if err != nil {
		t.Fatalf("NewChallengeIntent(completed) error = %v", err)
	}
	if err := store.SaveChallengeIntent(ctx, completed); err != nil {
		t.Fatalf("SaveChallengeIntent(completed) error = %v", err)
	}
	if err := store.CompleteChallengeIntent(ctx, completed.ID); err != nil {
		t.Fatalf("CompleteChallengeIntent() error = %v", err)
	}

	result, err := store.Reconcile(ctx)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if result.Current == nil || result.Current.Manifest.ID != manifest.ID {
		t.Fatalf("Reconcile() current = %#v", result.Current)
	}
	if len(result.PendingChallenges) != 1 || result.PendingChallenges[0].ID != pending.ID {
		t.Fatalf("Reconcile() pending = %#v", result.PendingChallenges)
	}
	if result.PendingChallenges[0].FQDN != pending.FQDN || result.PendingChallenges[0].ValueHash != pending.ValueHash {
		t.Fatalf("Reconcile() recovery item lacks exact name/hash: %#v", result.PendingChallenges[0])
	}
	if len(result.RemovedStages) != 1 || result.RemovedStages[0] != "orphan-stage" {
		t.Fatalf("Reconcile() removed stages = %#v", result.RemovedStages)
	}
	if len(result.RemovedCompletedChallenges) != 1 || result.RemovedCompletedChallenges[0] != completed.ID {
		t.Fatalf("Reconcile() removed completed = %#v", result.RemovedCompletedChallenges)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("orphan stage still exists: %v", err)
	}

	second, err := store.Reconcile(ctx)
	if err != nil {
		t.Fatalf("Reconcile(second) error = %v", err)
	}
	if second.Current == nil || second.Current.Manifest.ID != manifest.ID || len(second.PendingChallenges) != 1 {
		t.Fatalf("Reconcile(second) = %#v", second)
	}
	if len(second.RemovedStages) != 0 || len(second.RemovedCompletedChallenges) != 0 {
		t.Fatalf("Reconcile(second) was not idempotent: %#v", second)
	}
}

func TestReconcileDropsUnreadablePendingAfterCurrentFallback(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, time.July, 28, 8, 0, 0, 0, time.UTC)
	root := t.TempDir()
	store, err := OpenStateStore(root, WithStateClock(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("OpenStateStore() error = %v", err)
	}

	activeInput := testGenerationInput(t, 41, now)
	active, err := store.StageGeneration(ctx, activeInput)
	if err != nil {
		t.Fatalf("StageGeneration(active) error = %v", err)
	}
	if err := store.PromoteGeneration(ctx, active.ID, nil); err != nil {
		t.Fatalf("PromoteGeneration(active) error = %v", err)
	}

	pendingInput := testGenerationInput(t, 42, now)
	pendingInput.Pending = &PendingGenerationInput{
		PreviousGenerationID: active.ID,
		PolicySHA256:         strings.Repeat("a", sha256.Size*2),
		RecordRenewal:        true,
	}
	pending, err := store.StageGeneration(ctx, pendingInput)
	if err != nil {
		t.Fatalf("StageGeneration(pending) error = %v", err)
	}
	if err := store.PromoteGeneration(ctx, pending.ID, nil); err != nil {
		t.Fatalf("PromoteGeneration(pending) error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	certificatePath := filepath.Join(root, generationsDirectory, pending.ID, generationCertificateFile)
	if err := os.WriteFile(certificatePath, []byte("partial"), 0o600); err != nil {
		t.Fatalf("WriteFile(pending certificate) error = %v", err)
	}
	reopened, err := OpenStateStore(root, WithStateClock(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("OpenStateStore(reopen) error = %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })

	result, err := reopened.Reconcile(ctx)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if result.Current == nil || result.Current.Manifest.ID != active.ID {
		t.Fatalf("Reconcile() current = %#v, want fallback %q", result.Current, active.ID)
	}
	if result.RemovedPendingGeneration != pending.ID {
		t.Fatalf("Reconcile() removed pending = %q, want %q", result.RemovedPendingGeneration, pending.ID)
	}
	if _, err := reopened.LoadPendingGeneration(ctx); !errors.Is(err, ErrNoPendingGeneration) {
		t.Fatalf("LoadPendingGeneration() error = %v, want ErrNoPendingGeneration", err)
	}
}
