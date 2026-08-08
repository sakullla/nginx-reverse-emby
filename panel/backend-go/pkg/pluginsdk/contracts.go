// Package pluginsdk owns the stable contracts shared by plugin producers and
// the control-plane and Agent hosts. Runtime implementations may change
// without changing these identifiers or wire semantics.
package pluginsdk

import "context"

const (
	PolicyABIV1 = "nre:policy/v1"
	RPCABIV1    = "nre:rpc/v1"

	RuntimeWASMPolicy = "wasm-policy"
	RuntimeRPCService = "rpc-service"

	HostScopeAgent        = "agent"
	HostScopeControlPlane = "control-plane"
)

const (
	PolicyExportVersion  = "nre_policy_version"
	PolicyExportAllocate = "nre_policy_alloc"
	PolicyExportFree     = "nre_policy_free"
	PolicyExportInit     = "nre_policy_init"
	PolicyExportEvaluate = "nre_policy_evaluate"
	PolicyExportReset    = "nre_policy_reset"
	PolicyExportMemory   = "memory"

	PolicyHostModule         = PolicyABIV1
	PolicyHostReadField      = "nre_host_read_field"
	PolicyHostReadBodyWindow = "nre_host_read_body_window"
	PolicyHostStateGet       = "nre_host_state_get"
	PolicyHostStatePut       = "nre_host_state_put"
	PolicyHostEmitEvent      = "nre_host_emit_event"
	PolicyHostAddMetric      = "nre_host_add_metric"

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
	result := make(map[string]WASMFunctionSignature, 6)
	for _, name := range []string{
		PolicyHostReadField,
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
		return ""
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

type ErrorCode string

const (
	ErrorInvalidArgument   ErrorCode = "invalid_argument"
	ErrorPermissionDenied  ErrorCode = "permission_denied"
	ErrorResourceExhausted ErrorCode = "resource_exhausted"
	ErrorDeadlineExceeded  ErrorCode = "deadline_exceeded"
	ErrorUnavailable       ErrorCode = "unavailable"
	ErrorIncompatibleABI   ErrorCode = "incompatible_abi"
	ErrorInternal          ErrorCode = "internal"
)

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
	return string(e.Code) + ": " + e.Message
}

type PolicyInput struct {
	ExtensionPoint string
	RequestID      string
	Payload        []byte
}

type PolicyOutput struct {
	Action  string
	Payload []byte
}

// PolicyHost is the complete nre:policy/v1 host surface. It intentionally has
// no filesystem, network, process, wall-clock, or raw host-memory operation.
type PolicyHost interface {
	ReadField(context.Context, string) ([]byte, error)
	ReadBodyWindow(context.Context, uint32, uint32) ([]byte, error)
	StateGet(context.Context, string) ([]byte, bool, error)
	StatePut(context.Context, string, []byte) error
	EmitEvent(context.Context, string, []byte) error
	AddMetric(context.Context, string, int64) error
}

type RPCHandshakeRequest struct {
	ABI            string
	PluginID       string
	PluginVersion  string
	PackageDigest  string
	ArtifactDigest string
	GrantedScopes  []string
	Generation     string
}

type RPCHandshakeResponse struct {
	ABI          string
	Capabilities []string
}

type LifecycleRequest struct {
	Generation string
	Config     []byte
}

type LifecycleResponse struct {
	Ready bool
	Error *RuntimeError
}
