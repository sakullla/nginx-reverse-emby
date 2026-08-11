// Package pluginsdk owns the stable contracts shared by plugin producers and
// the control-plane and Agent hosts. Runtime implementations may change
// without changing these identifiers or wire semantics.
package pluginsdk

import (
	"context"
	"errors"
	"fmt"
)

const (
	PolicyABIV1 = "nre:policy/v1"
	RPCABIV1    = "nre:rpc/v1"

	RuntimeWASMPolicy = "wasm-policy"
	RuntimeRPCService = "rpc-service"

	HostScopeAgent        = "agent"
	HostScopeControlPlane = "control-plane"
)

// Policy v1 resource budgets are host-independent ABI limits. InputBytes and
// OutputBytes always count the complete deterministic protobuf wire frame,
// including field tags, length prefixes, nested result envelopes, and all
// host-added metadata. They never mean only an application payload field. The
// minimum input is the six-byte smallest EvaluateRequest with non-empty
// extension_point and request_id; the minimum output is the four-byte smallest
// EvaluateResponse success envelope with a valid non-zero action.
const (
	PolicyV1MaxTimeoutMilliseconds int64 = 2
	PolicyV1MinMemoryBytes         int64 = 1 << 16
	PolicyV1MaxMemoryBytes         int64 = 16 << 20
	PolicyV1MaxConcurrency               = 64
	PolicyV1MinInputFrameBytes     int64 = 6
	PolicyV1MaxInputFrameBytes     int64 = 128 << 10
	PolicyV1MinOutputFrameBytes    int64 = 4
	PolicyV1MaxOutputFrameBytes    int64 = 4 << 10
)

// PolicyV1ResourceBudget is the canonical admission shape for a wasm-policy
// manifest. Hosts can project their manifest/model types into this value and
// thereby share one set of policy ceilings and wire-frame minimums.
type PolicyV1ResourceBudget struct {
	TimeoutMilliseconds int64
	MemoryBytes         int64
	Concurrency         int
	InputFrameBytes     int64
	OutputFrameBytes    int64
}

func (budget PolicyV1ResourceBudget) Validate() error {
	if budget.TimeoutMilliseconds <= 0 || budget.TimeoutMilliseconds > PolicyV1MaxTimeoutMilliseconds {
		return fmt.Errorf("policy timeout_ms must be within 1..%d", PolicyV1MaxTimeoutMilliseconds)
	}
	if budget.MemoryBytes < PolicyV1MinMemoryBytes || budget.MemoryBytes > PolicyV1MaxMemoryBytes {
		return fmt.Errorf("policy memory_bytes must be within %d..%d", PolicyV1MinMemoryBytes, PolicyV1MaxMemoryBytes)
	}
	if budget.Concurrency <= 0 || budget.Concurrency > PolicyV1MaxConcurrency {
		return fmt.Errorf("policy concurrency must be within 1..%d", PolicyV1MaxConcurrency)
	}
	if budget.InputFrameBytes < PolicyV1MinInputFrameBytes || budget.InputFrameBytes > PolicyV1MaxInputFrameBytes {
		return fmt.Errorf("policy input_bytes must be a complete protobuf wire-frame budget within %d..%d", PolicyV1MinInputFrameBytes, PolicyV1MaxInputFrameBytes)
	}
	if budget.OutputFrameBytes < PolicyV1MinOutputFrameBytes || budget.OutputFrameBytes > PolicyV1MaxOutputFrameBytes {
		return fmt.Errorf("policy output_bytes must be a complete protobuf wire-frame budget within %d..%d", PolicyV1MinOutputFrameBytes, PolicyV1MaxOutputFrameBytes)
	}
	return nil
}

// BudgetDimension classifies resource exhaustion without changing the stable
// PolicyStatus or protobuf RuntimeError wire values.
type BudgetDimension string

const (
	BudgetDimensionInput       BudgetDimension = "input"
	BudgetDimensionOutput      BudgetDimension = "output"
	BudgetDimensionMemory      BudgetDimension = "memory"
	BudgetDimensionConcurrency BudgetDimension = "concurrency"
	BudgetDimensionDeadline    BudgetDimension = "deadline"
	BudgetDimensionState       BudgetDimension = "state"
)

