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
	PolicyExportInit     = "nre_policy_init"
	PolicyExportEvaluate = "nre_policy_evaluate"
	PolicyExportReset    = "nre_policy_reset"
)

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
