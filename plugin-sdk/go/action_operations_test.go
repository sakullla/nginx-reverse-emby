package pluginsdk

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type actionOperationMemoryStore struct {
	mu           sync.Mutex
	records      map[string]ActionOperationRecord
	failComplete int
}

func (store *actionOperationMemoryStore) ClaimActionOperation(_ context.Context, operationID, fingerprint, claimToken string, now, leaseExpiresAt time.Time) (ActionOperationRecord, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.records == nil {
		store.records = make(map[string]ActionOperationRecord)
	}
	if record, ok := store.records[operationID]; ok {
		if record.Fingerprint == fingerprint && record.State == ActionOperationPending && !record.LeaseExpiresAt.After(now) {
			record.ClaimToken, record.LeaseExpiresAt = claimToken, leaseExpiresAt
			store.records[operationID] = record
			return record, true, nil
		}
		return record, false, nil
	}
	record := ActionOperationRecord{OperationID: operationID, Fingerprint: fingerprint, State: ActionOperationPending, ClaimToken: claimToken, LeaseExpiresAt: leaseExpiresAt}
	store.records[operationID] = record
	return record, true, nil
}

func (store *actionOperationMemoryStore) CompleteActionOperation(_ context.Context, record ActionOperationRecord, claimToken string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	current, ok := store.records[record.OperationID]
	if !ok || current.State != ActionOperationPending || current.ClaimToken != claimToken {
		return errors.New("action claim owner changed")
	}
	if store.failComplete > 0 {
		store.failComplete--
		return errors.New("injected durable completion failure")
	}
	store.records[record.OperationID] = record
	return nil
}

func (store *actionOperationMemoryStore) GetActionOperation(_ context.Context, operationID string) (ActionOperationRecord, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	record, ok := store.records[operationID]
	return record, ok, nil
}

type actionHandler struct {
	execute   func(context.Context, RPCActionRequest) error
	reconcile func(context.Context, RPCActionRequest) (RPCActionResponse, bool, error)
}

func (handler actionHandler) ExecuteAction(ctx context.Context, request RPCActionRequest) error {
	return handler.execute(ctx, request)
}
func (handler actionHandler) ReconcileAction(ctx context.Context, request RPCActionRequest) (RPCActionResponse, bool, error) {
	if handler.reconcile == nil {
		return RPCActionResponse{}, false, nil
	}
	return handler.reconcile(ctx, request)
}

func TestDurableActionExecutorReplaysCommittedSuccessAndTypedFailure(t *testing.T) {
	for _, test := range []struct {
		name       string
		handlerErr error
	}{
		{name: "success"},
		{name: "typed failure", handlerErr: &RuntimeError{Code: ErrorUnavailable, Message: "try later", Retryable: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &actionOperationMemoryStore{}
			executor := DurableActionExecutor{Store: store}
			request := RPCActionRequest{Generation: "generation-1", ActionID: "rotate", TargetKind: "secret", TargetID: "secret-1", OperationID: "operation-1"}
			calls := 0
			handler := actionHandler{execute: func(context.Context, RPCActionRequest) error { calls++; return test.handlerErr }}
			first, err := executor.Invoke(t.Context(), request, handler)
			if err != nil || first.OperationID != request.OperationID {
				t.Fatalf("first response=%+v error=%v", first, err)
			}
			second, err := executor.Invoke(t.Context(), request, actionHandler{execute: func(context.Context, RPCActionRequest) error { calls++; return errors.New("must not run") }})
			if err != nil || calls != 1 || second.OperationID != first.OperationID || second.Accepted != first.Accepted || !sameRuntimeError(second.Error, first.Error) {
				t.Fatalf("replay first=%+v second=%+v calls=%d error=%v", first, second, calls, err)
			}
		})
	}
}