// BudgetDimensionError is implemented by host-side errors that retain the
// exhausted resource across package boundaries.
type BudgetDimensionError interface {
	error
	BudgetDimension() BudgetDimension
}

const (
	PolicyExportVersion  = "nre_policy_version"
	PolicyExportAllocate = "nre_policy_alloc"
	PolicyExportFree     = "nre_policy_free"
	PolicyExportInit     = "nre_policy_init"
	PolicyExportEvaluate = "nre_policy_evaluate"
	PolicyExportReset    = "nre_policy_reset"
	PolicyExportMemory   = "memory"

	PolicyHostModule             = PolicyABIV1
	PolicyHostReadField          = "nre_host_read_field"
	PolicyHostReadNormalizedHTTP = "nre_host_read_normalized_http"
	PolicyHostReadBodyWindow     = "nre_host_read_body_window"
	PolicyHostStateGet           = "nre_host_state_get"
	PolicyHostStatePut           = "nre_host_state_put"
	PolicyHostEmitEvent          = "nre_host_emit_event"
	PolicyHostAddMetric          = "nre_host_add_metric"

	PolicyABIMajorVersion uint32 = 1
)

// WASMValueType is a WebAssembly 1.0 numeric value type used by the policy
// ABI. The ABI deliberately has no externref, v128, multi-memory, or WASI
// dependency.
type WASMValueType byte

const (
	WASMI32 WASMValueType = 0x7f
	WASMI64 WASMValueType = 0x7e
)

type WASMFunctionSignature struct {
	Parameters []WASMValueType
	Results    []WASMValueType
}

// PolicyV1GuestFunctions returns the complete required guest export surface.
// A fresh map and slices are returned so callers cannot mutate the contract.
func PolicyV1GuestFunctions() map[string]WASMFunctionSignature {
	return map[string]WASMFunctionSignature{
		PolicyExportVersion:  wasmSignature(nil, []WASMValueType{WASMI32}),
		PolicyExportAllocate: wasmSignature([]WASMValueType{WASMI32}, []WASMValueType{WASMI32}),
		PolicyExportFree:     wasmSignature([]WASMValueType{WASMI32, WASMI32}, nil),
		PolicyExportInit:     wasmSignature([]WASMValueType{WASMI32, WASMI32}, []WASMValueType{WASMI32}),
		PolicyExportEvaluate: wasmSignature([]WASMValueType{WASMI32, WASMI32}, []WASMValueType{WASMI64}),
		PolicyExportReset:    wasmSignature(nil, []WASMValueType{WASMI32}),
	}
}

// PolicyV1HostFunctions returns the only imports allowed to a policy module.
// Every call uses protobuf request bytes and a caller-owned response buffer:
// (request_ptr, request_len, response_ptr, response_capacity) -> status/length.
func PolicyV1HostFunctions() map[string]WASMFunctionSignature {
	result := make(map[string]WASMFunctionSignature, 7)
	for _, name := range []string{
		PolicyHostReadField,
		PolicyHostReadNormalizedHTTP,
		PolicyHostReadBodyWindow,
		PolicyHostStateGet,
		PolicyHostStatePut,
		PolicyHostEmitEvent,
		PolicyHostAddMetric,
	} {
		result[name] = wasmSignature(
			[]WASMValueType{WASMI32, WASMI32, WASMI32, WASMI32},
			[]WASMValueType{WASMI64},
		)
	}
	return result
}

// PolicyV1RequiredHostFunctions returns the original policy/v1 imports which
// every guest must retain. Additive imports are intentionally absent so an
// updated host continues accepting already-published policy/v1 guests.
func PolicyV1RequiredHostFunctions() map[string]WASMFunctionSignature {
	result := PolicyV1HostFunctions()
	delete(result, PolicyHostReadNormalizedHTTP)
	return result
}

func wasmSignature(parameters, results []WASMValueType) WASMFunctionSignature {
	return WASMFunctionSignature{
		Parameters: append([]WASMValueType(nil), parameters...),
		Results:    append([]WASMValueType(nil), results...),
	}
}

// PolicyStatus is the stable numeric status used by init/reset and Host
// imports. Evaluate reports policy errors in EvaluateResponse.RuntimeError;
// traps and malformed frames are host failures.
type PolicyStatus uint32

