package policy

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/observability"
	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
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

	MaxPolicyTimeout        = time.Duration(pluginsdk.PolicyV1MaxTimeoutMilliseconds) * time.Millisecond
	MaxPolicyMemoryBytes    = pluginsdk.PolicyV1MaxMemoryBytes
	MaxPolicyConcurrency    = pluginsdk.PolicyV1MaxConcurrency
	MaxPolicyInputBytes     = pluginsdk.PolicyV1MaxInputFrameBytes
	MaxPolicyOutputBytes    = pluginsdk.PolicyV1MaxOutputFrameBytes
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
	// Budget input/output values count the complete protobuf wire frames built
	// by the module adapter, never only Payload or the returned payload field.
	Budget model.PolicyResourceBudget
	Host   Host
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

type budgetEvaluationError struct {
	base      *EvaluationError
	dimension pluginsdk.BudgetDimension
}

func (e *budgetEvaluationError) Error() string {
	if e == nil || e.base == nil {
		return ""
	}
	return e.base.Error()
}

func (e *budgetEvaluationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.base
}

func (e *budgetEvaluationError) BudgetDimension() pluginsdk.BudgetDimension {
	if e == nil {
		return ""
	}
	return e.dimension
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
	dimension := budgetDimensionFrom(code, err)
	if dimension == "" {
		return &EvaluationError{Kind: FailureBudget, Code: code, Err: err}
	}
	return BudgetErrorFor(dimension, code, err)
}

func BudgetErrorFor(dimension pluginsdk.BudgetDimension, code string, err error) error {
	return &budgetEvaluationError{
		base:      &EvaluationError{Kind: FailureBudget, Code: code, Err: err},
		dimension: dimension,
	}
}

func budgetDimensionFrom(code string, err error) pluginsdk.BudgetDimension {
	var dimensionError pluginsdk.BudgetDimensionError
	if errors.As(err, &dimensionError) {
		return dimensionError.BudgetDimension()
	}
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "input-budget", "input_budget_exceeded":
		return pluginsdk.BudgetDimensionInput
	case "output-budget", "output_budget_exceeded":
		return pluginsdk.BudgetDimensionOutput
	case "memory-budget", "memory_budget_exceeded":
		return pluginsdk.BudgetDimensionMemory
	case "concurrency-budget", "concurrency_budget_exceeded":
		return pluginsdk.BudgetDimensionConcurrency
	case "deadline", "deadline_exceeded":
		return pluginsdk.BudgetDimensionDeadline
	case "state-key-budget", "state-value-budget", "state-capacity":
		return pluginsdk.BudgetDimensionState
	default:
		return ""
	}
}

func PolicyResourceBudgetContract(budget model.PolicyResourceBudget) pluginsdk.PolicyV1ResourceBudget {
	return pluginsdk.PolicyV1ResourceBudget{
		TimeoutMilliseconds: budget.TimeoutMS,
		MemoryBytes:         budget.MemoryBytes,
		Concurrency:         budget.Concurrency,
		InputFrameBytes:     budget.InputBytes,
		OutputFrameBytes:    budget.OutputBytes,
	}
}

func ValidatePolicyResourceBudget(budget model.PolicyResourceBudget) error {
	return PolicyResourceBudgetContract(budget).Validate()
}

func AdmitPolicyInputFrame(budget model.PolicyResourceBudget, frameBytes int) error {
	if frameBytes < 0 || int64(frameBytes) > budget.InputBytes {
		return BudgetErrorFor(pluginsdk.BudgetDimensionInput, "input-budget", errors.New("policy input protobuf frame exceeds its budget"))
	}
	return nil
}

func AdmitPolicyOutputFrame(budget model.PolicyResourceBudget, frameBytes int) error {
	if frameBytes < 0 || int64(frameBytes) > budget.OutputBytes {
		return BudgetErrorFor(pluginsdk.BudgetDimensionOutput, "output-budget", errors.New("policy output protobuf frame exceeds its budget"))
	}
	return nil
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
