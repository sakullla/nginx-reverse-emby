package rpc

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/generation"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	managed "github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/network"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/policy"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

type ScopedSecretRedeemer interface {
	RedeemScopedPluginSecret(context.Context, model.PluginSecretRedemptionRequest) (json.RawMessage, error)
}
type NetworkSessionRegistrar interface {
	RegisterSession(string, generation.EntityKey, string, generation.Session) (*generation.SessionHandle, error)
}

type runtimeServices struct {
	datasets  *policy.DatasetGeneration
	evaluator policy.Evaluator
	policy    *model.PolicyRef
}

func (h *Host) SetNetworkSessionRegistrar(registrar NetworkSessionRegistrar) {
	h.mu.Lock()
	h.networkSessions = registrar
	h.mu.Unlock()
}
func managedRuntimeNeeded(candidate HostCandidate) bool {
	for _, scope := range candidate.Scopes {
		switch scope {
		case sdk.PermissionManagedNetworkListen, sdk.PermissionManagedNetworkDial, string(sdk.CapabilityDatasetQuery), string(sdk.CapabilityDatasetResolve), sdk.PermissionScopedSecretRead, sdk.PermissionScopedSecretWrite:
			return true
		}
	}
	return false
}

func (h *Host) startHostRuntime(candidate HostCandidate, security attemptSecurity, attempt *hostAttempt) ([]string, error) {
	if !managedRuntimeNeeded(candidate) {
		return nil, nil
	}
	if h.managed == nil || candidate.services == nil {
		return nil, errors.New("managed HostRuntime services are unavailable")
	}
	if err := h.checkGenerationRevoked(candidate); err != nil {
		return nil, err
	}
	admit := func(ctx context.Context, source sdk.ManagedSourceMetadata) error {
		if candidate.services.policy == nil {
			return nil
		}
		if candidate.services.evaluator == nil {
			return errors.New("managed entry policy evaluator unavailable")
		}
		metadata, err := policy.NewDirectMetadata(&net.TCPAddr{IP: net.ParseIP(source.Peer.Host), Port: source.Peer.Port})
		if err != nil {
			return err
		}
		body, err := policy.NewBodyWindow(nil, true, policy.BodyNotSkipped)
		if err != nil {
			return err
		}
		input, err := policy.NewInput(policy.ExtensionL4, "managed-entry", metadata, nil, body)
		if err != nil {
			return err
		}
		decision := candidate.services.evaluator.Evaluate(ctx, candidate.services.policy, input)
		if decision.Action == policy.ActionDeny || ctx.Err() != nil {
			return errors.New("managed source admission denied")
		}
		return nil
	}
	h.mu.RLock()
	registrar := h.networkSessions
	h.mu.RUnlock()
	var track func(generation.Session) (func(), error)
	if registrar != nil {
		track = func(session generation.Session) (func(), error) {
			handle, err := registrar.RegisterSession(candidate.Generation, generation.EntityKey{Module: "plugin-rpc", ID: candidate.InstanceID}, capabilityRuntimeToken(), session)
			if err != nil {
				return nil, err
			}
			return handle.Finish, nil
		}
	}
	owner, err := h.managed.Stage(managed.Authority{InstanceID: candidate.InstanceID, Generation: candidate.Generation, Grants: candidate.Grants, Admit: admit, Track: track})
	if err != nil {
		return nil, err
	}
	path := filepath.Join(security.endpointDirectory, "host-"+security.dial.Cookie[:12]+".sock")
	listenPath := path
	if security.endpointRoot != "" {
		listenPath = filepath.Join(security.endpointRoot, filepath.Base(path))
	}
	listener, err := net.Listen("unix", listenPath)
	if err != nil {
		_ = owner.Close()
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		_ = owner.Close()
		return nil, err
	}
	if security.sandboxUID > 0 {
		if err := os.Chown(path, security.sandboxUID, security.sandboxUID); err != nil {
			_ = listener.Close()
			_ = owner.Close()
			return nil, err
		}
	}
	calls := make(chan struct{}, 128)
	server := &http.Server{ReadTimeout: 10 * time.Second, ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 16 << 10, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != sdk.PluginHostCallPath || subtle.ConstantTimeCompare([]byte(r.Header.Get(sdk.HeaderPluginHostCredential)), []byte(security.dial.Cookie)) != 1 {
			http.Error(w, "denied", http.StatusForbidden)
			return
		}
		select {
		case calls <- struct{}{}:
			defer func() { <-calls }()
		default:
			http.Error(w, "busy", http.StatusTooManyRequests)
			return
		}
		payload, err := io.ReadAll(io.LimitReader(r.Body, sdk.PluginHostPayloadMaxBytes+2049))
		if err != nil || len(payload) > sdk.PluginHostPayloadMaxBytes+2048 {
			http.Error(w, "oversized", http.StatusBadRequest)
			return
		}
		defer clear(payload)
		var call sdk.HostRuntimeCall
		decoder := json.NewDecoder(bytes.NewReader(payload))
		decoder.DisallowUnknownFields()
		var extra any
		if decoder.Decode(&call) != nil || decoder.Decode(&extra) != io.EOF || call.Validate() != nil {
			http.Error(w, "invalid", http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithCancel(r.Context())
		stop := context.AfterFunc(owner.Context(), cancel)
		defer func() { stop(); cancel() }()
		response := h.dispatchHostRuntime(ctx, candidate, owner, attempt, call)
		defer clear(response.Payload)
		if ctx.Err() != nil {
			response = sdk.HostRuntimeResponse{Error: &sdk.RuntimeError{Code: sdk.ErrorUnavailable, Message: "managed generation call canceled"}}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	})}
	attempt.network = owner
	previousCleanup := attempt.cleanup
	attempt.cleanup = func() error { return errors.Join(owner.Close(), server.Close(), previousCleanup()) }
	go func() { _ = server.Serve(listener) }()
	return []string{sdk.EnvPluginHostEndpoint + "=unix:" + path}, nil
}

func capabilityRuntimeToken() string { return strconv.FormatInt(time.Now().UnixNano(), 36) }
func runtimeReply(value any, err error) sdk.HostRuntimeResponse {
	if err != nil {
		var failure *sdk.RuntimeError
		if errors.As(err, &failure) {
			return sdk.HostRuntimeResponse{Error: failure}
		}
		code := sdk.ErrorUnavailable
		if errors.Is(err, context.DeadlineExceeded) {
			code = sdk.ErrorDeadlineExceeded
		}
		return sdk.HostRuntimeResponse{Error: &sdk.RuntimeError{Code: code, Message: "managed Host operation failed"}}
	}
	payload, err := json.Marshal(value)
	if err != nil || len(payload) > sdk.PluginHostPayloadMaxBytes {
		return sdk.HostRuntimeResponse{Error: &sdk.RuntimeError{Code: sdk.ErrorInternal, Message: "managed response invalid"}}
	}
	return sdk.HostRuntimeResponse{Payload: payload}
}
func (h *Host) dispatchHostRuntime(ctx context.Context, candidate HostCandidate, owner *managed.Owner, attempt *hostAttempt, call sdk.HostRuntimeCall) sdk.HostRuntimeResponse {
	if ctx.Err() != nil {
		return runtimeReply(nil, ctx.Err())
	}
	auth := sdk.PolicyHostCallAuthorization{InstanceID: candidate.InstanceID, Generation: candidate.Generation, EntryID: candidate.InstanceID, DeclaredScopes: candidate.Scopes, GrantedScopes: candidate.Scopes}
	switch call.Operation {
	case sdk.HostRuntimeManagedNetwork:
		request, err := sdk.DecodeManagedNetworkRequest(call.Payload)
		if err != nil {
			return runtimeReply(nil, err)
		}
		if call.OperationID != "" && call.OperationID != request.RequestID {
			return runtimeReply(nil, &sdk.RuntimeError{Code: sdk.ErrorInvalidArgument, Message: "network operation identity differs"})
		}
		response, err := owner.Handle(ctx, request)
		return runtimeReply(response, err)
	case sdk.HostRuntimeDatasetResolve:
		response, err := sdk.CallDatasetResolveHost(ctx, candidate.services.datasets, sdk.DatasetResolveAuthorization{Binding: sdk.DatasetResolveBinding{InstanceID: candidate.InstanceID, Generation: candidate.Generation}, DeclaredScopes: candidate.Scopes, GrantedScopes: candidate.Scopes}, call.Payload)
		return runtimeReply(response, err)
	case sdk.HostRuntimeDatasetOpen:
		var request sdk.DatasetOpenRequest
		if strictRuntimePayload(call.Payload, &request) != nil || request.Validate() != nil {
			return runtimeReply(nil, &sdk.RuntimeError{Code: sdk.ErrorInvalidArgument, Message: "dataset open invalid"})
		}
		if candidate.services.datasets == nil {
			return runtimeReply(nil, errors.New("dataset provider missing"))
		}
		response, err := candidate.services.datasets.Open(auth, request)
		return runtimeReply(response, err)
	case sdk.HostRuntimeDatasetQuery:
		var request sdk.DatasetQueryRequest
		if strictRuntimePayload(call.Payload, &request) != nil {
			return runtimeReply(nil, &sdk.RuntimeError{Code: sdk.ErrorInvalidArgument, Message: "dataset query invalid"})
		}
		response, err := candidate.services.datasets.Query(ctx, auth, request)
		return runtimeReply(response, err)
	case sdk.HostRuntimeScopedSecret:
		if attempt.handleReady != nil {
			select {
			case <-attempt.handleReady:
			case <-ctx.Done():
				return runtimeReply(nil, ctx.Err())
			}
		}
		request, err := sdk.DecodeScopedSecretRequest(call.Payload)
		if err != nil {
			return runtimeReply(nil, err)
		}
		defer request.Material.Close()
		if request.Binding != owner.Binding() {
			return runtimeReply(nil, &sdk.RuntimeError{Code: sdk.ErrorPermissionDenied, Message: "secret binding denied"})
		}
		permission := sdk.PermissionScopedSecretWrite
		if request.Action == sdk.ScopedSecretRead {
			permission = sdk.PermissionScopedSecretRead
		}
		allowed := false
		for _, grant := range candidate.Grants {
			if grant.Name == permission && ((grant.ResourceKind == "" && (grant.ResourceID == request.Reference.Scope || grant.ResourceID == "secret-scope:"+request.Reference.Scope)) || (grant.ResourceKind == "secret-scope" && grant.ResourceID == request.Reference.Scope)) {
				allowed = true
			}
		}
		if !allowed {
			return runtimeReply(nil, &sdk.RuntimeError{Code: sdk.ErrorPermissionDenied, Message: "secret scope denied"})
		}
		redeemer, ok := h.secretRedeemer().(ScopedSecretRedeemer)
		if !ok {
			return runtimeReply(nil, errors.New("scoped secret redeemer unavailable"))
		}
		if attempt.handle != nil && request.Material != nil {
			_ = request.Material.WithBytes(func(value []byte) error { attempt.handle.RetainSensitiveValues([]string{string(value)}); return nil })
		}
		wire, err := redeemer.RedeemScopedPluginSecret(ctx, model.PluginSecretRedemptionRequest{Revision: uint64(candidate.Revision), GenerationID: candidate.ProviderGenerationID, RuntimeGenerationID: candidate.Generation, InstanceID: candidate.InstanceID, PluginID: candidate.PluginID, OperationID: candidate.OperationID, PackageDigest: candidate.PackageDigest, ArtifactDigest: candidate.Artifact.SHA256, Scoped: call.Payload})
		if err != nil {
			clear(wire)
			return runtimeReply(nil, errors.New("scoped secret redemption failed"))
		}
		response, err := sdk.DecodeScopedSecretResponse(request, wire)
		if err != nil {
			clear(wire)
			return runtimeReply(nil, errors.New("scoped secret response invalid"))
		}
		defer response.Material.Close()
		if attempt.handle != nil && response.Material != nil {
			_ = response.Material.WithBytes(func(value []byte) error { attempt.handle.RetainSensitiveValues([]string{string(value)}); return nil })
		}
		return sdk.HostRuntimeResponse{Payload: wire}
	default:
		return runtimeReply(nil, &sdk.RuntimeError{Code: sdk.ErrorInvalidArgument, Message: "HostRuntime operation unsupported"})
	}
}

func strictRuntimePayload(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return errors.New("trailing payload")
	}
	return nil
}
