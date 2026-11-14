//go:build !fast && !integration

package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func datasetRegisterPluginAuth() DatasetAuthorization {
	return DatasetAuthorization{ActorID: "plugin/ss", ResourceGroupID: "default", Manage: true}
}

func datasetRegisterRequest(data []byte) sdk.DatasetSourceRegisterRequest {
	return sdk.DatasetSourceRegisterRequest{
		Source:    sdk.DatasetSource{ID: "regions", Name: "Regions", URL: "https://datasets.example.com/regions.cidr", Format: sdk.DatasetFormatCIDR},
		Retrieval: sdk.DatasetSourceRetrieval{Mode: sdk.DatasetRetrievalPinned, Revision: "v1", ExpectedDigest: datasetServiceDigest(data)},
	}
}

func TestDatasetSourceRegisterPinnedPreparesAndActivates(t *testing.T) {
	service, _ := datasetServiceFixture(t)
	good := datasetServiceCIDR("cn-44")
	service.fetchCandidate = func(ctx context.Context, rawURL string, authority DatasetRetrieval, expected string) ([]byte, error) {
		if rawURL != "https://datasets.example.com/regions.cidr" {
			t.Fatalf("unexpected fetch URL %q", rawURL)
		}
		if authority.AllowPrivate || len(authority.RedirectHosts) != 0 {
			t.Fatal("plugin registration retained widened network authority")
		}
		if expected != datasetServiceDigest(good) {
			t.Fatalf("unexpected pinned digest %s", expected)
		}
		return good, nil
	}
	auth := datasetRegisterPluginAuth()
	request := datasetRegisterRequest(good)
	request.Source.RefreshIntervalSeconds = 3600
	response, err := service.RegisterSource(t.Context(), auth, request, "register-op-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := response.ValidateFor(request); err != nil {
		t.Fatalf("invalid registration acknowledgement: %v", err)
	}
	row, err := service.store.GetDatasetSource(t.Context(), request.Source.ID)
	if err != nil || row.CurrentDigest != response.Version.Digest || row.ResourceGroupID != "default" {
		t.Fatalf("activated source state = %+v, %v", row, err)
	}
	if row.NextRefreshAt == nil {
		t.Fatal("refresh interval did not schedule remote refresh")
	}
	var retrieval DatasetRetrieval
	if json.Unmarshal([]byte(row.RetrievalJSON), &retrieval) != nil || retrieval.Mode != datasetRetrievalPinned || retrieval.Revision != "v1" || retrieval.AllowPrivate || len(retrieval.RedirectHosts) != 0 {
		t.Fatalf("stored retrieval authority widened: %s", row.RetrievalJSON)
	}
	page, err := service.Catalog(t.Context(), auth, sdk.DatasetCatalogRequest{SourceID: request.Source.ID, VersionDigest: response.Version.Digest, Limit: 8})
	if err != nil || len(page.Classifications) != 1 || page.Classifications[0].Classification.Name != "cn-44" {
		t.Fatalf("prepared version not resolvable: %+v %v", page, err)
	}
}

func TestDatasetSourceRegisterRollingResolvesChecksum(t *testing.T) {
	service, _ := datasetServiceFixture(t)
	good := datasetServiceCIDR("cn-44")
	sum := sha256.Sum256(good)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	service.fetchPayload = func(ctx context.Context, rawURL string, authority DatasetRetrieval, maxBytes int64, timeout time.Duration) ([]byte, error) {
		if rawURL != "https://datasets.example.com/regions.cidr.sha256sum" {
			return nil, errors.New("unexpected checksum URL " + rawURL)
		}
		if authority.AllowPrivate || len(authority.RedirectHosts) != 0 {
			t.Fatal("rolling checksum fetch retained widened authority")
		}
		return []byte(hex.EncodeToString(sum[:]) + "  regions.cidr\n"), nil
	}
	service.fetchCandidate = func(ctx context.Context, rawURL string, authority DatasetRetrieval, expected string) ([]byte, error) {
		if expected != digest {
			t.Fatalf("rolling checksum did not project pinned digest: %s", expected)
		}
		return good, nil
	}
	auth := datasetRegisterPluginAuth()
	request := datasetRegisterRequest(good)
	request.Retrieval = sdk.DatasetSourceRetrieval{Mode: sdk.DatasetRetrievalRollingSHA256, ChecksumURL: "https://datasets.example.com/regions.cidr.sha256sum"}
	response, err := service.RegisterSource(t.Context(), auth, request, "register-op-rolling")
	if err != nil {
		t.Fatal(err)
	}
	if response.Version.RawDigest != digest {
		t.Fatalf("registered version pins wrong raw digest: %+v", response.Version)
	}
	stored, err := service.store.GetDatasetVersion(t.Context(), request.Source.ID, response.Version.Digest)
	if err != nil || stored.VerificationJSON == "" {
		t.Fatal("rolling checksum evidence was not stored with the version")
	}
}

