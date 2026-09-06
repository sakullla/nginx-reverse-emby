package pluginsdk

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

type datasetResolveHostFunc func(context.Context, DatasetResolveBinding, DatasetResolveRequest) (DatasetReference, error)

func (fn datasetResolveHostFunc) ResolveDataset(ctx context.Context, binding DatasetResolveBinding, request DatasetResolveRequest) (DatasetReference, error) {
	return fn(ctx, binding, request)
}
func datasetResolveTestAuthorization(instance, generation string) DatasetResolveAuthorization {
	return DatasetResolveAuthorization{Binding: DatasetResolveBinding{InstanceID: instance, Generation: generation}, DeclaredScopes: []string{string(CapabilityDatasetQuery), string(CapabilityDatasetResolve)}, GrantedScopes: []string{string(CapabilityDatasetQuery), string(CapabilityDatasetResolve)}}
}
func datasetResolveTestReference(binding DatasetResolveBinding, version string) DatasetReference {
	return DatasetReference{Handle: strings.Repeat(version, 32), InstanceID: binding.InstanceID, Generation: binding.Generation, SourceID: "regions", VersionDigest: "sha256:" + strings.Repeat(version, 64)}
}

func TestDatasetResolveRPCUsesBoundGenerationsAndPreservesExplicitOpen(t *testing.T) {
	oldAuth := datasetResolveTestAuthorization("instance", "old-generation")
	newAuth := datasetResolveTestAuthorization("instance", "new-generation")
	oldRef := datasetResolveTestReference(oldAuth.Binding, "a")
	newRef := datasetResolveTestReference(newAuth.Binding, "b")
	bindings := map[DatasetResolveBinding]DatasetReference{oldAuth.Binding: oldRef, newAuth.Binding: newRef}
	var revoked atomic.Bool
	host := datasetResolveHostFunc(func(ctx context.Context, binding DatasetResolveBinding, request DatasetResolveRequest) (DatasetReference, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("resolve detached from its call budget")
		}
		if revoked.Load() {
			return DatasetReference{}, &RuntimeError{Code: ErrorPermissionDenied, Message: "generation revoked"}
		}
		ref, ok := bindings[binding]
		if !ok || ref.SourceID != request.SourceID {
			return DatasetReference{}, &RuntimeError{Code: ErrorUnavailable, Message: "source is not bound in this generation"}
		}
		return ref, nil
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var call HostRuntimeCall
		if json.NewDecoder(r.Body).Decode(&call) != nil || call.Operation != HostRuntimeDatasetResolve {
			http.Error(w, "invalid operation", 400)
			return
		}
		authorization := oldAuth
		if r.Header.Get(HeaderPluginHostCredential) == "new" {
			authorization = newAuth
		} else if r.Header.Get(HeaderPluginHostCredential) != "old" {
			http.Error(w, "unauthorized", 403)
			return
		}
		ref, err := CallDatasetResolveHost(r.Context(), host, authorization, call.Payload)
		if err != nil {
			var failure *RuntimeError
			if !errors.As(err, &failure) {
				failure = &RuntimeError{Code: ErrorInternal, Message: "resolve failed"}
			}
			_ = json.NewEncoder(w).Encode(HostRuntimeResponse{Error: failure})
			return
		}
		payload, _ := json.Marshal(ref)
		_ = json.NewEncoder(w).Encode(HostRuntimeResponse{Payload: payload})
	}))
	defer server.Close()
	newClient := func(credential string) *HostRuntimeClient {
		transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
		}}
		t.Cleanup(transport.CloseIdleConnections)
		return &HostRuntimeClient{client: &http.Client{Transport: transport}, credential: credential}
	}
	oldClient, newClientValue := newClient("old"), newClient("new")
	request := DatasetResolveRequest{SourceID: "regions"}
	for i := 0; i < 2; i++ {
		old, err := oldClient.ResolveDataset(t.Context(), request)
		if err != nil || old != oldRef {
			t.Fatalf("old generation resolve: %+v %v", old, err)
		}
		fresh, err := newClientValue.ResolveDataset(t.Context(), request)
		if err != nil || fresh != newRef {
			t.Fatalf("new generation resolve: %+v %v", fresh, err)
		}
	}
	if ValidateDatasetResolvedReference(request, oldRef, newAuth.Binding) == nil {
		t.Fatal("old reference became valid in the new generation")
	}
	if _, err := oldClient.ResolveDataset(t.Context(), DatasetResolveRequest{SourceID: "unbound"}); err == nil {
		t.Fatal("missing source silently selected latest")
	}
	revoked.Store(true)
	if _, err := oldClient.ResolveDataset(t.Context(), request); err == nil {
		t.Fatal("revoked generation resolved a source")
	}
	if (DatasetOpenRequest{SourceID: "regions"}).Validate() == nil {
		t.Fatal("existing exact-digest open became implicit resolve")
	}
	if err := (DatasetOpenRequest{SourceID: "regions", VersionDigest: oldRef.VersionDigest}).Validate(); err != nil {
		t.Fatal("existing explicit open rejected", err)
	}
}

