package pluginsdk

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

func TestTwentyMillisecondAdmissionPreservesParentDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		query := policyDatasetTestQuery()
		query.Budget.MaxDurationMicros = 20000
		frame, err := MarshalPolicyDatasetQueryRequest(query, 4096)
		if err != nil {
			t.Fatal(err)
		}
		budget := PolicyV1ResourceBudget{TimeoutMilliseconds: 20, MemoryBytes: 1 << 20, Concurrency: 1, InputFrameBytes: 4096, OutputFrameBytes: 4096}
		scopes := []string{string(CapabilityDatasetQuery), string(CapabilityPolicyTrustedSource)}
		auth := PolicyHostCallAuthorization{InstanceID: query.Reference.InstanceID, Generation: query.Reference.Generation, EntryID: "entry", DeclaredScopes: scopes, GrantedScopes: scopes}
		delay := 5 * time.Millisecond
		host := policyDatasetHostFunc(func(ctx context.Context, req PolicyDatasetQueryRequest) (DatasetQueryResponse, error) {
			time.Sleep(delay)
			return DatasetQueryResponse{Reference: req.Reference, Status: DatasetQueryOK, Matches: []DatasetMatch{{Index: 0, Coverage: DatasetCovered}}}, nil
		})
		if _, err := CallPolicyDatasetHost(t.Context(), host, auth, frame, budget); err != nil {
			t.Fatalf("5ms operation must fit the 20ms budget: %v", err)
		}
		delay = 21 * time.Millisecond
		if _, err := CallPolicyDatasetHost(t.Context(), host, auth, frame, budget); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("20ms ceiling not enforced: %v", err)
		}
		delay = 5 * time.Millisecond
		parent, cancel := context.WithTimeout(t.Context(), time.Millisecond)
		defer cancel()
		if _, err := CallPolicyDatasetHost(parent, host, auth, frame, budget); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("shorter parent deadline was extended: %v", err)
		}
	})
}