func TestDatasetSourceRegisterRejectsNonCompliantRequests(t *testing.T) {
	service, _ := datasetServiceFixture(t)
	service.fetchCandidate = func(context.Context, string, DatasetRetrieval, string) ([]byte, error) {
		t.Fatal("rejected registration attempted a fetch")
		return nil, nil
	}
	auth := datasetRegisterPluginAuth()
	base := datasetRegisterRequest(datasetServiceCIDR("cn-44"))
	for name, tc := range map[string]struct {
		mutate func(*sdk.DatasetSourceRegisterRequest)
		want   error
	}{
		"non-https":           {mutate: func(r *sdk.DatasetSourceRegisterRequest) { r.Source.URL = "http://datasets.example.com/r" }, want: ErrInvalidArgument},
		"credential userinfo": {mutate: func(r *sdk.DatasetSourceRegisterRequest) { r.Source.URL = "https://user:pw@datasets.example.com/r" }, want: ErrInvalidArgument},
		"private literal":     {mutate: func(r *sdk.DatasetSourceRegisterRequest) { r.Source.URL = "https://192.168.7.7/r" }, want: ErrInvalidArgument},
		"special-use literal": {mutate: func(r *sdk.DatasetSourceRegisterRequest) { r.Source.URL = "https://192.0.2.9/r" }, want: errPluginHostDenied},
		"pinned checksum": {mutate: func(r *sdk.DatasetSourceRegisterRequest) {
			r.Retrieval.ChecksumURL = "https://datasets.example.com/sum"
		}, want: ErrInvalidArgument},
		"rolling pinned field": {mutate: func(r *sdk.DatasetSourceRegisterRequest) {
			r.Retrieval = sdk.DatasetSourceRetrieval{Mode: sdk.DatasetRetrievalRollingSHA256, ChecksumURL: "https://datasets.example.com/sum", ExpectedDigest: datasetServiceDigest([]byte("x"))}
		}, want: ErrInvalidArgument},
		"rolling special-use checksum": {mutate: func(r *sdk.DatasetSourceRegisterRequest) {
			r.Retrieval = sdk.DatasetSourceRetrieval{Mode: sdk.DatasetRetrievalRollingSHA256, ChecksumURL: "https://192.0.2.9/sum"}
		}, want: errPluginHostDenied},
	} {
		t.Run(name, func(t *testing.T) {
			request := base
			tc.mutate(&request)
			if _, err := service.RegisterSource(t.Context(), auth, request, "register-op"); !errors.Is(err, tc.want) {
				t.Fatalf("non-compliant registration returned %v, want %v", err, tc.want)
			}
			if _, err := service.store.GetDatasetSource(t.Context(), request.Source.ID); !errors.Is(err, storage.ErrDatasetNotFound) {
				t.Fatal("rejected registration still stored a source")
			}
		})
	}
	// The legacy public put-source operation still cannot widen plugin
	// retrieval authority; source-register carries its own restricted grant.
	if err := service.PutSource(t.Context(), auth, base.Source, DatasetRetrieval{Mode: datasetRetrievalRolling, ChecksumURL: "https://datasets.example.com/sum"}); !errors.Is(err, errPluginHostDenied) {
		t.Fatal("legacy put-source granted rolling retrieval to a plugin")
	}
}

