package pluginsdk

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

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
	OperationID string
	Fingerprint string
	State       ActionOperationState
	Error       *RuntimeError
}

func (record ActionOperationRecord) Validate() error {
	if err := ValidatePolicyIdentity(record.OperationID); err != nil {
		return fmt.Errorf("action operation identity: %w", err)
	}
	if len(record.Fingerprint) != 64 {
		return errors.New("action operation fingerprint must be sha256")
	}
	if _, err := hex.DecodeString(record.Fingerprint); err != nil {
		return errors.New("action operation fingerprint must be lowercase sha256")
	}
	if record.Fingerprint != strings.ToLower(record.Fingerprint) {
		return errors.New("action operation fingerprint must be lowercase sha256")
	}
	switch record.State {
	case ActionOperationPending, ActionOperationSucceeded:
		if record.Error != nil {
			return errors.New("pending or succeeded action operation cannot contain an error")
		}
	case ActionOperationFailed:
		if record.Error == nil {
			return errors.New("failed action operation must contain an error")
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
// Claim must atomically create a pending record or return the existing record;
// Complete must durably commit the exact terminal record before returning.
type ActionOperationStore interface {
	ClaimActionOperation(context.Context, string, string) (ActionOperationRecord, bool, error)
	CompleteActionOperation(context.Context, ActionOperationRecord) error
	GetActionOperation(context.Context, string) (ActionOperationRecord, bool, error)
}

type DurableActionExecutor struct {
	Store ActionOperationStore
}

func (executor DurableActionExecutor) Invoke(ctx context.Context, request RPCActionRequest, handler func(context.Context, RPCActionRequest) error) (RPCActionResponse, error) {
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
	record, claimed, err := executor.Store.ClaimActionOperation(ctx, request.OperationID, fingerprint)
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
	actionErr := handler(ctx, request)
	record.State = ActionOperationSucceeded
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
	if err := executor.Store.CompleteActionOperation(ctx, record); err != nil {
		return RPCActionResponse{}, fmt.Errorf("durably complete action operation: %w", err)
	}
	return actionOperationResponse(record), nil
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
		"operation_id": request.OperationID, "resource_handle": request.ResourceHandle,
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
