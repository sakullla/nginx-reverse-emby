package policy

import (
	"context"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func (host *requestHost) PolicyAuthorization() sdk.PolicyHostCallAuthorization {
	return sdk.PolicyHostCallAuthorization{InstanceID: host.instanceID, Generation: host.generationID, EntryID: host.input.entryID, DeclaredScopes: host.stage.DeclaredScopes, GrantedScopes: host.stage.GrantedScopes}
}
func (host *requestHost) ResolveDataset(ctx context.Context, binding sdk.DatasetResolveBinding, request sdk.DatasetResolveRequest) (sdk.DatasetReference, error) {
	if host.datasets == nil || binding.InstanceID != host.instanceID || binding.Generation != host.generationID {
		return sdk.DatasetReference{}, &sdk.RuntimeError{Code: sdk.ErrorUnavailable, Message: "generation dataset binding unavailable"}
	}
	return host.datasets.ResolveDataset(ctx, binding, request)
}
func (host *requestHost) ReadTrustedSource(ctx context.Context) (sdk.PolicyTrustedSource, error) {
	if err := ctx.Err(); err != nil {
		return sdk.PolicyTrustedSource{}, err
	}
	if sdk.ValidateHostCapabilityGrant(sdk.CapabilityPolicyTrustedSource, host.stage.DeclaredScopes, host.stage.GrantedScopes) != nil || !host.input.metadata.authorized {
		return sdk.PolicyTrustedSource{}, &sdk.RuntimeError{Code: sdk.ErrorPermissionDenied, Message: "trusted source unavailable"}
	}
	authority := sdk.PolicySourceSocket
	switch host.input.metadata.kind {
	case SourceTrustedProxy:
		authority = sdk.PolicySourceXFF
	case SourceProxyProtocol:
		authority = sdk.PolicySourcePROXY
	case SourceRelay:
		authority = sdk.PolicySourceRelay
	}
	source := sdk.PolicyTrustedSource{InstanceID: host.instanceID, Generation: host.generationID, EntryID: host.input.entryID, SourceAddress: host.input.metadata.source.Addr().Unmap(), PeerAddress: host.input.metadata.peer.Addr().Unmap(), Authority: authority}
	return source, source.Validate()
}
func (host *requestHost) QuerySourceDatasets(ctx context.Context, request sdk.PolicyDatasetQueryRequest) (sdk.DatasetQueryResponse, error) {
	source, err := host.ReadTrustedSource(ctx)
	if err != nil {
		return sdk.DatasetQueryResponse{}, err
	}
	if host.datasets == nil {
		return sdk.DatasetQueryResponse{}, &sdk.RuntimeError{Code: sdk.ErrorUnavailable, Message: "generation dataset unavailable"}
	}
	response, err := host.datasets.Query(ctx, host.PolicyAuthorization(), sdk.DatasetQueryRequest{Reference: request.Reference, Address: source.SourceAddress.String(), Classifications: request.Classifications, Budget: request.Budget})
	if err == nil && response.Status != sdk.DatasetQueryOK {
		reason := "dataset-unavailable"
		switch response.Status {
		case sdk.DatasetQueryMissingClassification:
			reason = "classification-missing"
		case sdk.DatasetQueryBudgetExceeded:
			reason = "budget-exceeded"
		case sdk.DatasetQueryUnauthorized, sdk.DatasetQueryStaleReference:
			reason = "revoked"
		case sdk.DatasetQueryInvalidData:
			reason = "invalid-result"
		}
		host.checkFailure = reason
	}
	return response, err
}
func (host *requestHost) RecordPolicyHostFailure(name string, status sdk.PolicyStatus) {
	if status == sdk.PolicyStatusOK {
		return
	}
	reason := "guest-failure"
	switch name {
	case sdk.PolicyHostReadTrustedSource, sdk.PolicyHostReadNormalizedHTTP:
		reason = "source-unavailable"
	case sdk.PolicyHostDatasetResolve, sdk.PolicyHostDatasetQuery:
		reason = "dataset-unavailable"
	}
	if status == sdk.PolicyStatusResourceExhausted || status == sdk.PolicyStatusDeadlineExceeded {
		reason = "budget-exceeded"
	}
	if host.checkFailure == "" {
		host.checkFailure = reason
	}
}

// Initialization resolves resources for the actual generation, without inventing
// a connection entry or source. Request/body/state/event imports remain unavailable.
type initializationHost struct{ *requestHost }

func NewInitializationHost(generation string, stage model.PolicyStage, datasets *DatasetGeneration) Host {
	return &initializationHost{&requestHost{generationID: generation, instanceID: stage.InstanceID, stage: model.ClonePolicyStage(stage), datasets: datasets}}
}
func initUnavailable() error {
	return &sdk.RuntimeError{Code: sdk.ErrorUnavailable, Message: "connection Host operation unavailable during init"}
}
func (*initializationHost) ReadField(context.Context, string) ([]byte, error) {
	return nil, initUnavailable()
}
func (*initializationHost) ReadBodyWindow(context.Context, uint32, uint32) ([]byte, error) {
	return nil, initUnavailable()
}
func (*initializationHost) StateGet(context.Context, string) ([]byte, bool, error) {
	return nil, false, initUnavailable()
}
func (*initializationHost) StatePut(context.Context, string, []byte) error { return initUnavailable() }
func (*initializationHost) EmitEvent(context.Context, sdk.PolicySecurityEvent) error {
	return initUnavailable()
}
func (*initializationHost) AddMetric(context.Context, string, int64) error { return initUnavailable() }
func (*initializationHost) ReadNormalizedHTTP(context.Context) (sdk.PolicyNormalizedHTTP, error) {
	return sdk.PolicyNormalizedHTTP{}, initUnavailable()
}