func TestDatasetSourceRegisterFailedCandidateKeepsActiveVersion(t *testing.T) {
	service, _ := datasetServiceFixture(t)
	good := datasetServiceCIDR("cn-44")
	var payload atomic.Value
	payload.Store(good)
	service.fetchCandidate = func(context.Context, string, DatasetRetrieval, string) ([]byte, error) {
		return payload.Load().([]byte), nil
	}
	auth := datasetRegisterPluginAuth()
	first, err := service.RegisterSource(t.Context(), auth, datasetRegisterRequest(good), "register-good")
	if err != nil {
		t.Fatal(err)
	}
	old := first.Version.Digest
	payload.Store([]byte("corrupt"))
	corrupt := datasetRegisterRequest([]byte("corrupt"))
	corrupt.Retrieval.Revision = "v2"
	if _, err := service.RegisterSource(t.Context(), auth, corrupt, "register-corrupt"); err == nil {
		t.Fatal("corrupt candidate accepted")
	}
	row, err := service.store.GetDatasetSource(t.Context(), corrupt.Source.ID)
	if err != nil || row.CurrentDigest != old || row.LastFailure != string(sdk.DatasetFailureInvalidData) {
		t.Fatalf("failed registration replaced last-good state: %+v %v", row, err)
	}
	page, err := service.Catalog(t.Context(), auth, sdk.DatasetCatalogRequest{SourceID: corrupt.Source.ID, VersionDigest: old, Limit: 8})
	if err != nil || len(page.Classifications) != 1 {
		t.Fatal("old version no longer resolvable after failed registration")
	}
}

func TestDatasetSourceRegisterPluginHostDispatchAndReplay(t *testing.T) {
	service, _ := datasetServiceFixture(t)
	good := datasetServiceCIDR("cn-44")
	var fetches atomic.Int32
	service.fetchCandidate = func(context.Context, string, DatasetRetrieval, string) ([]byte, error) {
		fetches.Add(1)
		return good, nil
	}
	manager := &PluginCapabilityManager{store: service.store, datasets: service, operationLocks: make(map[string]*pluginCapabilityOperationLock)}
	caller := pluginhost.Candidate{InstanceID: "instance", ResourceGroupID: "default",
		Identity: pluginhost.Identity{PluginID: "ss-plugin", Generation: "generation", Scopes: []string{string(sdk.CapabilityDatasetSourceRegister)}},
		Grants:   []string{string(sdk.CapabilityDatasetSourceRegister)}}
	client := datasetHostRuntimeClient(t, manager, &caller)
	request := datasetRegisterRequest(good)
	first, err := client.RegisterDatasetSource(t.Context(), "register-1", request)
	if err != nil || first.Version.SourceID != request.Source.ID || first.Version.Digest == "" {
		t.Fatalf("registration = %+v, %v", first, err)
	}
	// Durable replay after a Host restart does not fetch or mutate again.
	restarted := &PluginCapabilityManager{store: service.store, datasets: service, operationLocks: make(map[string]*pluginCapabilityOperationLock)}
	second := datasetHostRuntimeClient(t, restarted, &caller)
	replayed, err := second.RegisterDatasetSource(t.Context(), "register-1", request)
	if err != nil || replayed != first {
		t.Fatalf("replayed registration = %+v, %v", replayed, err)
	}
	if fetches.Load() != 1 {
		t.Fatal("replayed registration fetched again")
	}
	// The durable mutation requires an operation ID on the envelope.
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var ignored any
	var failure *sdk.RuntimeError
	if err := second.Call(t.Context(), sdk.HostRuntimeCall{Operation: sdk.HostRuntimeDatasetSourceRegister, Payload: encoded}, &ignored); !errors.As(err, &failure) || failure.Code != sdk.ErrorInvalidArgument {
		t.Fatalf("missing operation id returned %v", err)
	}
	// A caller without the grant cannot reach the service at all.
	caller.Grants = nil
	if _, err := second.RegisterDatasetSource(t.Context(), "register-2", request); !errors.As(err, &failure) || failure.Code != sdk.ErrorPermissionDenied {
		t.Fatalf("ungranted registration returned %v", err)
	}
	caller.Grants = []string{string(sdk.CapabilityDatasetSourceRegister)}
	caller.GrantSelectors = map[string][]string{string(sdk.CapabilityDatasetSourceRegister): {"other-source"}}
	if _, err := second.RegisterDatasetSource(t.Context(), "register-3", request); !errors.As(err, &failure) || failure.Code != sdk.ErrorPermissionDenied {
		t.Fatalf("selector-restricted registration returned %v", err)
	}
	caller.GrantSelectors = nil
	// A destination passing SDK syntax but denied by Host-side authority is
	// still refused before any fetch happens.
	denied := request
	denied.Source.ID = "blocked"
	denied.Source.URL = "https://192.0.2.9/regions.cidr"
	if _, err := second.RegisterDatasetSource(t.Context(), "register-4", denied); !errors.As(err, &failure) || failure.Code != sdk.ErrorPermissionDenied {
		t.Fatalf("Host-denied destination returned %v", err)
	}
	if fetches.Load() != 1 {
		t.Fatal("denied registration fetched")
	}
}
