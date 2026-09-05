package pluginsdk

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protowire"
)

func policyResolveTestRequest() PolicyDatasetResolveRequest {
	return PolicyDatasetResolveRequest{SourceID: "regions", Budget: DatasetQueryBudget{MaxDurationMicros: 2000, MaxResponseBytes: 4096}}
}

func TestPolicyDatasetResolveWireBoundsAndErrors(t *testing.T) {
	request := policyResolveTestRequest()
	frame, err := MarshalPolicyDatasetResolveRequest(request, 4096)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := UnmarshalPolicyDatasetResolveRequest(frame, 4096)
	if err != nil || !reflect.DeepEqual(decoded, request) {
		t.Fatalf("request roundtrip: %+v %v", decoded, err)
	}
	if _, err := MarshalPolicyDatasetResolveRequest(request, len(frame)-1); err == nil {
		t.Fatal("complete request limit ignored")
	}
	if _, err := UnmarshalPolicyDatasetResolveRequest(frame, len(frame)-1); err == nil {
		t.Fatal("oversized request decoded")
	}
	reference := datasetResolveTestReference(DatasetResolveBinding{InstanceID: "instance", Generation: "generation"}, "a")
	success := PolicyDatasetResolveResponse{Reference: &reference}
	encoded, err := MarshalPolicyDatasetResolveResponse(success, request)
	if err != nil || len(encoded) > 4096 {
		t.Fatal(err)
	}
	response, err := UnmarshalPolicyDatasetResolveResponse(encoded, request)
	if err != nil || response.Reference == nil || *response.Reference != reference {
		t.Fatalf("response roundtrip: %+v %v", response, err)
	}
	for _, code := range []ErrorCode{ErrorPermissionDenied, ErrorUnavailable, ErrorResourceExhausted, ErrorDeadlineExceeded} {
		failure := PolicyDatasetResolveResponse{Error: &RuntimeError{Code: code, Message: "binding unavailable"}}
		wire, err := MarshalPolicyDatasetResolveResponse(failure, request)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := UnmarshalPolicyDatasetResolveResponse(wire, request)
		if err != nil || decoded.Reference != nil || decoded.Error == nil || decoded.Error.Code != code {
			t.Fatal("failure did not roundtrip independently of a reference", err)
		}
	}
	small := request
	small.Budget.MaxResponseBytes = len(encoded) - 1
	if _, err := MarshalPolicyDatasetResolveResponse(success, small); err == nil {
		t.Fatal("complete response limit ignored")
	}
	if _, err := UnmarshalPolicyDatasetResolveResponse(encoded, small); err == nil {
		t.Fatal("oversized response decoded")
	}
	for _, response := range []PolicyDatasetResolveResponse{{}, {Reference: &reference, Error: &RuntimeError{Code: ErrorUnavailable}}, {Error: &RuntimeError{}}, {Error: &RuntimeError{Code: ErrorUnavailable, Message: strings.Repeat("x", 4096)}}} {
		if _, err := MarshalPolicyDatasetResolveResponse(response, request); err == nil {
			t.Fatal("invalid/oversized result accepted")
		}
	}
	for _, mutate := range []func(*PolicyDatasetResolveRequest){func(r *PolicyDatasetResolveRequest) { r.SourceID = "" }, func(r *PolicyDatasetResolveRequest) { r.SourceID = strings.Repeat("a", PolicyIdentityMaxBytes+1) }, func(r *PolicyDatasetResolveRequest) { r.Budget.MaxDurationMicros = 2001 }, func(r *PolicyDatasetResolveRequest) { r.Budget.MaxResponseBytes = 4097 }, func(r *PolicyDatasetResolveRequest) { r.Budget = DatasetQueryBudget{} }} {
		invalid := request
		mutate(&invalid)
		if _, err := MarshalPolicyDatasetResolveRequest(invalid, 4096); err == nil {
			t.Fatal("invalid source/resolve budget accepted")
		}
	}
}

