//go:build !fast && !integration

package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/datasets"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func datasetServiceFixture(t *testing.T) (*DatasetService, DatasetAuthorization) {
	t.Helper()
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	service := NewDatasetService(config.Config{LocalAgentID: "local", EnableLocalAgent: true}, store)
	t.Cleanup(func() { _ = service.Close(); _ = store.Close() })
	return service, DatasetAuthorization{ActorID: "administrator", ResourceGroupID: "default", Administrator: true, Manage: true}
}
func datasetServiceCIDR(name string) []byte {
	data, _ := json.Marshal(datasets.CIDRDocument{Schema: datasets.CIDRSchema, Classifications: []datasets.CIDRClassification{{Name: name, Kind: sdk.DatasetClassificationRegion, DisplayName: "省份", CIDRs: []string{"192.0.2.0/24"}}}})
	return data
}
func datasetServiceDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func datasetHostRuntimeClient(t *testing.T, manager *PluginCapabilityManager, candidate *pluginhost.Candidate) *sdk.HostRuntimeClient {
	t.Helper()
	directory, err := os.MkdirTemp("", "ds-host-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	socket := filepath.Join(directory, "dataset.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal("actual private HostRuntime endpoint unavailable", err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != sdk.PluginHostCallPath || r.Header.Get(sdk.HeaderPluginHostCredential) != "dataset-test-credential" {
			http.Error(w, "denied", http.StatusForbidden)
			return
		}
		var call sdk.HostRuntimeCall
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if decoder.Decode(&call) != nil || call.Validate() != nil {
			http.Error(w, "invalid", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(manager.DispatchPluginHostResource(r.Context(), *candidate, call))
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	cookie := filepath.Join(directory, "cookie")
	if err := os.WriteFile(cookie, []byte("dataset-test-credential"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(sdk.EnvPluginHostEndpoint, "unix:"+socket)
	t.Setenv("NRE_PLUGIN_COOKIE_FILE", cookie)
	client, err := sdk.NewHostRuntimeClientFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestDatasetServiceRefreshIntegrityAndMissingClassPreserveActiveVersion(t *testing.T) {
	service, auth := datasetServiceFixture(t)
	good := datasetServiceCIDR("cn-44")
	var bad atomic.Bool
	var downloads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downloads.Add(1)
		if bad.Load() {
			_, _ = w.Write([]byte("corrupt"))
			return
		}
		_, _ = w.Write(good)
	}))
	defer server.Close()
	source := sdk.DatasetSource{ID: "regions", Name: "Regions", URL: server.URL, Format: sdk.DatasetFormatCIDR, RefreshIntervalSeconds: 3600}
	retrieval := DatasetRetrieval{Revision: "fixed-v1", ExpectedDigest: datasetServiceDigest(good), AllowPrivate: true}
	if err := service.PutSource(t.Context(), auth, source, retrieval); err != nil {
		t.Fatal(err)
	}
	if err := service.RefreshDue(t.Context(), time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	versions, err := service.store.ListDatasetVersions(t.Context(), source.ID)
	if err != nil || len(versions) != 1 {
		t.Fatalf("scheduled download did not prepare actual index: %d %v", len(versions), err)
	}
	old := versions[0]
	if _, err := service.Control(t.Context(), auth, sdk.DatasetControlRequest{Action: sdk.DatasetControlActivate, SourceID: source.ID, VersionDigest: old.Digest}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Control(t.Context(), auth, sdk.DatasetControlRequest{Action: sdk.DatasetControlRefresh, SourceID: source.ID}); err != nil {
		t.Fatal(err)
	}
	versions, _ = service.store.ListDatasetVersions(t.Context(), source.ID)
	if len(versions) != 1 || downloads.Load() != 2 {
		t.Fatal("unchanged refresh manufactured a new data version")
	}
	bad.Store(true)
	if _, err := service.Control(t.Context(), auth, sdk.DatasetControlRequest{Action: sdk.DatasetControlRefresh, SourceID: source.ID}); err == nil {
		t.Fatal("bad source digest accepted")
	}
	row, _ := service.store.GetDatasetSource(t.Context(), source.ID)
	if row.CurrentDigest != old.Digest || row.LastFailure != string(sdk.DatasetFailureDigest) {
		t.Fatalf("refresh failure replaced last-good state: %+v", row)
	}
	if err := service.store.PutDatasetBinding(t.Context(), storage.DatasetBindingRow{AgentID: "local", InstanceID: "existing-instance", SourceID: source.ID, VersionDigest: old.Digest, Revision: 1, ClassificationsJSON: `[{"name":"cn-44","kind":"region"}]`}); err != nil {
		t.Fatal(err)
	}
	newData := datasetServiceCIDR("cn-32")
	uploaded, err := service.Upload(t.Context(), auth, source.ID, bytes.NewReader(newData))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Control(t.Context(), auth, sdk.DatasetControlRequest{Action: sdk.DatasetControlImport, SourceID: source.ID, Candidate: &sdk.DatasetImportCandidate{Revision: "fixed-v2", ExpectedDigest: uploaded, ArtifactDigest: uploaded}}); err != nil {
		t.Fatal(err)
	}
	versions, _ = service.store.ListDatasetVersions(t.Context(), source.ID)
	var next string
	for _, version := range versions {
		if version.Digest != old.Digest {
			next = version.Digest
		}
	}
	if _, err := service.Control(t.Context(), auth, sdk.DatasetControlRequest{Action: sdk.DatasetControlActivate, SourceID: source.ID, VersionDigest: next}); err == nil {
		t.Fatal("candidate removed a referenced province")
	}
	row, _ = service.store.GetDatasetSource(t.Context(), source.ID)
	if row.CurrentDigest != old.Digest || row.LastFailure != string(sdk.DatasetFailureMissingClassification) {
		t.Fatal("missing-class failure not preserved or visible")
	}
	bindings, _ := service.store.DatasetBindings(t.Context(), source.ID)
	if bindings[0].VersionDigest != old.Digest {
		t.Fatal("failed activation changed consumer version")
	}
	page, err := service.Catalog(t.Context(), auth, sdk.DatasetCatalogRequest{SourceID: source.ID, VersionDigest: old.Digest, Limit: 128})
	if err != nil || len(page.Classifications) != 1 || page.Classifications[0].Classification.Name != "cn-44" {
		t.Fatal("old catalog not usable after candidate failure", err)
	}
}

func TestDatasetServicePrivateSourceAndRegisteredHostOperationsRequireAuthority(t *testing.T) {
	service, auth := datasetServiceFixture(t)
	data := datasetServiceCIDR("cn-44")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(data) }))
	defer server.Close()
	source := sdk.DatasetSource{ID: "regions", Name: "Regions", URL: server.URL, Format: sdk.DatasetFormatCIDR}
	if err := service.PutSource(t.Context(), auth, source, DatasetRetrieval{}); err != nil {
		t.Fatal(err)
	}
	candidate := sdk.DatasetImportCandidate{Revision: "fixed", ExpectedDigest: datasetServiceDigest(data), URL: server.URL}
	if _, err := service.Control(t.Context(), auth, sdk.DatasetControlRequest{Action: sdk.DatasetControlImport, SourceID: source.ID, Candidate: &candidate}); !errors.Is(err, errPluginHostDenied) {
		t.Fatalf("implicit private network access allowed: %v", err)
	}
	foreign := DatasetAuthorization{ActorID: "plugin/p", ResourceGroupID: "other", Manage: true}
	if _, err := service.Catalog(t.Context(), foreign, sdk.DatasetCatalogRequest{SourceID: source.ID, Limit: 1}); !errors.Is(err, errPluginHostDenied) {
		t.Fatal("foreign group could read catalog")
	}
	if err := service.PutSource(t.Context(), foreign, source, DatasetRetrieval{AllowPrivate: true}); !errors.Is(err, errPluginHostDenied) {
		t.Fatal("plugin granted itself private network authority")
	}
	uploaded, err := service.Upload(t.Context(), auth, source.ID, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Control(t.Context(), auth, sdk.DatasetControlRequest{Action: sdk.DatasetControlImport, SourceID: source.ID, Candidate: &sdk.DatasetImportCandidate{Revision: "fixed", ExpectedDigest: uploaded, ArtifactDigest: uploaded}}); err != nil {
		t.Fatal(err)
	}
	manager := &PluginCapabilityManager{datasets: service}
	caller := pluginhost.Candidate{InstanceID: "instance", ResourceGroupID: "default", Identity: pluginhost.Identity{PluginID: "plugin", Generation: "generation", Scopes: []string{string(sdk.CapabilityDatasetManage)}}, Grants: []string{string(sdk.CapabilityDatasetManage)}, GrantSelectors: map[string][]string{string(sdk.CapabilityDatasetManage): {source.ID}}}
	call := sdk.HostRuntimeCall{Operation: sdk.HostRuntimeDatasetCatalog, Payload: json.RawMessage(`{"source_id":"regions","limit":1}`)}
	client := datasetHostRuntimeClient(t, manager, &caller)
	var page sdk.DatasetCatalogResponse
	if err := client.Call(t.Context(), call, &page); err != nil || len(page.Versions) != 1 {
		t.Fatal("registered private HostRuntime path did not return actual history", err)
	}
	caller.InstanceID = "another-authorized-instance"
	if err := client.Call(t.Context(), call, &page); err != nil {
		t.Fatal("shared source locked to creator instance", err)
	}
	caller.Grants = nil
	err = client.Call(t.Context(), call, &page)
	var runtimeFailure *sdk.RuntimeError
	if !errors.As(err, &runtimeFailure) || runtimeFailure.Code != sdk.ErrorPermissionDenied {
		t.Fatal("ungranted dataset operation was accepted")
	}
	caller.Grants = []string{string(sdk.CapabilityDatasetManage)}
	caller.GrantSelectors[string(sdk.CapabilityDatasetManage)] = []string{"other-source"}
	if err := client.Call(t.Context(), call, &page); err == nil {
		t.Fatal("source selector grant bypassed")
	}
	caller.GrantSelectors[string(sdk.CapabilityDatasetManage)] = []string{source.ID}
	caller.ResourceGroupID = "other-group"
	if err := client.Call(t.Context(), call, &page); err == nil {
		t.Fatal("foreign resource group accessed shared source")
	}
	if _, err := service.store.ReadDatasetUpload(t.Context(), "other-source", uploaded); !errors.Is(err, storage.ErrDatasetNotFound) {
		t.Fatal("upload escaped source scope")
	}
	if err := service.store.PruneDatasetUploads(t.Context(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := service.store.ReadDatasetUpload(t.Context(), source.ID, uploaded); !errors.Is(err, storage.ErrDatasetNotFound) {
		t.Fatal("expired staging upload remained available")
	}
	versions, _ := service.store.ListDatasetVersions(t.Context(), source.ID)
	if _, _, err := service.store.ReadDatasetIndex(t.Context(), source.ID, versions[0].Digest); err != nil {
		t.Fatal("upload cleanup removed prepared index", err)
	}
}

func TestDatasetFetchRejectsUnapprovedRedirectAndChecksum(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("payload")) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, strings.Replace(target.URL, "127.0.0.1", "localhost", 1), http.StatusFound)
	}))
	defer redirect.Close()
	if _, err := fetchDatasetCandidate(t.Context(), redirect.URL, DatasetRetrieval{AllowPrivate: true}, datasetServiceDigest([]byte("payload"))); err == nil {
		t.Fatal("unapproved redirect host accepted")
	}
	if _, err := fetchDatasetCandidate(t.Context(), target.URL, DatasetRetrieval{AllowPrivate: true}, "sha256:"+strings.Repeat("a", 64)); err == nil {
		t.Fatal("wrong checksum accepted")
	}
	if value, err := fetchDatasetCandidate(t.Context(), redirect.URL, DatasetRetrieval{AllowPrivate: true, RedirectHosts: []string{"localhost"}}, datasetServiceDigest([]byte("payload"))); err != nil || string(value) != "payload" {
		t.Fatalf("approved bounded redirect failed: %q %v", value, err)
	}
}
