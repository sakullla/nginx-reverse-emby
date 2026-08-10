package pluginsdk

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go/protoschema"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

type ActionOperationState string

const (
	ActionOperationPending   ActionOperationState = "pending"
	ActionOperationSucceeded ActionOperationState = "succeeded"
	ActionOperationFailed    ActionOperationState = "failed"
)

type ActionOperationRecord struct {
	OperationID    string
	Fingerprint    string
	State          ActionOperationState
	ClaimToken     string
	LeaseExpiresAt time.Time
	Error          *RuntimeError
}

func (record ActionOperationRecord) Validate() error {
	if err := ValidatePolicyIdentity(record.OperationID); err != nil {
		return fmt.Errorf("action operation identity: %w", err)
	}
	if len(record.Fingerprint) != 64 {
		return errors.New("action operation fingerprint must be sha256")
	}
	if _, err := hex.DecodeString(record.Fingerprint); err != nil || record.Fingerprint != strings.ToLower(record.Fingerprint) {
		return errors.New("action operation fingerprint must be lowercase sha256")
	}
	switch record.State {
	case ActionOperationPending:
		if err := ValidatePolicyIdentity(record.ClaimToken); err != nil || record.LeaseExpiresAt.IsZero() {
			return errors.New("pending action operation requires a claim token and lease")
		}
		if record.Error != nil {
			return errors.New("pending action operation cannot contain an error")
		}
	case ActionOperationSucceeded:
		if record.Error != nil || record.ClaimToken != "" || !record.LeaseExpiresAt.IsZero() {
			return errors.New("succeeded action operation must be terminal")
		}
	case ActionOperationFailed:
		if record.Error == nil || record.ClaimToken != "" || !record.LeaseExpiresAt.IsZero() {
			return errors.New("failed action operation must contain only a terminal error")
		}
		if err := record.Error.Validate(); err != nil {
			return fmt.Errorf("failed action operation: %w", err)
		}
	default:
		return errors.New("action operation state is invalid")
	}
	return nil
}

// ActionOperationStore is implemented by the guest with crash-durable storage.
// Claim must atomically create a pending record, take over an expired matching
// pending record, or return an existing live/terminal record. Complete must
// compare the current pending claim token before committing the terminal row.
type ActionOperationStore interface {
	ClaimActionOperation(context.Context, string, string, string, time.Time, time.Time) (ActionOperationRecord, bool, error)
	CompleteActionOperation(context.Context, ActionOperationRecord, string) error
	GetActionOperation(context.Context, string) (ActionOperationRecord, bool, error)
}

// DurableActionHandler reconciles core-owned side effects before a new or
// expired claim executes. Reconcile returns handled=true only for an exact
// terminal response, allowing recovery after a crash between the side effect
// and the durable operation commit without executing it twice.
type DurableActionHandler interface {
	ReconcileAction(context.Context, RPCActionRequest) (RPCActionResponse, bool, error)
	ExecuteAction(context.Context, RPCActionRequest) error
}

type DurableActionExecutor struct {
	Store         ActionOperationStore
	Now           func() time.Time
	Lease         time.Duration
	CommitTimeout time.Duration
}

