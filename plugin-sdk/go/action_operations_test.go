package pluginsdk

import (
	"context"
	"errors"
	"sync"
	"testing"
)

type actionOperationMemoryStore struct {
	mu      sync.Mutex
	records map[string]ActionOperationRecord
}

func (store *actionOperationMemoryStore) ClaimActionOperation(_ context.Context, operationID, fingerprint string) (ActionOperationRecord, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.records == nil {
		store.records = make(map[string]ActionOperationRecord)
	}
	if record, ok := store.records[operationID]; ok {
		return record, false, nil
	}
	record := ActionOperationRecord{OperationID: operationID, Fingerprint: fingerprint, State: ActionOperationPending}
	store.records[operationID] = record
	return record, true, nil
}

func (store *actionOperationMemoryStore) CompleteActionOperation(_ context.Context, record ActionOperationRecord) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.records[record.OperationID] = record
	return nil
}

func (store *actionOperationMemoryStore) GetActionOperation(_ context.Context, operationID string) (ActionOperationRecord, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	record, ok := store.records[operationID]
	return record, ok, nil
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
			first, err := executor.Invoke(t.Context(), request, func(context.Context, RPCActionRequest) error {
				calls++
				return test.handlerErr
			})
			if err != nil || first.OperationID != request.OperationID {
				t.Fatalf("first response=%+v error=%v", first, err)
			}
			second, err := executor.Invoke(t.Context(), request, func(context.Context, RPCActionRequest) error {
				calls++
				return errors.New("must not run")
			})
			if err != nil || calls != 1 || second.OperationID != first.OperationID || second.Accepted != first.Accepted || !sameRuntimeError(second.Error, first.Error) {
				t.Fatalf("replay first=%+v second=%+v calls=%d error=%v", first, second, calls, err)
			}
			queried, err := executor.Query(t.Context(), RPCActionQueryRequest{Generation: request.Generation, OperationID: request.OperationID})
			if err != nil || queried.Accepted != first.Accepted || !sameRuntimeError(queried.Error, first.Error) {
				t.Fatalf("query response=%+v error=%v", queried, err)
			}
		})
	}
}

func sameRuntimeError(left, right *RuntimeError) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.Code == right.Code && left.Message == right.Message && left.Retryable == right.Retryable
}

func TestDurableActionExecutorReportsMissingAndPendingWithoutExecuting(t *testing.T) {
	store := &actionOperationMemoryStore{}
	executor := DurableActionExecutor{Store: store}
	query := RPCActionQueryRequest{Generation: "generation-1", OperationID: "operation-1"}
	missing, err := executor.Query(t.Context(), query)
	if err != nil || !missing.Missing {
		t.Fatalf("missing response=%+v error=%v", missing, err)
	}
	fingerprint, err := ActionRequestFingerprint(RPCActionRequest{Generation: query.Generation, ActionID: "rotate", TargetKind: "secret", TargetID: "secret-1", OperationID: query.OperationID})
	if err != nil {
		t.Fatal(err)
	}
	store.records = map[string]ActionOperationRecord{query.OperationID: {OperationID: query.OperationID, Fingerprint: fingerprint, State: ActionOperationPending}}
	pending, err := executor.Query(t.Context(), query)
	if err != nil || !pending.Pending {
		t.Fatalf("pending response=%+v error=%v", pending, err)
	}
}
