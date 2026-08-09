package policy

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/observability"
)

const (
	ExtensionHTTP = "http.request"
	ExtensionL4   = "l4.accept"

	FieldRequestMethod       = "request.method"
	FieldRequestHost         = "request.host"
	FieldRequestPath         = "request.path"
	FieldRequestQuery        = "request.query"
	FieldRequestScheme       = "request.scheme"
	FieldRequestHeaderPrefix = "request.header."
	FieldFlowProtocol        = "flow.protocol"
	FieldFlowTargetIP        = "flow.target_ip"
	FieldFlowTargetPort      = "flow.target_port"
	// FieldFlowNew is true for HTTP requests, accepted TCP connections, and the
	// first datagram of a new UDP association. Later UDP datagrams still run the
	// whole chain with false so the rate stage does not count another new flow.
	FieldFlowNew = "flow.new"

	MaxPolicyTimeout        = 2 * time.Millisecond
	MaxPolicyMemoryBytes    = int64(16 << 20)
	MaxPolicyConcurrency    = 64
	MaxPolicyInputBytes     = int64(128 << 10)
	MaxPolicyOutputBytes    = int64(4 << 10)
	MaxBodyPrefixBytes      = 128 << 10
	MaxPolicyRequestIDBytes = 256
)

func CanonicalHTTPHeaderField(name string) (string, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" || len(name) > 256 {
		return "", false
	}
	for _, char := range name {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-' {
			continue
		}
		return "", false
	}
	return FieldRequestHeaderPrefix + name, true
}

type Action string

const (
	ActionAllow   Action = "allow"
	ActionDeny    Action = "deny"
	ActionObserve Action = "observe"
)

func (action Action) valid() bool {
	return action == ActionAllow || action == ActionDeny || action == ActionObserve
}

type Decision struct {
	Action     Action
	StatusCode int
	Stage      model.PolicyKind
	PolicyID   string
	Reason     string
	Degraded   bool
	Observed   bool
}

type Evaluator interface {
	Evaluate(context.Context, *model.PolicyRef, Input) Decision
}

// Host is the complete request-scoped nre:policy/v1 host surface. It does not
// expose files, network, processes, clocks, credentials, or host memory.
type Host interface {
	ReadField(context.Context, string) ([]byte, error)
	ReadBodyWindow(context.Context, uint32, uint32) ([]byte, error)
	StateGet(context.Context, string) ([]byte, bool, error)
	StatePut(context.Context, string, []byte) error
	EmitEvent(context.Context, string, []byte) error
	AddMetric(context.Context, string, int64) error
}

type ModuleRequest struct {
	GenerationID   string
	PolicyID       string
	PolicyKind     model.PolicyKind
	InstanceID     string
	ExtensionPoint string
	RequestID      string
	Payload        []byte
	Budget         model.PolicyResourceBudget
	Host           Host
}

type ModuleResponse struct {
	Action  Action
	Payload []byte
}

// ModuleEvaluator is implemented by a prepared generation runtime (for T04,
// the wazero runtime). HTTP and L4 depend only on Evaluator and never construct
// or call a ModuleEvaluator directly.
type ModuleEvaluator interface {
	Evaluate(context.Context, ModuleRequest) (ModuleResponse, error)
}

type FailureKind string

const (
	FailureRuntime FailureKind = "runtime"
	FailureBudget  FailureKind = "budget"
)

type EvaluationError struct {
	Kind FailureKind
	Code string
	Err  error
}

func (e *EvaluationError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return fmt.Sprintf("policy %s failure: %s", e.Kind, e.Code)
	}
	return fmt.Sprintf("policy %s failure: %s: %v", e.Kind, e.Code, e.Err)
}

func (e *EvaluationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func BudgetError(code string, err error) error {
	return &EvaluationError{Kind: FailureBudget, Code: code, Err: err}
}

func RuntimeError(code string, err error) error {
	return &EvaluationError{Kind: FailureRuntime, Code: code, Err: err}
}

func failureKind(err error) FailureKind {
	var evaluationError *EvaluationError
	if errors.As(err, &evaluationError) && evaluationError.Kind == FailureBudget {
		return FailureBudget
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return FailureBudget
	}
	return FailureRuntime
}

func observeEvent(ctx context.Context, observer observability.Observer, event observability.Event) {
	if observer == nil {
		return
	}
	observability.Observe(observability.WithObserver(ctx, observer), event)
}