const (
	PolicyStatusOK PolicyStatus = iota
	PolicyStatusInvalidArgument
	PolicyStatusPermissionDenied
	PolicyStatusResourceExhausted
	PolicyStatusDeadlineExceeded
	PolicyStatusUnavailable
	PolicyStatusIncompatibleABI
	PolicyStatusInternal
)

// PackPolicyBuffer packs an output pointer in the high 32 bits and its length
// in the low 32 bits, as returned by nre_policy_evaluate.
func PackPolicyBuffer(pointer, length uint32) uint64 {
	return uint64(pointer)<<32 | uint64(length)
}

func UnpackPolicyBuffer(value uint64) (pointer, length uint32) {
	return uint32(value >> 32), uint32(value)
}

// PackPolicyHostResult packs a Host import status in the high 32 bits and the
// written or required response length in the low 32 bits. A
// resource_exhausted status with length > capacity means retry with a larger
// caller-owned buffer; Host imports never allocate guest memory.
func PackPolicyHostResult(status PolicyStatus, length uint32) uint64 {
	return uint64(status)<<32 | uint64(length)
}

func UnpackPolicyHostResult(value uint64) (status PolicyStatus, length uint32) {
	return PolicyStatus(value >> 32), uint32(value)
}

// ErrorCode returns the protobuf RuntimeError code corresponding to a non-zero
// ABI status. OK has no error code and unknown values fail closed as internal.
func (status PolicyStatus) ErrorCode() ErrorCode {
	switch status {
	case PolicyStatusOK:
		return ErrorUnspecified
	case PolicyStatusInvalidArgument:
		return ErrorInvalidArgument
	case PolicyStatusPermissionDenied:
		return ErrorPermissionDenied
	case PolicyStatusResourceExhausted:
		return ErrorResourceExhausted
	case PolicyStatusDeadlineExceeded:
		return ErrorDeadlineExceeded
	case PolicyStatusUnavailable:
		return ErrorUnavailable
	case PolicyStatusIncompatibleABI:
		return ErrorIncompatibleABI
	case PolicyStatusInternal:
		return ErrorInternal
	default:
		return ErrorInternal
	}
}

// ErrorCode is the stable numeric RuntimeErrorCode enum defined identically by
// both v1 IDLs. Zero is deliberately invalid in a failure envelope.
type ErrorCode uint32

const (
	ErrorUnspecified ErrorCode = iota
	ErrorInvalidArgument
	ErrorPermissionDenied
	ErrorResourceExhausted
	ErrorDeadlineExceeded
	ErrorUnavailable
	ErrorIncompatibleABI
	ErrorInternal
)

func (code ErrorCode) Valid() bool {
	return code >= ErrorInvalidArgument && code <= ErrorInternal
}

func (code ErrorCode) String() string {
	switch code {
	case ErrorInvalidArgument:
		return "invalid_argument"
	case ErrorPermissionDenied:
		return "permission_denied"
	case ErrorResourceExhausted:
		return "resource_exhausted"
	case ErrorDeadlineExceeded:
		return "deadline_exceeded"
	case ErrorUnavailable:
		return "unavailable"
	case ErrorIncompatibleABI:
		return "incompatible_abi"
	case ErrorInternal:
		return "internal"
	case ErrorUnspecified:
		return "unspecified"
	default:
		return fmt.Sprintf("unknown(%d)", code)
	}
}

// RuntimeError is safe to cross the ABI. Message must not contain credentials
// or other secret material.
type RuntimeError struct {
	Code      ErrorCode
	Message   string
	Retryable bool
}

func (e *RuntimeError) Error() string {
	if e == nil {
		return ""
	}
	return e.Code.String() + ": " + e.Message
}

func (e *RuntimeError) Validate() error {
	if e == nil {
		return errors.New("runtime error is missing")
	}
	if !e.Code.Valid() {
		return fmt.Errorf("runtime error code %d is unspecified or unknown", e.Code)
	}
	return nil
}

type PolicyInput struct {
	ExtensionPoint string
	RequestID      string
	Payload        []byte
}

type PolicyOutput struct {
	Action  PolicyAction
	Payload []byte
}

// PolicyAction mirrors policy/v1 EvaluateSuccess.Action.
type PolicyAction uint32

const (
	PolicyActionUnspecified PolicyAction = iota
	PolicyActionAllow
	PolicyActionDeny
	PolicyActionObserve
)

func (action PolicyAction) Valid() bool {
	return action >= PolicyActionAllow && action <= PolicyActionObserve
}

