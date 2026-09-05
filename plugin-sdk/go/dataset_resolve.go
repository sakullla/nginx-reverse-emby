package pluginsdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"
)

const (
	HostRuntimeDatasetResolve       = "dataset.resolve"
	DatasetResolveMaxFrameBytes     = 4096
	DatasetResolveMaxDurationMicros = 2000
)

// DatasetResolveRequest selects only an already prepared source binding in the
// authenticated caller's generation. It cannot select global latest, another
// generation, an administrator's desired version, or an arbitrary digest.
// Missing bindings fail; resolution never downloads, activates, or edits Config.
type DatasetResolveRequest struct {
	SourceID string `json:"source_id"`
}

func (request DatasetResolveRequest) Validate() error {
	return ValidatePolicyIdentity(request.SourceID)
}

// DatasetResolveBinding is Host-authenticated invocation identity, never parsed
// from the resolve request. It can also be used to verify a returned reference
// when the client knows its lifecycle identity.
type DatasetResolveBinding struct{ InstanceID, Generation string }

func (binding DatasetResolveBinding) Validate() error {
	if ValidatePolicyIdentity(binding.InstanceID) != nil || ValidatePolicyIdentity(binding.Generation) != nil {
		return errors.New("dataset resolve caller binding is invalid")
	}
	return nil
}

type DatasetResolveAuthorization struct {
	Binding        DatasetResolveBinding
	DeclaredScopes []string
	GrantedScopes  []string
}

// DatasetResolveHost resolves exact Host-owned instance/generation/source table
// state. It must recheck source grants and revocation, reject missing bindings,
// and reuse the same reference for repeated resolves in one generation. Old and
// new generations may legitimately return different immutable version digests.
// Implementations must honor ctx and perform only bounded local registry work.
type DatasetResolveHost interface {
	ResolveDataset(context.Context, DatasetResolveBinding, DatasetResolveRequest) (DatasetReference, error)
}

func ValidateDatasetResolvedReference(request DatasetResolveRequest, reference DatasetReference, binding DatasetResolveBinding) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if err := binding.Validate(); err != nil {
		return err
	}
	if err := reference.ValidateFor(binding.InstanceID, binding.Generation); err != nil {
		return err
	}
	if reference.SourceID != request.SourceID {
		return errors.New("resolved dataset belongs to another source")
	}
	return nil
}

// DecodeDatasetResolveRequest bounds the JSON payload and rejects repeated,
// unknown, wrongly-cased, or trailing fields rather than guessing selectors.
func DecodeDatasetResolveRequest(payload json.RawMessage) (DatasetResolveRequest, error) {
	var request DatasetResolveRequest
	if len(payload) > DatasetResolveMaxFrameBytes {
		return request, &RuntimeError{Code: ErrorResourceExhausted, Message: "dataset resolve request exceeds frame limit"}
	}
	invalid := func() (DatasetResolveRequest, error) {
		return DatasetResolveRequest{}, &RuntimeError{Code: ErrorInvalidArgument, Message: "dataset resolve requires only source_id"}
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return invalid()
	}
	seen := false
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil || key != "source_id" || seen {
			return invalid()
		}
		seen = true
		if decoder.Decode(&request.SourceID) != nil {
			return invalid()
		}
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim('}') || !seen {
		return invalid()
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return invalid()
	}
	if request.Validate() != nil {
		return invalid()
	}
	return request, nil
}

// CallDatasetResolveHost is the RPC Host boundary after private-transport
// authentication. It uses a fixed 2 ms ceiling and validates the returned
// reference against the actual caller, not any plugin-supplied identity.
func CallDatasetResolveHost(ctx context.Context, host DatasetResolveHost, authorization DatasetResolveAuthorization, payload json.RawMessage) (DatasetReference, error) {
	ctx, cancel := context.WithTimeout(ctx, DatasetResolveMaxDurationMicros*time.Microsecond)
	defer cancel()
	request, err := DecodeDatasetResolveRequest(payload)
	if err != nil {
		return DatasetReference{}, err
	}
	reference, err := resolveDatasetForCaller(ctx, host, authorization, request)
	if err != nil {
		return DatasetReference{}, err
	}
	encoded, err := json.Marshal(reference)
	if err != nil {
		return DatasetReference{}, err
	}
	complete, err := json.Marshal(HostRuntimeResponse{Payload: encoded})
	if err != nil || len(complete) > DatasetResolveMaxFrameBytes {
		return DatasetReference{}, &RuntimeError{Code: ErrorResourceExhausted, Message: "dataset resolve response exceeds frame limit"}
	}
	if err := ctx.Err(); err != nil {
		return DatasetReference{}, err
	}
	return reference, nil
}

