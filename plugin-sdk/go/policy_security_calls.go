package pluginsdk

import (
	"context"
	"errors"
	"time"
)

// PolicyHostCallAuthorization comes from the Host's authenticated invocation,
// signed package and live administrator grant. It must never be decoded from a
// guest request. Resource grant lookup/revocation and quota remain Host duties.
type PolicyHostCallAuthorization struct {
	InstanceID     string
	Generation     string
	EntryID        string
	DeclaredScopes []string
	GrantedScopes  []string
}

func (authorization PolicyHostCallAuthorization) Validate() error {
	for _, id := range []string{authorization.InstanceID, authorization.Generation, authorization.EntryID} {
		if err := ValidatePolicyIdentity(id); err != nil {
			return err
		}
	}
	return nil
}

// CallPolicyDatasetHost is the canonical bounded decoder/authorization/response
// boundary for nre_host_dataset_query. ctx must be the enclosing admission
// context, so this nested call cannot reset its deadline. Host implementations
// must cooperate with cancellation and use bounded local indices, never fetch.
func CallPolicyDatasetHost(ctx context.Context, host PolicyDatasetHost, authorization PolicyHostCallAuthorization, frame []byte, budget PolicyV1ResourceBudget) ([]byte, error) {
	if err := budget.Validate(); err != nil {
		return nil, err
	}
	ctx, stopAdmission := context.WithTimeout(ctx, time.Duration(budget.TimeoutMilliseconds)*time.Millisecond)
	defer stopAdmission()
	if err := authorization.Validate(); err != nil {
		return nil, err
	}
	if err := ValidatePolicyV1ImportGrant(PolicyHostDatasetQuery, authorization.DeclaredScopes, authorization.GrantedScopes); err != nil {
		return nil, &RuntimeError{Code: ErrorPermissionDenied, Message: "dataset source query capability denied"}
	}
	request, err := UnmarshalPolicyDatasetQueryRequest(frame, int(budget.InputFrameBytes))
	if err != nil {
		return nil, err
	}
	if err := request.ValidateFor(authorization.InstanceID, authorization.Generation); err != nil {
		return nil, err
	}
	if request.Budget.MaxResponseBytes > int(budget.OutputFrameBytes) {
		return nil, &RuntimeError{Code: ErrorResourceExhausted, Message: "dataset response budget exceeds manifest"}
	}
	if host == nil {
		return nil, &RuntimeError{Code: ErrorUnavailable, Message: "dataset source query unavailable"}
	}
	queryContext, cancel := context.WithTimeout(ctx, time.Duration(request.Budget.MaxDurationMicros)*time.Microsecond)
	defer cancel()
	if err := queryContext.Err(); err != nil {
		return nil, err
	}
	response, err := host.QuerySourceDatasets(queryContext, request)
	if err != nil {
		return nil, err
	}
	if err := queryContext.Err(); err != nil {
		return nil, err
	}
	encoded, err := MarshalPolicyDatasetQueryResponse(response, request)
	if err != nil {
		return nil, err
	}
	if err := queryContext.Err(); err != nil {
		return nil, err
	}
	return encoded, nil
}

// CallPolicyTrustedSourceHost accepts an empty request and validates the actual
// Host source against this call's instance, generation and entry. Missing source
// or authority fails; no trusted=true fallback is synthesized.
func CallPolicyTrustedSourceHost(ctx context.Context, host PolicyTrustedSourceHost, authorization PolicyHostCallAuthorization, frame []byte, budget PolicyV1ResourceBudget) ([]byte, error) {
	if err := budget.Validate(); err != nil {
		return nil, err
	}
	ctx, stopAdmission := context.WithTimeout(ctx, time.Duration(budget.TimeoutMilliseconds)*time.Millisecond)
	defer stopAdmission()
	if err := authorization.Validate(); err != nil {
		return nil, err
	}
	if err := ValidatePolicyTrustedSourceRequestFrame(frame); err != nil {
		return nil, err
	}
	if err := ValidatePolicyV1ImportGrant(PolicyHostReadTrustedSource, authorization.DeclaredScopes, authorization.GrantedScopes); err != nil {
		return nil, &RuntimeError{Code: ErrorPermissionDenied, Message: "trusted source capability denied"}
	}
	if host == nil {
		return nil, &RuntimeError{Code: ErrorUnavailable, Message: "trusted source unavailable"}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	source, err := host.ReadTrustedSource(ctx)
	if err != nil {
		return nil, err
	}
	if err := source.ValidateFor(authorization.InstanceID, authorization.Generation, authorization.EntryID); err != nil {
		return nil, err
	}
	encoded, err := MarshalPolicyTrustedSourceResponse(PolicyTrustedSourceResponse{Source: &source}, int(budget.OutputFrameBytes))
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return encoded, nil
}

// PolicySecurityCallStatus maps boundary errors to the existing policy/v1
// numeric status; no new ABI status or unbounded error string is introduced.
func PolicySecurityCallStatus(err error) PolicyStatus {
	if err == nil {
		return PolicyStatusOK
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return PolicyStatusDeadlineExceeded
	}
	if errors.Is(err, context.Canceled) {
		return PolicyStatusUnavailable
	}
	var failure *RuntimeError
	if errors.As(err, &failure) && failure.Code.Valid() {
		return PolicyStatus(failure.Code)
	}
	return PolicyStatusInvalidArgument
}