// PolicyEvaluateResponse mirrors the policy IDL oneof. Exactly one member is
// required, and the success action must be one of the stable IDL values.
type PolicyEvaluateResponse struct {
	Success *PolicyOutput
	Error   *RuntimeError
}

func (response PolicyEvaluateResponse) Validate() error {
	if (response.Success == nil) == (response.Error == nil) {
		return errors.New("policy evaluate response must contain exactly one success or error result")
	}
	if response.Error != nil {
		return response.Error.Validate()
	}
	if !response.Success.Action.Valid() {
		return fmt.Errorf("policy action %d is unspecified or unknown", response.Success.Action)
	}
	return nil
}

// PolicyHost is the complete nre:policy/v1 host surface. It intentionally has
// no filesystem, network, process, wall-clock, or raw host-memory operation.
type PolicyHost interface {
	// ReadField returns nil for a missing field and a non-nil slice, including
	// an empty slice, for a present field.
	ReadField(context.Context, string) ([]byte, error)
	ReadBodyWindow(context.Context, uint32, uint32) ([]byte, error)
	StateGet(context.Context, string) ([]byte, bool, error)
	StatePut(context.Context, string, []byte) error
	EmitEvent(context.Context, PolicySecurityEvent) error
	AddMetric(context.Context, string, int64) error
}

// PolicyNormalizedHTTP is the fixed, request-scoped HTTP projection returned
// by the additive normalized HTTP host import. Headers is a deterministic
// lower-case name/value projection and must never be silently truncated.
type PolicyNormalizedHTTP struct {
	Path                       []byte
	Query                      []byte
	Headers                    []byte
	TrustedSource              []byte
	TrustedSourceAuthenticated bool
	BodyWindowComplete         bool
	BodyWindowLength           uint32
}

// PolicyNormalizedHTTPHost is optional for policy/v1 hosts so existing hosts
// and guests remain source- and binary-compatible with the original v1 ABI.
type PolicyNormalizedHTTPHost interface {
	ReadNormalizedHTTP(context.Context) (PolicyNormalizedHTTP, error)
}

type RPCHandshakeRequest struct {
	ABI              string
	PluginID         string
	PluginVersion    string
	PackageDigest    string
	ArtifactDigest   string
	GrantedScopes    []string
	Generation       string
	RequiredFeatures []string
}

type RPCHandshakeResponse struct {
	ABI          string
	Capabilities []string
	Features     []string
}

type LifecycleRequest struct {
	Generation string
	Config     []byte
}

type LifecycleSuccess struct {
	Ready bool
}

type RPCActionRequest struct {
	Generation      string
	ActionID        string
	TargetKind      string
	TargetID        string
	OperationID     string
	ResourceHandle  string
	ResourceResults []RPCResourceResult
}

func (request RPCActionRequest) Validate() error {
	for name, value := range map[string]string{"generation": request.Generation, "action": request.ActionID, "operation": request.OperationID} {
		if err := ValidatePolicyIdentity(value); err != nil {
			return fmt.Errorf("%s identity: %w", name, err)
		}
	}
	if request.ResourceHandle != "" {
		if request.TargetKind != "" || request.TargetID != "" {
			return errors.New("action request cannot combine an opaque resource handle with raw target identity")
		}
		if err := ValidatePolicyIdentity(request.ResourceHandle); err != nil {
			return fmt.Errorf("resource handle identity: %w", err)
		}
		return validateRPCResourceResults(request.ResourceResults)
	}
	if len(request.ResourceResults) != 0 {
		return errors.New("action request resource results require an opaque resource handle")
	}
	for name, value := range map[string]string{"target kind": request.TargetKind, "target": request.TargetID} {
		if err := ValidatePolicyIdentity(value); err != nil {
			return fmt.Errorf("%s identity: %w", name, err)
		}
	}
	return nil
}

type RPCResourceOperation int32

const (
	RPCResourceInspect        RPCResourceOperation = 1
	RPCResourceProbe          RPCResourceOperation = 2
	RPCResourceTrafficSummary RPCResourceOperation = 3
	RPCResourceDNSApply       RPCResourceOperation = 4
	RPCResourceDockerRequest  RPCResourceOperation = 5
)

const RPCResourcePayloadMaxBytes = 4096