func resolveDatasetForCaller(ctx context.Context, host DatasetResolveHost, authorization DatasetResolveAuthorization, request DatasetResolveRequest) (DatasetReference, error) {
	if authorization.Binding.Validate() != nil {
		return DatasetReference{}, &RuntimeError{Code: ErrorPermissionDenied, Message: "dataset resolve caller binding is invalid"}
	}
	for _, capability := range []HostCapability{CapabilityDatasetResolve, CapabilityDatasetQuery} {
		if ValidateHostCapabilityGrant(capability, authorization.DeclaredScopes, authorization.GrantedScopes) != nil {
			return DatasetReference{}, &RuntimeError{Code: ErrorPermissionDenied, Message: "dataset resolve capability denied"}
		}
	}
	if err := ctx.Err(); err != nil {
		return DatasetReference{}, err
	}
	if host == nil {
		return DatasetReference{}, &RuntimeError{Code: ErrorUnavailable, Message: "dataset resolver is unavailable"}
	}
	reference, err := host.ResolveDataset(ctx, authorization.Binding, request)
	if err != nil {
		return DatasetReference{}, err
	}
	if err := ctx.Err(); err != nil {
		return DatasetReference{}, err
	}
	if ValidateDatasetResolvedReference(request, reference, authorization.Binding) != nil {
		return DatasetReference{}, &RuntimeError{Code: ErrorPermissionDenied, Message: "dataset resolver returned a foreign or invalid reference"}
	}
	return reference, nil
}

// ResolveDataset uses the authenticated private HostRuntime transport. Source
// and reference syntax are checked here; Host must enforce instance/generation
// identity via CallDatasetResolveHost. Callers knowing their binding may also
// use ValidateDatasetResolvedReference. OpenDataset keeps exact-digest semantics.
func (client *HostRuntimeClient) ResolveDataset(ctx context.Context, request DatasetResolveRequest) (DatasetReference, error) {
	if err := request.Validate(); err != nil {
		return DatasetReference{}, err
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return DatasetReference{}, err
	}
	call := HostRuntimeCall{Operation: HostRuntimeDatasetResolve, Payload: payload}
	complete, err := json.Marshal(call)
	if err != nil || len(complete) > DatasetResolveMaxFrameBytes {
		return DatasetReference{}, &RuntimeError{Code: ErrorResourceExhausted, Message: "dataset resolve request exceeds frame limit"}
	}
	var raw json.RawMessage
	if err := client.Call(ctx, call, &raw); err != nil {
		return DatasetReference{}, err
	}
	complete, err = json.Marshal(HostRuntimeResponse{Payload: raw})
	if err != nil || len(complete) > DatasetResolveMaxFrameBytes {
		return DatasetReference{}, &RuntimeError{Code: ErrorResourceExhausted, Message: "dataset resolve response exceeds frame limit"}
	}
	var reference DatasetReference
	if err := decodeDatasetResolveReference(raw, &reference); err != nil {
		return DatasetReference{}, err
	}
	if reference.Validate() != nil || reference.SourceID != request.SourceID {
		return DatasetReference{}, errors.New("resolved dataset response differs from requested source")
	}
	return reference, nil
}

func decodeDatasetResolveReference(raw []byte, reference *DatasetReference) error {
	invalid := errors.New("dataset resolve reference JSON is malformed or ambiguous")
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return invalid
	}
	fields := map[string]*string{"handle": &reference.Handle, "instance_id": &reference.InstanceID, "generation": &reference.Generation, "source_id": &reference.SourceID, "version_digest": &reference.VersionDigest}
	seen := map[string]bool{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return invalid
		}
		key, ok := token.(string)
		if !ok || fields[key] == nil || seen[key] {
			return invalid
		}
		seen[key] = true
		if decoder.Decode(fields[key]) != nil {
			return invalid
		}
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim('}') || len(seen) != len(fields) {
		return invalid
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return invalid
	}
	return nil
}
