package http

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/authz"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func (d Dependencies) datasetAdmin(w http.ResponseWriter, r *http.Request) (service.DatasetAuthorization, bool) {
	actor, ok := d.requireAccessPermission(w, r, authz.PermissionSystemAdmin)
	if !ok {
		return service.DatasetAuthorization{}, false
	}
	if d.DatasetService == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorPayload("dataset service unavailable"))
		return service.DatasetAuthorization{}, false
	}
	return service.DatasetAuthorization{ActorID: actor.ID, ResourceGroupID: authz.DefaultResourceGroup, Administrator: true, Manage: true}, true
}
func datasetHTTPError(w http.ResponseWriter, err error) {
	if errors.Is(err, storage.ErrDatasetNotFound) {
		writeJSON(w, http.StatusNotFound, errorPayload("dataset resource not found"))
		return
	}
	if errors.Is(err, storage.ErrDatasetInUse) {
		writeJSON(w, http.StatusConflict, errorPayload("dataset resource is referenced or retained"))
		return
	}
	status, payload := mapServiceError(err)
	writeJSON(w, status, payload)
}
func (d Dependencies) handleDatasets(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	authority, ok := d.datasetAdmin(w, r)
	if !ok {
		return
	}
	result, err := d.DatasetService.List(r.Context(), authority)
	if err != nil {
		datasetHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sources": result})
}
func (d Dependencies) handleDatasetSource(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.NotFound(w, r)
		return
	}
	authority, ok := d.datasetAdmin(w, r)
	if !ok {
		return
	}
	var input struct {
		Source    pluginsdk.DatasetSource  `json:"source"`
		Retrieval service.DatasetRetrieval `json:"retrieval"`
	}
	if err := decodeStrictPluginJSON(r, &input); err != nil {
		datasetHTTPError(w, err)
		return
	}
	if input.Source.ID != r.PathValue("sourceID") {
		datasetHTTPError(w, service.ErrInvalidArgument)
		return
	}
	if err := d.DatasetService.PutSource(r.Context(), authority, input.Source, input.Retrieval); err != nil {
		datasetHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"stored": true})
}
func (d Dependencies) handleDatasetControl(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	authority, ok := d.datasetAdmin(w, r)
	if !ok {
		return
	}
	var request pluginsdk.DatasetControlRequest
	if err := decodeStrictPluginJSON(r, &request); err != nil {
		datasetHTTPError(w, err)
		return
	}
	response, err := d.DatasetService.Control(r.Context(), authority, request)
	if err != nil {
		datasetHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}
func (d Dependencies) handleDatasetCatalog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	authority, ok := d.datasetAdmin(w, r)
	if !ok {
		return
	}
	limit := pluginsdk.DatasetMaxCatalogPage
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			datasetHTTPError(w, service.ErrInvalidArgument)
			return
		}
		limit = value
	}
	response, err := d.DatasetService.Catalog(r.Context(), authority, pluginsdk.DatasetCatalogRequest{SourceID: r.PathValue("sourceID"), VersionDigest: r.URL.Query().Get("version_digest"), Cursor: r.URL.Query().Get("cursor"), Limit: limit})
	if err != nil {
		datasetHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}
func (d Dependencies) handleDatasetStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	authority, ok := d.datasetAdmin(w, r)
	if !ok {
		return
	}
	response, err := d.DatasetService.Status(r.Context(), authority, pluginsdk.DatasetStatusRequest{SourceID: r.PathValue("sourceID"), NodeID: r.URL.Query().Get("node_id")})
	if err != nil {
		datasetHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}
func (d Dependencies) handleDatasetBinding(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.NotFound(w, r)
		return
	}
	authority, ok := d.datasetAdmin(w, r)
	if !ok {
		return
	}
	var request service.DatasetBindingRequest
	if err := decodeStrictPluginJSON(r, &request); err != nil {
		datasetHTTPError(w, err)
		return
	}
	if request.SourceID != r.PathValue("sourceID") {
		datasetHTTPError(w, service.ErrInvalidArgument)
		return
	}
	if err := d.DatasetService.Bind(r.Context(), authority, request); err != nil {
		datasetHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"stored": true})
}
func (d Dependencies) handleDatasetUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	authority, ok := d.datasetAdmin(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, pluginsdk.DatasetMaxDownloadBytes)
	digest, err := d.DatasetService.Upload(r.Context(), authority, r.PathValue("sourceID"), r.Body)
	if err != nil {
		datasetHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"artifact_digest": digest})
}
func (d Dependencies) handleAgentDatasetArtifact(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	agent, ok := d.authenticateRevisionAgent(w, r)
	if !ok {
		return
	}
	if d.DatasetService == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorPayload("dataset service unavailable"))
		return
	}
	revision, err := strconv.ParseInt(r.URL.Query().Get("revision"), 10, 64)
	if err != nil || revision <= 0 {
		datasetHTTPError(w, service.ErrInvalidArgument)
		return
	}
	artifact, err := d.DatasetService.ResolveAgentDatasetArtifact(r.Context(), agent.ID, revision, r.URL.Query().Get("snapshot_digest"), r.PathValue("artifactID"))
	if err != nil {
		datasetHTTPError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.nre.dataset-index")
	w.Header().Set("Content-Length", strconv.FormatInt(artifact.SizeBytes, 10))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(artifact.Payload)
}