type RPCResourceCall struct {
	RequestID      string
	ResourceHandle string
	Operation      RPCResourceOperation
	Input          []byte
}

func (call RPCResourceCall) Validate() error {
	if err := ValidatePolicyIdentity(call.RequestID); err != nil {
		return fmt.Errorf("resource call request identity: %w", err)
	}
	if err := ValidatePolicyIdentity(call.ResourceHandle); err != nil {
		return fmt.Errorf("resource call handle identity: %w", err)
	}
	if call.Operation < RPCResourceInspect || call.Operation > RPCResourceDockerRequest {
		return errors.New("resource call operation is unspecified or unknown")
	}
	if len(call.Input) > RPCResourcePayloadMaxBytes {
		return errors.New("resource call input exceeds the canonical bound")
	}
	return nil
}

type RPCResourceResult struct {
	RequestID string
	Value     []byte
	Error     *RuntimeError
}

func (result RPCResourceResult) Validate() error {
	if err := ValidatePolicyIdentity(result.RequestID); err != nil {
		return fmt.Errorf("resource result request identity: %w", err)
	}
	if (result.Error == nil) == (result.Value == nil) {
		return errors.New("resource result must contain exactly one value or error")
	}
	if len(result.Value) > RPCResourcePayloadMaxBytes {
		return errors.New("resource result value exceeds the canonical bound")
	}
	if result.Error != nil {
		return result.Error.Validate()
	}
	return nil
}

type RPCActionPlanResponse struct {
	Calls []RPCResourceCall
	Error *RuntimeError
}

func (response RPCActionPlanResponse) Validate() error {
	if response.Error != nil {
		if len(response.Calls) != 0 {
			return errors.New("action plan cannot combine calls with an error")
		}
		return response.Error.Validate()
	}
	if len(response.Calls) > 16 {
		return errors.New("action plan exceeds the canonical call bound")
	}
	seen := make(map[string]struct{}, len(response.Calls))
	for _, call := range response.Calls {
		if err := call.Validate(); err != nil {
			return err
		}
		if _, exists := seen[call.RequestID]; exists {
			return errors.New("action plan contains a duplicate request identity")
		}
		seen[call.RequestID] = struct{}{}
	}
	return nil
}

func validateRPCResourceResults(results []RPCResourceResult) error {
	seen := make(map[string]struct{}, len(results))
	for _, result := range results {
		if err := result.Validate(); err != nil {
			return err
		}
		if _, exists := seen[result.RequestID]; exists {
			return errors.New("action request contains a duplicate resource result")
		}
		seen[result.RequestID] = struct{}{}
	}
	return nil
}

type RPCActionQueryRequest struct {
	Generation  string
	OperationID string
}

func (request RPCActionQueryRequest) Validate() error {
	for name, value := range map[string]string{"generation": request.Generation, "operation": request.OperationID} {
		if err := ValidatePolicyIdentity(value); err != nil {
			return fmt.Errorf("action query %s identity: %w", name, err)
		}
	}
	return nil
}

type RPCActionResponse struct {
	Accepted    bool
	OperationID string
	Error       *RuntimeError
	Pending     bool
	Missing     bool
}

func (response RPCActionResponse) Validate() error {
	if err := ValidatePolicyIdentity(response.OperationID); err != nil {
		return fmt.Errorf("action response operation identity: %w", err)
	}
	branches := 0
	if response.Accepted {
		branches++
	}
	if response.Error != nil {
		branches++
	}
	if response.Pending {
		branches++
	}
	if response.Missing {
		branches++
	}
	if branches != 1 {
		return errors.New("action response must contain exactly one success, error, pending, or missing result")
	}
	if response.Error != nil {
		return response.Error.Validate()
	}
	return nil
}

// LifecycleResponse mirrors the RPC IDL oneof. A success is only actionable
// when ready=true; false readiness and missing/conflicting results fail closed.
type LifecycleResponse struct {
	Success *LifecycleSuccess
	Error   *RuntimeError
}

func (response LifecycleResponse) Validate() error {
	if (response.Success == nil) == (response.Error == nil) {
		return errors.New("lifecycle response must contain exactly one success or error result")
	}
	if response.Error != nil {
		return response.Error.Validate()
	}
	if !response.Success.Ready {
		return errors.New("lifecycle success is not ready")
	}
	return nil
}
