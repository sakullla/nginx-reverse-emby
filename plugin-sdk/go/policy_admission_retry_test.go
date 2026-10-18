package pluginsdk

import (
	"context"
	"errors"
	"testing"
)

// policyAdmissionAttempts bounds retries of one admitted host call.
const policyAdmissionAttempts = 64

// admitWithinBudget runs one admitted host call and retries only when the
// attempt expired before the host callback ran. The policy contract caps the
// admission budget at PolicyV1MaxTimeoutMilliseconds, and a descheduled test
// goroutine on a loaded CI runner can miss that window before the callback is
// invoked. Tests that assert an expected deadline call the Host helpers
// directly so the deadline result is observed instead of retried.
func admitWithinBudget[T any](t *testing.T, calls *int, call func() (T, error)) (T, error) {
	t.Helper()
	for attempt := 0; attempt < policyAdmissionAttempts; attempt++ {
		before := 0
		if calls != nil {
			before = *calls
		}
		value, err := call()
		if err == nil || !errors.Is(err, context.DeadlineExceeded) {
			return value, err
		}
		if calls != nil && *calls != before {
			// The host ran, so the budget genuinely expired inside the call.
			return value, err
		}
	}
	var zero T
	return zero, context.DeadlineExceeded
}