func TestDatasetResolveHostRejectsMissingAuthorityAndForeignReferences(t *testing.T) {
	authorization := datasetResolveTestAuthorization("instance", "generation")
	reference := datasetResolveTestReference(authorization.Binding, "a")
	calls := 0
	host := datasetResolveHostFunc(func(context.Context, DatasetResolveBinding, DatasetResolveRequest) (DatasetReference, error) {
		calls++
		return reference, nil
	})
	payload := json.RawMessage(`{"source_id":"regions"}`)
	for _, scopes := range [][]string{nil, {string(CapabilityDatasetQuery)}, {string(CapabilityDatasetResolve)}} {
		for _, declaration := range []bool{false, true} {
			invalid := authorization
			if declaration {
				invalid.DeclaredScopes = scopes
			} else {
				invalid.GrantedScopes = scopes
			}
			if _, err := CallDatasetResolveHost(t.Context(), host, invalid, payload); PolicySecurityCallStatus(err) != PolicyStatusPermissionDenied {
				t.Fatalf("missing authority was not denied: %v", err)
			}
		}
	}
	if calls != 0 {
		t.Fatal("unauthorized resolve reached Host registry")
	}
	for _, field := range []string{"source", "instance", "generation", "handle", "digest"} {
		t.Run(field, func(t *testing.T) {
			bad := reference
			switch field {
			case "source":
				bad.SourceID = "other"
			case "instance":
				bad.InstanceID = "other"
			case "generation":
				bad.Generation = "old"
			case "handle":
				bad.Handle = "forged"
			case "digest":
				bad.VersionDigest = "latest"
			}
			host := datasetResolveHostFunc(func(context.Context, DatasetResolveBinding, DatasetResolveRequest) (DatasetReference, error) {
				return bad, nil
			})
			if _, err := CallDatasetResolveHost(t.Context(), host, authorization, payload); err == nil {
				t.Fatal("foreign/invalid Host reference accepted")
			}
		})
	}
	if _, err := CallDatasetResolveHost(t.Context(), nil, authorization, payload); PolicySecurityCallStatus(err) != PolicyStatusUnavailable {
		t.Fatal("missing resolver did not fail clearly", err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := CallDatasetResolveHost(canceled, host, authorization, payload); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored", err)
	}
	blocking := datasetResolveHostFunc(func(ctx context.Context, _ DatasetResolveBinding, _ DatasetResolveRequest) (DatasetReference, error) {
		<-ctx.Done()
		return reference, nil
	})
	if _, err := CallDatasetResolveHost(t.Context(), blocking, authorization, payload); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("resolver exceeded budget without error", err)
	}
}

