package pluginsdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
)

const (
	HostRuntimeScopedSecret                    = "secret.scoped"
	PermissionScopedSecretRead                 = "secret.scoped.read"
	PermissionScopedSecretWrite                = "secret.scoped.write"
	CapabilityScopedSecretRead  HostCapability = PermissionScopedSecretRead
	CapabilityScopedSecretWrite HostCapability = PermissionScopedSecretWrite
	ManagedSecretMaxBytes                      = 16 << 10
	ScopedSecretCreate                         = "create"
	ScopedSecretRead                           = "read"
	ScopedSecretRotate                         = "rotate"
	ScopedSecretRevoke                         = "revoke"
)

// ManagedSecretMaterial has no ordinary JSON representation. Only the explicit
// scoped-secret transport encoders can deliver bytes; ordinary state serialization
// fails. Formatting and structured logging redact all material. Close wipes the
// owned copy. Bind Close to the consuming generation's revocation lifecycle.
type ManagedSecretMaterial struct {
	state *managedSecretState
}

type managedSecretState struct {
	mu    sync.Mutex
	value []byte
}

func NewManagedSecretMaterial(value []byte) (*ManagedSecretMaterial, error) {
	if len(value) == 0 || len(value) > ManagedSecretMaxBytes {
		return nil, errors.New("scoped secret material size is invalid")
	}
	return &ManagedSecretMaterial{state: &managedSecretState{value: append([]byte(nil), value...)}}, nil
}

func (material ManagedSecretMaterial) String() string   { return "[REDACTED]" }
func (material ManagedSecretMaterial) GoString() string { return "[REDACTED]" }
func (material ManagedSecretMaterial) Format(state fmt.State, verb rune) {
	_, _ = state.Write([]byte("[REDACTED]"))
}
func (material ManagedSecretMaterial) LogValue() slog.Value { return slog.StringValue("[REDACTED]") }
func (material ManagedSecretMaterial) MarshalJSON() ([]byte, error) {
	return nil, errors.New("secret material requires explicit scoped delivery")
}

// WithBytes supplies a transient copy, wiped when the callback returns. The
// caller must not retain or log this copy, nor return errors containing it.
func (material *ManagedSecretMaterial) WithBytes(call func([]byte) error) error {
	if material == nil || material.state == nil || call == nil {
		return errors.New("scoped secret material is unavailable")
	}
	material.state.mu.Lock()
	if len(material.state.value) == 0 {
		material.state.mu.Unlock()
		return errors.New("scoped secret material is revoked")
	}
	value := append([]byte(nil), material.state.value...)
	material.state.mu.Unlock()
	defer wipeManagedSecret(value)
	return call(value)
}

func (material *ManagedSecretMaterial) Close() {
	if material == nil || material.state == nil {
		return
	}
	material.state.mu.Lock()
	defer material.state.mu.Unlock()
	wipeManagedSecret(material.state.value)
	material.state.value = nil
}

func wipeManagedSecret(value []byte) {
	for i := range value {
		value[i] = 0
	}
}

func (material *ManagedSecretMaterial) validate() error {
	return material.WithBytes(func(value []byte) error {
		if len(value) == 0 || len(value) > ManagedSecretMaxBytes {
			return errors.New("scoped secret material size is invalid")
		}
		return nil
	})
}

// ScopedSecretReference is safe to persist. Version is immutable and explicitly
// selected: reads never silently fall back to another version or ordinary state.
// Scope identifies the granted purpose, independently of the reference name.
type ScopedSecretReference struct {
	InstanceID string `json:"instance_id"`
	ID         string `json:"id"`
	Version    string `json:"version"`
	Scope      string `json:"scope"`
}

func (reference ScopedSecretReference) Validate() error { return reference.validate(false) }
func (reference ScopedSecretReference) validate(creating bool) error {
	for _, value := range []string{reference.InstanceID, reference.ID, reference.Scope} {
		if ValidatePolicyIdentity(value) != nil {
			return errors.New("scoped secret reference is invalid")
		}
	}
	if creating {
		if reference.Version != "" {
			return errors.New("new secret must not select an existing version")
		}
	} else if !validManagedToken(reference.Version) {
		return errors.New("scoped secret version is invalid")
	}
	return nil
}