func (executor DurableActionExecutor) Invoke(ctx context.Context, request RPCActionRequest, handler DurableActionHandler) (RPCActionResponse, error) {
	if executor.Store == nil || handler == nil {
		return RPCActionResponse{}, errors.New("durable action store and handler are required")
	}
	if err := request.Validate(); err != nil {
		return RPCActionResponse{}, err
	}
	fingerprint, err := ActionRequestFingerprint(request)
	if err != nil {
		return RPCActionResponse{}, err
	}
	now := time.Now().UTC()
	if executor.Now != nil {
		now = executor.Now().UTC()
	}
	lease := executor.Lease
	if lease <= 0 {
		lease = 2 * time.Minute
	}
	claimToken, err := newActionClaimToken()
	if err != nil {
		return RPCActionResponse{}, err
	}
	record, claimed, err := executor.Store.ClaimActionOperation(ctx, request.OperationID, fingerprint, claimToken, now, now.Add(lease))
	if err != nil {
		return RPCActionResponse{}, err
	}
	if err := record.Validate(); err != nil {
		return RPCActionResponse{}, fmt.Errorf("durable action claim: %w", err)
	}
	if record.Fingerprint != fingerprint {
		return RPCActionResponse{}, errors.New("action operation identity was reused with a different request")
	}
	if !claimed {
		return actionOperationResponse(record), nil
	}
	if record.ClaimToken != claimToken {
		return RPCActionResponse{}, errors.New("durable action claim owner differs from the requested token")
	}

	reconciled, handled, reconcileErr := handler.ReconcileAction(ctx, request)
	if reconcileErr != nil {
		return RPCActionResponse{}, fmt.Errorf("reconcile durable action: %w", reconcileErr)
	}
	if handled {
		terminal, err := actionTerminalRecord(record, reconciled)
		if err != nil {
			return RPCActionResponse{}, err
		}
		return executor.commit(ctx, terminal, claimToken)
	}

	actionErr := handler.ExecuteAction(ctx, request)
	if errors.Is(actionErr, context.Canceled) || errors.Is(actionErr, context.DeadlineExceeded) {
		return actionOperationResponse(record), actionErr
	}
	record.State = ActionOperationSucceeded
	record.ClaimToken = ""
	record.LeaseExpiresAt = time.Time{}
	record.Error = nil
	if actionErr != nil {
		record.State = ActionOperationFailed
		var runtimeErr *RuntimeError
		if errors.As(actionErr, &runtimeErr) && runtimeErr.Validate() == nil {
			copy := *runtimeErr
			record.Error = &copy
		} else {
			record.Error = &RuntimeError{Code: ErrorInternal, Message: "action failed", Retryable: false}
		}
	}
	return executor.commit(ctx, record, claimToken)
}

func (executor DurableActionExecutor) commit(ctx context.Context, record ActionOperationRecord, claimToken string) (RPCActionResponse, error) {
	timeout := executor.CommitTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	commitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	if err := executor.Store.CompleteActionOperation(commitCtx, record, claimToken); err != nil {
		return RPCActionResponse{}, fmt.Errorf("durably complete action operation: %w", err)
	}
	return actionOperationResponse(record), nil
}

func actionTerminalRecord(record ActionOperationRecord, response RPCActionResponse) (ActionOperationRecord, error) {
	if err := response.Validate(); err != nil || response.OperationID != record.OperationID || response.Pending || response.Missing {
		return ActionOperationRecord{}, errors.Join(errors.New("reconciled action result is not an exact terminal outcome"), err)
	}
	record.ClaimToken = ""
	record.LeaseExpiresAt = time.Time{}
	record.Error = nil
	if response.Accepted {
		record.State = ActionOperationSucceeded
	} else {
		record.State = ActionOperationFailed
		copy := *response.Error
		record.Error = &copy
	}
	return record, record.Validate()
}

func (executor DurableActionExecutor) Query(ctx context.Context, request RPCActionQueryRequest) (RPCActionResponse, error) {
	if executor.Store == nil {
		return RPCActionResponse{}, errors.New("durable action store is required")
	}
	if err := request.Validate(); err != nil {
		return RPCActionResponse{}, err
	}
	record, ok, err := executor.Store.GetActionOperation(ctx, request.OperationID)
	if err != nil {
		return RPCActionResponse{}, err
	}
	if !ok {
		return RPCActionResponse{OperationID: request.OperationID, Missing: true}, nil
	}
	if err := record.Validate(); err != nil {
		return RPCActionResponse{}, fmt.Errorf("durable action query: %w", err)
	}
	return actionOperationResponse(record), nil
}

func newActionClaimToken() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return "claim-" + hex.EncodeToString(value), nil
}

func ActionRequestFingerprint(request RPCActionRequest) (string, error) {
	if err := request.Validate(); err != nil {
		return "", err
	}
	descriptor, err := protoschema.Message("nre.plugin.rpc.v1.ActionRequest")
	if err != nil {
		return "", err
	}
	message := dynamicpb.NewMessage(descriptor)
	for name, value := range map[protoreflect.Name]string{
		"generation": request.Generation, "action_id": request.ActionID,
		"target_kind": request.TargetKind, "target_id": request.TargetID,
		"operation_id": request.OperationID,
	} {
		if value != "" {
			message.Set(descriptor.Fields().ByName(name), protoreflect.ValueOfString(value))
		}
	}
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func actionOperationResponse(record ActionOperationRecord) RPCActionResponse {
	response := RPCActionResponse{OperationID: record.OperationID}
	switch record.State {
	case ActionOperationPending:
		response.Pending = true
	case ActionOperationSucceeded:
		response.Accepted = true
	case ActionOperationFailed:
		if record.Error != nil {
			copy := *record.Error
			response.Error = &copy
		}
	}
	return response
}
