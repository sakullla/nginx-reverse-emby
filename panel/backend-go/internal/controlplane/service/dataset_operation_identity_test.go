//go:build !fast && !integration

package service

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestDatasetSDKBoundMutationsScopeRevisionIDsAndReplayByInstance(t *testing.T) {
	for _, action := range []sdk.DatasetControlAction{sdk.DatasetControlActivate, sdk.DatasetControlRefresh} {
		t.Run(string(action), func(t *testing.T) {
			service := rollingDatasetFixture(t)
			admin := DatasetAuthorization{ActorID: "administrator", ResourceGroupID: "default", Administrator: true, Manage: true}
			base := datasetServiceCIDR("cn-44")
			bodies := [][]byte{base, []byte(strings.Replace(string(base), "192.0.2.0/24", "198.51.100.0/24", 1)), []byte(strings.Replace(string(base), "192.0.2.0/24", "203.0.113.0/24", 1))}
			var mu sync.Mutex
			body := base
			downloads := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				downloads++
				if strings.HasSuffix(r.URL.Path, ".sha256sum") {
					_, _ = w.Write([]byte(datasetServiceDigest(body)[7:] + "  regions.json\n"))
					return
				}
				_, _ = w.Write(body)
			}))
			defer server.Close()
			source := sdk.DatasetSource{ID: "instance-scoped", Name: "Shared source", URL: server.URL + "/regions.json", Format: sdk.DatasetFormatCIDR}
			retrieval := DatasetRetrieval{Mode: datasetRetrievalRolling, ChecksumURL: server.URL + "/regions.json.sha256sum", AllowPrivate: true}
			if err := service.PutSource(t.Context(), admin, source, retrieval); err != nil {
				t.Fatal(err)
			}
			if _, err := service.Control(t.Context(), admin, sdk.DatasetControlRequest{Action: sdk.DatasetControlRefresh, SourceID: source.ID}); err != nil {
				t.Fatal(err)
			}
			initial, err := service.store.GetDatasetSource(t.Context(), source.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := service.Bind(t.Context(), admin, DatasetBindingRequest{SourceID: source.ID, VersionDigest: initial.CurrentDigest, AgentID: "local", InstanceID: "rolling-instance", Classifications: []sdk.DatasetClassification{{Name: "cn-44", Kind: sdk.DatasetClassificationRegion}}}); err != nil {
				t.Fatal(err)
			}
			versions := make([]string, 2)
			if action == sdk.DatasetControlActivate {
				for i := range versions {
					digest, err := service.Upload(t.Context(), admin, source.ID, bytes.NewReader(bodies[i+1]))
					if err != nil {
						t.Fatal(err)
					}
					if _, err := service.Control(t.Context(), admin, sdk.DatasetControlRequest{Action: sdk.DatasetControlImport, SourceID: source.ID, Candidate: &sdk.DatasetImportCandidate{Revision: "version-" + digest, ExpectedDigest: digest, ArtifactDigest: digest}}); err != nil {
						t.Fatal(err)
					}
					index, err := service.prepare(t.Context(), initial, sdk.DatasetImportCandidate{Revision: "version-" + digest, ExpectedDigest: digest, ArtifactDigest: digest})
					if err != nil {
						t.Fatal(err)
					}
					versions[i] = index.Digest
				}
			}
			manager := &PluginCapabilityManager{store: service.store, datasets: service, operationLocks: make(map[string]*pluginCapabilityOperationLock)}
			callers := []pluginhost.Candidate{
				{InstanceID: "controller-a", ResourceGroupID: "default", Identity: pluginhost.Identity{PluginID: "dataset-controller", Generation: "generation-a", Scopes: []string{string(sdk.CapabilityDatasetManage)}}, Grants: []string{string(sdk.CapabilityDatasetManage)}},
				{InstanceID: "controller-b", ResourceGroupID: "default", Identity: pluginhost.Identity{PluginID: "dataset-controller", Generation: "generation-b", Scopes: []string{string(sdk.CapabilityDatasetManage)}}, Grants: []string{string(sdk.CapabilityDatasetManage)}},
			}
			clients := []*sdk.HostRuntimeClient{datasetHostRuntimeClient(t, manager, &callers[0]), datasetHostRuntimeClient(t, manager, &callers[1])}
			requests := make([]sdk.DatasetControlRequest, 2)
			responses := make([]sdk.DatasetControlResponse, 2)
			internalIDs := make([]string, 2)
			previousDigest := initial.CurrentDigest
			for i, client := range clients {
				mu.Lock()
				body = bodies[i+1]
				mu.Unlock()
				requests[i] = sdk.DatasetControlRequest{Action: action, SourceID: source.ID, VersionDigest: versions[i]}
				response, err := client.ControlDataset(t.Context(), "operation-1", requests[i])
				if err != nil || response.OperationID != "operation-1" {
					t.Fatalf("instance %d mutation failed or changed public acknowledgement: %+v %v", i, response, err)
				}
				responses[i] = response
				row, err := service.store.GetDatasetSource(t.Context(), source.ID)
				if err != nil || row.CurrentDigest == previousDigest {
					t.Fatalf("instance %d did not execute an actual bound mutation: %v", i, err)
				}
				previousDigest = row.CurrentDigest
				bindings, err := service.store.DatasetBindings(t.Context(), source.ID)
				if err != nil || len(bindings) != 1 || bindings[0].VersionDigest != row.CurrentDigest {
					t.Fatal("source and consumer version differ", err)
				}
				pointer, found, err := service.store.GetAgentRevisionPointer(t.Context(), "local")
				if err != nil || !found || pointer.DesiredRevision != bindings[0].Revision {
					t.Fatal("mutation did not reach the revision ledger", err)
				}
				issued, err := service.store.ListAgentRevisions(t.Context(), "local")
				if err != nil {
					t.Fatal(err)
				}
				for _, revision := range issued {
					if revision.Revision == pointer.DesiredRevision {
						internalIDs[i] = revision.OperationID
					}
				}
			}
			if internalIDs[0] == "" || internalIDs[1] == "" || internalIDs[0] == internalIDs[1] || internalIDs[0] == "operation-1" || internalIDs[1] == "operation-1" {
				t.Fatalf("global operation identities are not isolated: %v", internalIDs)
			}
			before, _, _ := service.store.GetAgentRevisionPointer(t.Context(), "local")
			mu.Lock()
			beforeDownloads := downloads
			mu.Unlock()
			// New managers/endpoints prove both replay results come from durable,
			// independently scoped records rather than an in-memory response cache.
			restarted := &PluginCapabilityManager{store: service.store, datasets: service, operationLocks: make(map[string]*pluginCapabilityOperationLock)}
			for i := range callers {
				client := datasetHostRuntimeClient(t, restarted, &callers[i])
				response, err := client.ControlDataset(t.Context(), "operation-1", requests[i])
				if err != nil || response != responses[i] {
					t.Fatalf("instance %d independent replay failed: %+v %v", i, response, err)
				}
			}
			after, _, _ := service.store.GetAgentRevisionPointer(t.Context(), "local")
			current, _ := service.store.GetDatasetSource(t.Context(), source.ID)
			mu.Lock()
			afterDownloads := downloads
			mu.Unlock()
			if before.DesiredRevision != after.DesiredRevision || current.CurrentDigest != previousDigest || beforeDownloads != afterDownloads {
				t.Fatal("replay repeated a mutation/download or rolled back the newer instance's version")
			}
		})
	}
}