func TestPolicyDatasetResolveRejectsMalformedRepeatedAndForeignWire(t *testing.T) {
	request := policyResolveTestRequest()
	frame, err := MarshalPolicyDatasetResolveRequest(request, 4096)
	if err != nil {
		t.Fatal(err)
	}
	unknown := protowire.AppendVarint(protowire.AppendTag(nil, 99, protowire.VarintType), 1)
	for _, bad := range [][]byte{nil, {0xff}, append(append([]byte(nil), frame...), frame...), append(append([]byte(nil), frame...), unknown...), protowire.AppendVarint(protowire.AppendTag(nil, 1, protowire.VarintType), 1)} {
		if _, err := UnmarshalPolicyDatasetResolveRequest(bad, 4096); err == nil {
			t.Fatal("malformed/repeated request accepted")
		}
	}
	reference := datasetResolveTestReference(DatasetResolveBinding{InstanceID: "i", Generation: "g"}, "a")
	success, _ := MarshalPolicyDatasetResolveResponse(PolicyDatasetResolveResponse{Reference: &reference}, request)
	failure, _ := MarshalPolicyDatasetResolveResponse(PolicyDatasetResolveResponse{Error: &RuntimeError{Code: ErrorUnavailable}}, request)
	for _, bad := range [][]byte{nil, {0xff}, append(append([]byte(nil), success...), success...), append(append([]byte(nil), success...), failure...), append(append([]byte(nil), success...), unknown...)} {
		if _, err := UnmarshalPolicyDatasetResolveResponse(bad, request); err == nil {
			t.Fatal("ambiguous/malformed response accepted")
		}
	}
	reference.SourceID = "foreign"
	if _, err := MarshalPolicyDatasetResolveResponse(PolicyDatasetResolveResponse{Reference: &reference}, request); err == nil {
		t.Fatal("wrong source response encoded")
	}
	message, err := newPolicyExtensionMessage("DatasetResolveResponse")
	if err != nil {
		t.Fatal(err)
	}
	encodePolicyDatasetReference(policyExtensionChild(message, "reference"), reference)
	foreign, err := marshalPolicyExtension(message, 4096, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UnmarshalPolicyDatasetResolveResponse(foreign, request); err == nil {
		t.Fatal("wrong source response decoded")
	}
}

func TestPolicyDatasetResolveHostBindsCallerAndEnclosingBudget(t *testing.T) {
	request := policyResolveTestRequest()
	frame, err := MarshalPolicyDatasetResolveRequest(request, 4096)
	if err != nil {
		t.Fatal(err)
	}
	old := datasetResolveTestAuthorization("instance", "old")
	fresh := datasetResolveTestAuthorization("instance", "new")
	refs := map[DatasetResolveBinding]DatasetReference{old.Binding: datasetResolveTestReference(old.Binding, "a"), fresh.Binding: datasetResolveTestReference(fresh.Binding, "b")}
	// Warm shared descriptors before measuring the admitted call's deadline.
	for _, reference := range refs {
		if _, err := MarshalPolicyDatasetResolveResponse(PolicyDatasetResolveResponse{Reference: &reference}, request); err != nil {
			t.Fatal(err)
		}
	}
	budget := PolicyV1ResourceBudget{TimeoutMilliseconds: 2, MemoryBytes: 1 << 20, Concurrency: 1, InputFrameBytes: 4096, OutputFrameBytes: 4096}
	calls := 0
	host := datasetResolveHostFunc(func(ctx context.Context, binding DatasetResolveBinding, request DatasetResolveRequest) (DatasetReference, error) {
		calls++
		if _, ok := ctx.Deadline(); !ok {
			t.Error("resolve has no invocation deadline")
		}
		reference, ok := refs[binding]
		if !ok {
			return DatasetReference{}, &RuntimeError{Code: ErrorUnavailable, Message: "revoked or missing binding"}
		}
		return reference, nil
	})
	policyAuth := func(value DatasetResolveAuthorization) PolicyHostCallAuthorization {
		return PolicyHostCallAuthorization{InstanceID: value.Binding.InstanceID, Generation: value.Binding.Generation, EntryID: "entry", DeclaredScopes: value.DeclaredScopes, GrantedScopes: value.GrantedScopes}
	}
	for _, authorization := range []DatasetResolveAuthorization{old, fresh, old} {
		encoded, err := CallPolicyDatasetResolveHost(t.Context(), host, policyAuth(authorization), frame, budget)
		if err != nil {
			t.Fatal(err)
		}
		response, err := UnmarshalPolicyDatasetResolveResponse(encoded, request)
		if err != nil || response.Reference == nil || *response.Reference != refs[authorization.Binding] {
			t.Fatal("resolve selected another generation", err)
		}
	}
	before := calls
	missing := policyAuth(fresh)
	missing.GrantedScopes = []string{string(CapabilityDatasetQuery)}
	if _, err := CallPolicyDatasetResolveHost(t.Context(), host, missing, frame, budget); PolicySecurityCallStatus(err) != PolicyStatusPermissionDenied || calls != before {
		t.Fatal("missing resolver grant reached registry", err)
	}
	wrong := datasetResolveHostFunc(func(context.Context, DatasetResolveBinding, DatasetResolveRequest) (DatasetReference, error) {
		return refs[old.Binding], nil
	})
	if _, err := CallPolicyDatasetResolveHost(t.Context(), wrong, policyAuth(fresh), frame, budget); PolicySecurityCallStatus(err) != PolicyStatusPermissionDenied {
		t.Fatal("new generation accepted old reference", err)
	}
	wrongInstance := refs[fresh.Binding]
	wrongInstance.InstanceID = "other-instance"
	wrong = datasetResolveHostFunc(func(context.Context, DatasetResolveBinding, DatasetResolveRequest) (DatasetReference, error) {
		return wrongInstance, nil
	})
	if _, err := CallPolicyDatasetResolveHost(t.Context(), wrong, policyAuth(fresh), frame, budget); PolicySecurityCallStatus(err) != PolicyStatusPermissionDenied {
		t.Fatal("foreign instance reference accepted", err)
	}
	delete(refs, old.Binding)
	if _, err := CallPolicyDatasetResolveHost(t.Context(), host, policyAuth(old), frame, budget); PolicySecurityCallStatus(err) != PolicyStatusUnavailable {
		t.Fatal("revoked binding returned success", err)
	}
	small := budget
	small.OutputFrameBytes = 1024
	if _, err := CallPolicyDatasetResolveHost(t.Context(), host, policyAuth(fresh), frame, small); PolicySecurityCallStatus(err) != PolicyStatusResourceExhausted {
		t.Fatal("resolve exceeded manifest output limit", err)
	}
	parent, cancel := context.WithTimeout(t.Context(), time.Millisecond)
	defer cancel()
	parentDeadline, _ := parent.Deadline()
	blocking := datasetResolveHostFunc(func(ctx context.Context, _ DatasetResolveBinding, _ DatasetResolveRequest) (DatasetReference, error) {
		deadline, _ := ctx.Deadline()
		if deadline.After(parentDeadline) {
			t.Error("nested resolution reset enclosing deadline")
		}
		<-ctx.Done()
		return refs[fresh.Binding], nil
	})
	if _, err := CallPolicyDatasetResolveHost(parent, blocking, policyAuth(fresh), frame, budget); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("enclosing deadline was ignored", err)
	}
	canceled, stop := context.WithCancel(t.Context())
	stop()
	if _, err := CallPolicyDatasetResolveHost(canceled, host, policyAuth(fresh), frame, budget); !errors.Is(err, context.Canceled) {
		t.Fatal("enclosing cancellation was ignored", err)
	}
}