// ScopedSecretRequest is authenticated through HostRuntime. Create imports new
// material into secure Host storage. Rotate atomically replaces the exact old
// version and revokes old deliveries before acknowledging the new version;
// concurrent stale rotates fail. Revoke invalidates this exact version and all
// deliveries. Read requires an explicit scope grant and live generation.
// No operation accepts secret material in an ordinary state/config fallback.
type ScopedSecretRequest struct {
	Action    string                 `json:"action"`
	Binding   ManagedBinding         `json:"binding"`
	Reference ScopedSecretReference  `json:"reference"`
	Material  *ManagedSecretMaterial `json:"material,omitempty"`
}

func (request ScopedSecretRequest) Validate() error {
	if err := request.Binding.Validate(); err != nil {
		return err
	}
	if err := request.Reference.validate(request.Action == ScopedSecretCreate); err != nil {
		return err
	}
	if request.Reference.InstanceID != request.Binding.InstanceID {
		return errors.New("scoped secret instance binding mismatch")
	}
	switch request.Action {
	case ScopedSecretCreate, ScopedSecretRotate:
		return request.Material.validate()
	case ScopedSecretRead, ScopedSecretRevoke:
		if request.Material != nil {
			return errors.New("scoped secret action does not accept material")
		}
		return nil
	default:
		return errors.New("scoped secret action is unsupported")
	}
}

type ScopedSecretResponse struct {
	Reference ScopedSecretReference  `json:"reference"`
	Material  *ManagedSecretMaterial `json:"material,omitempty"`
	Revoked   bool                   `json:"revoked,omitempty"`
}

func (response ScopedSecretResponse) ValidateFor(request ScopedSecretRequest) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if err := response.Reference.Validate(); err != nil {
		return err
	}
	if response.Reference.ID != request.Reference.ID || response.Reference.InstanceID != request.Reference.InstanceID || response.Reference.Scope != request.Reference.Scope {
		return errors.New("scoped secret response binding mismatch")
	}
	switch request.Action {
	case ScopedSecretCreate, ScopedSecretRotate:
		if response.Reference.Version == request.Reference.Version || response.Material != nil || response.Revoked {
			return errors.New("scoped secret replacement response is invalid")
		}
	case ScopedSecretRead:
		if response.Reference != request.Reference || response.Revoked {
			return errors.New("scoped secret delivery version mismatch")
		}
		return response.Material.validate()
	case ScopedSecretRevoke:
		if response.Reference != request.Reference || !response.Revoked || response.Material != nil {
			return errors.New("scoped secret revocation response is invalid")
		}
	}
	return nil
}

// ScopedSecretRecord is trusted Host state. Active includes version revocation;
// caller must independently come from a live authenticated generation. Scope
// grants below are exact purpose IDs from the administrator's authorization.
type ScopedSecretRecord struct {
	Reference ScopedSecretReference
	Active    bool
}

func ValidateScopedSecretBinding(request ScopedSecretRequest, caller ManagedBinding, record *ScopedSecretRecord, grants, scopes []string) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if caller.Validate() != nil || request.Binding != caller {
		return errors.New("scoped secret caller binding mismatch")
	}
	permission := PermissionScopedSecretWrite
	if request.Action == ScopedSecretRead {
		permission = PermissionScopedSecretRead
	}
	if !hasManagedGrant(grants, permission) || !hasManagedGrant(scopes, request.Reference.Scope) {
		return errors.New("scoped secret permission is missing")
	}
	if request.Action == ScopedSecretCreate {
		if record != nil {
			return errors.New("scoped secret already exists")
		}
	} else if record == nil || !record.Active || record.Reference != request.Reference {
		return errors.New("scoped secret version is unknown or revoked")
	}
	return nil
}

type scopedSecretRequestWire struct {
	Action    string                `json:"action"`
	Binding   ManagedBinding        `json:"binding"`
	Reference ScopedSecretReference `json:"reference"`
	Material  []byte                `json:"material,omitempty"`
}
type scopedSecretResponseWire struct {
	Reference ScopedSecretReference `json:"reference"`
	Material  []byte                `json:"material,omitempty"`
	Revoked   bool                  `json:"revoked,omitempty"`
}

