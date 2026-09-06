package pluginsdk

import (
	"context"
	"errors"
	"time"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// PolicyDatasetResolveRequest adds only a per-call budget to source selection.
// Host identity is inherited from the authenticated invocation, including init
// when supported by the Host. Resolution does not require a connection source.
type PolicyDatasetResolveRequest struct {
	SourceID string
	Budget   DatasetQueryBudget
}

func (request PolicyDatasetResolveRequest) Validate() error {
	if err := (DatasetResolveRequest{SourceID: request.SourceID}).Validate(); err != nil {
		return err
	}
	if err := request.Budget.Validate(); err != nil {
		return err
	}
	if request.Budget.MaxResponseBytes > DatasetResolveMaxFrameBytes {
		return errors.New("policy dataset resolve response budget exceeds 4 KiB")
	}
	return nil
}

type PolicyDatasetResolveResponse struct {
	Reference *DatasetReference
	Error     *RuntimeError
}

func (response PolicyDatasetResolveResponse) Validate() error {
	if (response.Reference == nil) == (response.Error == nil) {
		return errors.New("dataset resolve response requires exactly one reference or error")
	}
	if response.Error != nil {
		return response.Error.Validate()
	}
	return response.Reference.Validate()
}
func (response PolicyDatasetResolveResponse) ValidateFor(request PolicyDatasetResolveRequest) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if err := response.Validate(); err != nil {
		return err
	}
	if response.Reference != nil && response.Reference.SourceID != request.SourceID {
		return errors.New("policy dataset resolver returned another source")
	}
	return nil
}

func MarshalPolicyDatasetResolveRequest(request PolicyDatasetResolveRequest, inputFrameBytes int) ([]byte, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	message, err := newPolicyExtensionMessage("DatasetResolveRequest")
	if err != nil {
		return nil, err
	}
	policyExtensionSet(message, "source_id", protoreflect.ValueOfString(request.SourceID))
	policyExtensionSet(message, "max_duration_micros", protoreflect.ValueOfUint32(uint32(request.Budget.MaxDurationMicros)))
	policyExtensionSet(message, "max_response_bytes", protoreflect.ValueOfUint32(uint32(request.Budget.MaxResponseBytes)))
	return marshalPolicyExtension(message, inputFrameBytes, int(PolicyV1MaxInputFrameBytes))
}
func UnmarshalPolicyDatasetResolveRequest(frame []byte, inputFrameBytes int) (PolicyDatasetResolveRequest, error) {
	message, err := unmarshalPolicyExtension("DatasetResolveRequest", frame, inputFrameBytes, int(PolicyV1MaxInputFrameBytes))
	if err != nil {
		return PolicyDatasetResolveRequest{}, err
	}
	request := PolicyDatasetResolveRequest{SourceID: policyExtensionGet(message, "source_id").String(), Budget: DatasetQueryBudget{MaxDurationMicros: int(policyExtensionGet(message, "max_duration_micros").Uint()), MaxResponseBytes: int(policyExtensionGet(message, "max_response_bytes").Uint())}}
	if err := request.Validate(); err != nil {
		return PolicyDatasetResolveRequest{}, err
	}
	return request, nil
}
func MarshalPolicyDatasetResolveResponse(response PolicyDatasetResolveResponse, request PolicyDatasetResolveRequest) ([]byte, error) {
	if err := response.ValidateFor(request); err != nil {
		return nil, err
	}
	message, err := newPolicyExtensionMessage("DatasetResolveResponse")
	if err != nil {
		return nil, err
	}
	if response.Reference != nil {
		encodePolicyDatasetReference(policyExtensionChild(message, "reference"), *response.Reference)
	} else {
		failure := policyExtensionChild(message, "error")
		policyExtensionSet(failure, "code", protoreflect.ValueOfEnum(protoreflect.EnumNumber(response.Error.Code)))
		policyExtensionSet(failure, "message", protoreflect.ValueOfString(response.Error.Message))
		policyExtensionSet(failure, "retryable", protoreflect.ValueOfBool(response.Error.Retryable))
	}
	return marshalPolicyExtension(message, request.Budget.MaxResponseBytes, DatasetResolveMaxFrameBytes)
}
func UnmarshalPolicyDatasetResolveResponse(frame []byte, request PolicyDatasetResolveRequest) (PolicyDatasetResolveResponse, error) {
	if err := request.Validate(); err != nil {
		return PolicyDatasetResolveResponse{}, err
	}
	message, err := unmarshalPolicyExtension("DatasetResolveResponse", frame, request.Budget.MaxResponseBytes, DatasetResolveMaxFrameBytes)
	if err != nil {
		return PolicyDatasetResolveResponse{}, err
	}
	var response PolicyDatasetResolveResponse
	if message.Has(policyExtensionField(message, "reference")) {
		reference := decodePolicyDatasetReference(policyExtensionGet(message, "reference").Message())
		response.Reference = &reference
	} else if message.Has(policyExtensionField(message, "error")) {
		failure := policyExtensionGet(message, "error").Message()
		response.Error = &RuntimeError{Code: ErrorCode(policyExtensionGet(failure, "code").Enum()), Message: policyExtensionGet(failure, "message").String(), Retryable: policyExtensionGet(failure, "retryable").Bool()}
	}
	if err := response.ValidateFor(request); err != nil {
		return PolicyDatasetResolveResponse{}, err
	}
	return response, nil
}

// CallPolicyDatasetResolveHost is the bounded optional import boundary. The
// enclosing invocation context must be passed through, so nested resolution
// cannot reset its admission deadline. Returned identity is checked against
// actual Host authorization before any reference is delivered to the guest.
// Connectionless initialization has no EntryID: resolve authenticates only the
// resource's instance/generation and grants. Source read/query retain their
// separate connection-entry validation.
func CallPolicyDatasetResolveHost(ctx context.Context, host DatasetResolveHost, authorization PolicyHostCallAuthorization, frame []byte, budget PolicyV1ResourceBudget) ([]byte, error) {
	if err := budget.Validate(); err != nil {
		return nil, err
	}
	ctx, stop := context.WithTimeout(ctx, time.Duration(budget.TimeoutMilliseconds)*time.Millisecond)
	defer stop()
	binding := DatasetResolveBinding{InstanceID: authorization.InstanceID, Generation: authorization.Generation}
	if err := binding.Validate(); err != nil {
		return nil, err
	}
	request, err := UnmarshalPolicyDatasetResolveRequest(frame, int(budget.InputFrameBytes))
	if err != nil {
		return nil, err
	}
	if request.Budget.MaxResponseBytes > int(budget.OutputFrameBytes) {
		return nil, &RuntimeError{Code: ErrorResourceExhausted, Message: "dataset resolve response budget exceeds manifest"}
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(request.Budget.MaxDurationMicros)*time.Microsecond)
	defer cancel()
	reference, err := resolveDatasetForCaller(ctx, host, DatasetResolveAuthorization{Binding: binding, DeclaredScopes: authorization.DeclaredScopes, GrantedScopes: authorization.GrantedScopes}, DatasetResolveRequest{SourceID: request.SourceID})
	if err != nil {
		return nil, err
	}
	encoded, err := MarshalPolicyDatasetResolveResponse(PolicyDatasetResolveResponse{Reference: &reference}, request)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return encoded, nil
}