func TestDurableActionExecutorTakesOverExpiredClaimAndReconcilesCommittedSideEffect(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	store := &actionOperationMemoryStore{failComplete: 1}
	executor := DurableActionExecutor{Store: store, Now: func() time.Time { return now }, Lease: time.Minute}
	request := RPCActionRequest{Generation: "generation-1", ActionID: "rotate", TargetKind: "secret", TargetID: "secret-1", OperationID: "operation-1"}
	sideEffect := false
	executeCalls := 0
	handler := actionHandler{
		execute: func(context.Context, RPCActionRequest) error { executeCalls++; sideEffect = true; return nil },
		reconcile: func(_ context.Context, request RPCActionRequest) (RPCActionResponse, bool, error) {
			if sideEffect {
				return RPCActionResponse{OperationID: request.OperationID, Accepted: true}, true, nil
			}
			return RPCActionResponse{}, false, nil
		},
	}
	if _, err := executor.Invoke(t.Context(), request, handler); err == nil || executeCalls != 1 {
		t.Fatalf("first invoke error=%v calls=%d", err, executeCalls)
	}
	if response, err := executor.Invoke(t.Context(), request, handler); err != nil || !response.Pending || executeCalls != 1 {
		t.Fatalf("live lease response=%+v error=%v calls=%d", response, err, executeCalls)
	}
	now = now.Add(2 * time.Minute)
	response, err := executor.Invoke(t.Context(), request, handler)
	if err != nil || !response.Accepted || executeCalls != 1 {
		t.Fatalf("takeover response=%+v error=%v calls=%d", response, err, executeCalls)
	}
}

func TestDurableActionExecutorRestartAfterClaimExecutesAndOwnerFenceRejectsOldCommit(t *testing.T) {
	now := time.Unix(2000, 0).UTC()
	store := &actionOperationMemoryStore{}
	request := RPCActionRequest{Generation: "generation-1", ActionID: "rotate", TargetKind: "secret", TargetID: "secret-1", OperationID: "operation-1"}
	fingerprint, err := ActionRequestFingerprint(request)
	if err != nil {
		t.Fatal(err)
	}
	old, _, err := store.ClaimActionOperation(t.Context(), request.OperationID, fingerprint, "claim-old", now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	executor := DurableActionExecutor{Store: store, Now: func() time.Time { return now }}
	calls := 0
	response, err := executor.Invoke(t.Context(), request, actionHandler{execute: func(context.Context, RPCActionRequest) error { calls++; return nil }})
	if err != nil || !response.Accepted || calls != 1 {
		t.Fatalf("restart response=%+v error=%v calls=%d", response, err, calls)
	}
	old.State, old.ClaimToken, old.LeaseExpiresAt = ActionOperationSucceeded, "", time.Time{}
	if err := store.CompleteActionOperation(t.Context(), old, "claim-old"); err == nil {
		t.Fatal("expected stale claim owner to be rejected")
	}
}

func TestDurableActionExecutorReportsMissingAndPending(t *testing.T) {
	store := &actionOperationMemoryStore{}
	executor := DurableActionExecutor{Store: store}
	query := RPCActionQueryRequest{Generation: "generation-1", OperationID: "operation-1"}
	missing, err := executor.Query(t.Context(), query)
	if err != nil || !missing.Missing {
		t.Fatalf("missing response=%+v error=%v", missing, err)
	}
	request := RPCActionRequest{Generation: query.Generation, ActionID: "rotate", TargetKind: "secret", TargetID: "secret-1", OperationID: query.OperationID}
	fingerprint, _ := ActionRequestFingerprint(request)
	now := time.Now().UTC()
	_, _, _ = store.ClaimActionOperation(t.Context(), query.OperationID, fingerprint, "claim-pending", now, now.Add(time.Minute))
	pending, err := executor.Query(t.Context(), query)
	if err != nil || !pending.Pending {
		t.Fatalf("pending response=%+v error=%v", pending, err)
	}
}

func TestActionRequestFingerprintAllowsOpaqueHandleRotationButBindsRawTarget(t *testing.T) {
	base := RPCActionRequest{Generation: "generation-1", ActionID: "rotate", OperationID: "operation-1", ResourceHandle: "handle-1"}
	first, err := ActionRequestFingerprint(base)
	if err != nil {
		t.Fatal(err)
	}
	base.ResourceHandle = "handle-2"
	second, err := ActionRequestFingerprint(base)
	if err != nil || second != first {
		t.Fatalf("rotated handle fingerprint first=%s second=%s error=%v", first, second, err)
	}
	raw := RPCActionRequest{Generation: base.Generation, ActionID: base.ActionID, OperationID: base.OperationID, TargetKind: "secret", TargetID: "secret-1"}
	rawFirst, _ := ActionRequestFingerprint(raw)
	raw.TargetID = "secret-2"
	rawSecond, _ := ActionRequestFingerprint(raw)
	if rawFirst == rawSecond {
		t.Fatal("raw target identity was not bound into the fingerprint")
	}
}

func sameRuntimeError(left, right *RuntimeError) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.Code == right.Code && left.Message == right.Message && left.Retryable == right.Retryable
}