// EncodeScopedSecretRequest is exclusively for the authenticated private Host
// transport. Its result contains material and must never be logged or persisted.
// The caller owns wiping the returned bytes after transport completion.
func EncodeScopedSecretRequest(request ScopedSecretRequest) (json.RawMessage, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	wire := scopedSecretRequestWire{Action: request.Action, Binding: request.Binding, Reference: request.Reference}
	if request.Material == nil {
		return json.Marshal(wire)
	}
	var encoded []byte
	err := request.Material.WithBytes(func(value []byte) error {
		wire.Material = value
		var err error
		encoded, err = json.Marshal(wire)
		return err
	})
	return encoded, err
}

func DecodeScopedSecretRequest(payload json.RawMessage) (ScopedSecretRequest, error) {
	var wire scopedSecretRequestWire
	if err := decodeManagedPayload(payload, &wire); err != nil {
		wipeManagedSecret(wire.Material)
		return ScopedSecretRequest{}, err
	}
	defer wipeManagedSecret(wire.Material)
	request := ScopedSecretRequest{Action: wire.Action, Binding: wire.Binding, Reference: wire.Reference}
	if wire.Material != nil {
		var err error
		request.Material, err = NewManagedSecretMaterial(wire.Material)
		if err != nil {
			return ScopedSecretRequest{}, err
		}
	}
	if err := request.Validate(); err != nil {
		request.Material.Close()
		return ScopedSecretRequest{}, err
	}
	return request, nil
}

// EncodeScopedSecretResponse has the same private-transport-only and wiping
// obligations as EncodeScopedSecretRequest. The Host must authorize first.
func EncodeScopedSecretResponse(request ScopedSecretRequest, response ScopedSecretResponse) (json.RawMessage, error) {
	if err := response.ValidateFor(request); err != nil {
		return nil, err
	}
	wire := scopedSecretResponseWire{Reference: response.Reference, Revoked: response.Revoked}
	if response.Material == nil {
		return json.Marshal(wire)
	}
	var encoded []byte
	err := response.Material.WithBytes(func(value []byte) error {
		wire.Material = value
		var err error
		encoded, err = json.Marshal(wire)
		return err
	})
	return encoded, err
}

func DecodeScopedSecretResponse(request ScopedSecretRequest, payload json.RawMessage) (ScopedSecretResponse, error) {
	var wire scopedSecretResponseWire
	if err := decodeManagedPayload(payload, &wire); err != nil {
		wipeManagedSecret(wire.Material)
		return ScopedSecretResponse{}, err
	}
	defer wipeManagedSecret(wire.Material)
	response := ScopedSecretResponse{Reference: wire.Reference, Revoked: wire.Revoked}
	if wire.Material != nil {
		var err error
		response.Material, err = NewManagedSecretMaterial(wire.Material)
		if err != nil {
			return ScopedSecretResponse{}, err
		}
	}
	if err := response.ValidateFor(request); err != nil {
		response.Material.Close()
		return ScopedSecretResponse{}, err
	}
	return response, nil
}

func (client *HostRuntimeClient) ScopedSecret(ctx context.Context, request ScopedSecretRequest) (ScopedSecretResponse, error) {
	payload, err := EncodeScopedSecretRequest(request)
	if err != nil {
		return ScopedSecretResponse{}, err
	}
	defer wipeManagedSecret(payload)
	var result json.RawMessage
	if err := client.Call(ctx, HostRuntimeCall{Operation: HostRuntimeScopedSecret, Payload: payload}, &result); err != nil {
		// A faulty peer might echo credentials in a failure message. Preserve
		// stable error classification but never pass its free text to callers.
		var runtimeError *RuntimeError
		if errors.As(err, &runtimeError) {
			safe := *runtimeError
			safe.Message = "scoped secret operation failed"
			return ScopedSecretResponse{}, &safe
		}
		if ctx.Err() != nil {
			return ScopedSecretResponse{}, ctx.Err()
		}
		return ScopedSecretResponse{}, errors.New("scoped secret transport failed")
	}
	defer wipeManagedSecret(result)
	return DecodeScopedSecretResponse(request, result)
}
