package pluginsdk

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"
)

type policyDatasetHostFunc func(context.Context, PolicyDatasetQueryRequest) (DatasetQueryResponse, error)

func (call policyDatasetHostFunc) QuerySourceDatasets(ctx context.Context, request PolicyDatasetQueryRequest) (DatasetQueryResponse, error) {
	return call(ctx, request)
}

type policySourceHostFunc func(context.Context) (PolicyTrustedSource, error)

func (call policySourceHostFunc) ReadTrustedSource(ctx context.Context) (PolicyTrustedSource, error) {
	return call(ctx)
}

func TestPolicySecurityCallsEnforceGrantBindingAndAdmissionBudget(t *testing.T) {
	query := policyDatasetTestQuery()
	query.Budget.MaxDurationMicros = 2000
	frame, err := MarshalPolicyDatasetQueryRequest(query, 4096)
	if err != nil {
		t.Fatal(err)
	}
	// Initialize response descriptors outside the simulated admission, as Host
	// preparation does. This test asserts boundaries, not cold-start scheduling.
	if _, err := MarshalPolicyDatasetQueryResponse(DatasetQueryResponse{Reference: query.Reference, Status: DatasetQueryUnavailable}, query); err != nil {
		t.Fatal(err)
	}
	budget := PolicyV1ResourceBudget{TimeoutMilliseconds: 2, MemoryBytes: 1 << 20, Concurrency: 1, InputFrameBytes: 4096, OutputFrameBytes: 4096}
	scopes := []string{string(CapabilityDatasetQuery), string(CapabilityPolicyTrustedSource)}
	authorization := PolicyHostCallAuthorization{InstanceID: query.Reference.InstanceID, Generation: query.Reference.Generation, EntryID: "entry-1", DeclaredScopes: scopes, GrantedScopes: scopes}
	calls := 0
	host := policyDatasetHostFunc(func(ctx context.Context, request PolicyDatasetQueryRequest) (DatasetQueryResponse, error) {
		calls++
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 2*time.Millisecond {
			t.Error("query was detached from admission budget")
		}
		return DatasetQueryResponse{Reference: request.Reference, Status: DatasetQueryOK, Matches: []DatasetMatch{{Index: 0, Coverage: DatasetCovered}}}, nil
	})
	responseFrame, err := CallPolicyDatasetHost(t.Context(), host, authorization, frame, budget)
	if err != nil {
		t.Fatal(err)
	}
	response, err := UnmarshalPolicyDatasetQueryResponse(responseFrame, query)
	if err != nil || response.Status != DatasetQueryOK || calls != 1 {
		t.Fatalf("public Host contract failed: %+v %v", response, err)
	}
	missing := authorization
	missing.GrantedScopes = scopes[:1]
	if _, err := CallPolicyDatasetHost(t.Context(), host, missing, frame, budget); PolicySecurityCallStatus(err) != PolicyStatusPermissionDenied {
		t.Fatalf("missing grant status: %v", err)
	}
	foreign := authorization
	foreign.Generation = "generation-other"
	if _, err := CallPolicyDatasetHost(t.Context(), host, foreign, frame, budget); err == nil {
		t.Fatal("foreign binding reached Host")
	}
	if calls != 1 {
		t.Fatal("unauthorized query invoked Host")
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := CallPolicyDatasetHost(canceled, host, authorization, frame, budget); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled admission result: %v", err)
	}
	if calls != 1 {
		t.Fatal("canceled admission invoked Host")
	}
	blocking := policyDatasetHostFunc(func(ctx context.Context, request PolicyDatasetQueryRequest) (DatasetQueryResponse, error) {
		<-ctx.Done()
		// A late success from the adapter must not become a valid non-match.
		return DatasetQueryResponse{Reference: request.Reference, Status: DatasetQueryUnavailable}, nil
	})
	if _, err := CallPolicyDatasetHost(t.Context(), blocking, authorization, frame, budget); PolicySecurityCallStatus(err) != PolicyStatusDeadlineExceeded {
		t.Fatalf("late response did not consume admission budget: %v", err)
	}
	source := PolicyTrustedSource{InstanceID: authorization.InstanceID, Generation: authorization.Generation, EntryID: authorization.EntryID, PeerAddress: netip.MustParseAddr("192.0.2.1"), SourceAddress: netip.MustParseAddr("192.0.2.1"), Authority: PolicySourceSocket}
	sourceHost := policySourceHostFunc(func(context.Context) (PolicyTrustedSource, error) { return source, nil })
	if _, err := CallPolicyTrustedSourceHost(t.Context(), sourceHost, authorization, nil, budget); err != nil {
		t.Fatal(err)
	}
	source.EntryID = "foreign-entry"
	if _, err := CallPolicyTrustedSourceHost(t.Context(), sourceHost, authorization, nil, budget); err == nil {
		t.Fatal("Host source from another entry accepted")
	}
	if _, err := CallPolicyTrustedSourceHost(t.Context(), sourceHost, authorization, []byte{0x08, 1}, budget); err == nil {
		t.Fatal("self-reported trusted request accepted")
	}
}