func TestDatasetResolveRejectsNonSourceSelectorsAndMalformedRPCResponses(t *testing.T) {
	for _, raw := range []string{`{}`, `null`, `[]`, `{"SourceID":"regions"}`, `{"source_id":"regions","source_id":"other"}`, `{"source_id":"regions","generation":"g"}`, `{"source_id":"regions","version_digest":"latest"}`, `{"source_id":"regions","trusted":true}`, `{"source_id":"regions"} {}`, `{"source_id":7}`, strings.Repeat(" ", DatasetResolveMaxFrameBytes+1)} {
		if _, err := DecodeDatasetResolveRequest(json.RawMessage(raw)); err == nil {
			t.Fatalf("invalid resolve request accepted: %.80s", raw)
		}
	}
	valid := datasetResolveTestReference(datasetResolveTestAuthorization("instance", "generation").Binding, "a")
	encoded, _ := json.Marshal(valid)
	for name, payload := range map[string]json.RawMessage{
		"source":    json.RawMessage(strings.Replace(string(encoded), `"regions"`, `"other"`, 1)),
		"duplicate": append([]byte(`{"instance_id":"foreign",`), encoded[1:]...),
		"unknown":   append([]byte(`{"trusted":true,`), encoded[1:]...),
		"missing":   json.RawMessage(`{}`),
		"oversize":  json.RawMessage(`{"padding":"` + strings.Repeat("x", DatasetResolveMaxFrameBytes) + `"}`),
	} {
		t.Run(name, func(t *testing.T) {
			client := managedTestClient(t, func(_ *http.Request, call HostRuntimeCall) HostRuntimeResponse {
				if call.Operation != HostRuntimeDatasetResolve {
					t.Fatal("wrong RPC operation")
				}
				return HostRuntimeResponse{Payload: payload}
			})
			if _, err := client.ResolveDataset(t.Context(), DatasetResolveRequest{SourceID: "regions"}); err == nil {
				t.Fatal("malformed resolver response accepted")
			}
		})
	}
	large := datasetResolveTestAuthorization(strings.Repeat("<", PolicyIdentityMaxBytes), strings.Repeat("<", PolicyIdentityMaxBytes))
	ref := datasetResolveTestReference(large.Binding, "a")
	ref.SourceID = strings.Repeat("<", PolicyIdentityMaxBytes)
	host := datasetResolveHostFunc(func(context.Context, DatasetResolveBinding, DatasetResolveRequest) (DatasetReference, error) {
		return ref, nil
	})
	payload, _ := json.Marshal(DatasetResolveRequest{SourceID: ref.SourceID})
	if _, err := CallDatasetResolveHost(t.Context(), host, large, payload); PolicySecurityCallStatus(err) != PolicyStatusResourceExhausted {
		t.Fatal("complete RPC response byte limit ignored", err)
	}
}

func TestDatasetResolveCapabilityAndFeatureAdmissionIsAdditive(t *testing.T) {
	old := RequiredRPCFeatures([]string{string(CapabilityDatasetQuery)})
	if !reflect.DeepEqual(old, []string{RPCFeatureDatasetsV1}) {
		t.Fatal("old dataset users acquired a new mandatory feature")
	}
	scopes := []string{string(CapabilityDatasetQuery), string(CapabilityDatasetResolve)}
	features := RequiredRPCFeatures(scopes)
	if !reflect.DeepEqual(features, []string{RPCFeatureDatasetsV1, RPCFeatureDatasetResolveV1}) {
		t.Fatalf("resolve feature projection: %v", features)
	}
	if ValidateRPCFeatures(features, old) == nil {
		t.Fatal("Host without resolver feature accepted")
	}
	if err := ValidateRPCFeatures(features, features); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(PluginManifestSchemaV1()), `"dataset.resolve"`) {
		t.Fatal("manifest schema omits resolver permission")
	}
	manifest := Manifest{Runtime: Runtime{Kind: RuntimeWASMPolicy}, Permissions: []Permission{{Name: scopes[0]}, {Name: scopes[1]}}}
	if ValidateManifestManagedCapabilities(manifest, []HostCapability{CapabilityDatasetQuery}) == nil {
		t.Fatal("Host lacking resolver capability admitted package")
	}
	if err := ValidateManifestManagedCapabilities(manifest, []HostCapability{CapabilityDatasetQuery, CapabilityDatasetResolve}); err != nil {
		t.Fatal(err)
	}
	if capability, err := DatasetRuntimeCapability(HostRuntimeDatasetResolve); err != nil || capability != CapabilityDatasetResolve {
		t.Fatal("resolve operation missing from capability catalog")
	}
	if err := ValidatePolicyV1ImportGrant(PolicyHostDatasetResolve, scopes, scopes); err != nil {
		t.Fatal("resolve incorrectly requires a connection-source grant", err)
	}
	if ValidatePolicyV1ImportGrant(PolicyHostDatasetQuery, scopes, scopes) == nil {
		t.Fatal("query lost trusted-source grant requirement")
	}
	if len(PolicyV1RequiredHostFunctions()) != 6 {
		t.Fatal("legacy mandatory imports changed")
	}
	if _, ok := PolicyV1RequiredHostFunctions()[PolicyHostDatasetResolve]; ok {
		t.Fatal("resolver became mandatory for legacy guests")
	}
}
