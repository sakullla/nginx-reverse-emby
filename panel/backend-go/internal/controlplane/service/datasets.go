package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/datasets"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/revision"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

// DatasetAuthorization is created by authenticated HTTP/plugin Host adapters,
// never decoded from a management request. Administrator grants authorize shared
// source management; plugins remain confined to their existing resource group.
type DatasetAuthorization struct {
	ActorID, ResourceGroupID string
	Administrator, Manage    bool
}

// DatasetRetrieval selects a pinned revision or an administrator-approved SHA256
// sidecar. A rolling digest is captured before each download and is integrity
// evidence, not an independent publisher signature. Version revisions identify
// captured bytes, never the mutable latest URL.
type DatasetRetrieval struct {
	Mode           string   `json:"mode,omitempty"`
	ChecksumURL    string   `json:"checksum_url,omitempty"`
	Revision       string   `json:"revision,omitempty"`
	ExpectedDigest string   `json:"expected_digest,omitempty"`
	AllowPrivate   bool     `json:"allow_private"`
	RedirectHosts  []string `json:"redirect_hosts,omitempty"`
}

func (retrieval DatasetRetrieval) Validate(source pluginsdk.DatasetSource) error {
	if retrieval.Mode != "" && retrieval.Mode != datasetRetrievalPinned && retrieval.Mode != datasetRetrievalRolling {
		return fmt.Errorf("%w: unsupported dataset retrieval mode", ErrInvalidArgument)
	}
	if len(retrieval.RedirectHosts) > 8 {
		return fmt.Errorf("%w: too many dataset redirect hosts", ErrInvalidArgument)
	}
	for _, host := range retrieval.RedirectHosts {
		if host == "" || strings.ToLower(host) != host || strings.ContainsAny(host, "/:@?#\\ \t\r\n") {
			return fmt.Errorf("%w: invalid dataset redirect host", ErrInvalidArgument)
		}
	}
	if retrieval.Mode == datasetRetrievalRolling {
		if retrieval.Revision != "" || retrieval.ExpectedDigest != "" || source.URL == "" {
			return fmt.Errorf("%w: rolling source cannot use pinned fields or uploads only", ErrInvalidArgument)
		}
		candidate := pluginsdk.DatasetImportCandidate{Revision: "metadata", ExpectedDigest: "sha256:" + strings.Repeat("0", 64), URL: retrieval.ChecksumURL}
		if candidate.Validate() != nil {
			return fmt.Errorf("%w: rolling source requires a checksum URL", ErrInvalidArgument)
		}
		return nil
	}
	if retrieval.ChecksumURL != "" {
		return fmt.Errorf("%w: checksum URL requires rolling-sha256 mode", ErrInvalidArgument)
	}
	if retrieval.Revision != "" || retrieval.ExpectedDigest != "" || source.RefreshIntervalSeconds > 0 {
		candidate := pluginsdk.DatasetImportCandidate{Revision: retrieval.Revision, ExpectedDigest: retrieval.ExpectedDigest, URL: source.URL}
		if err := candidate.Validate(); err != nil {
			return fmt.Errorf("%w: refresh requires a pinned revision and checksum", ErrInvalidArgument)
		}
	}
	return nil
}

type DatasetSourceSummary struct {
	Source        pluginsdk.DatasetSource             `json:"source"`
	Retrieval     DatasetRetrieval                    `json:"retrieval"`
	CurrentDigest string                              `json:"current_digest,omitempty"`
	Failure       pluginsdk.DatasetFailureCode        `json:"failure,omitempty"`
	LastRefreshAt *time.Time                          `json:"last_refresh_at,omitempty"`
	Verification  *storage.DatasetVersionVerification `json:"verification,omitempty"`
}

type DatasetBindingRequest struct {
	SourceID        string                            `json:"source_id"`
	VersionDigest   string                            `json:"version_digest"`
	AgentID         string                            `json:"agent_id"`
	InstanceID      string                            `json:"instance_id"`
	Classifications []pluginsdk.DatasetClassification `json:"classifications"`
	Remove          bool                              `json:"remove,omitempty"`
}

type DatasetService struct {
	store        *storage.GormStore
	cfg          config.Config
	executor     *revision.Executor
	importGate   chan struct{}
	ctx          context.Context
	cancel       context.CancelFunc
	done         chan struct{}
	closeOnce    sync.Once
	refreshGates sync.Map
}

func NewDatasetService(cfg config.Config, store *storage.GormStore) *DatasetService {
	ctx, cancel := context.WithCancel(context.Background())
	service := &DatasetService{store: store, cfg: cfg, executor: newMutationExecutor(cfg, store), importGate: make(chan struct{}, 1), ctx: ctx, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(service.done)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				_ = service.RefreshDue(ctx, now)
			}
		}
	}()
	return service
}
func (s *DatasetService) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() { s.cancel(); <-s.done })
	return nil
}

func datasetSourceSummary(row storage.DatasetSourceRow) (DatasetSourceSummary, error) {
	var result DatasetSourceSummary
	if json.Unmarshal([]byte(row.SourceJSON), &result.Source) != nil || json.Unmarshal([]byte(row.RetrievalJSON), &result.Retrieval) != nil {
		return result, errors.New("dataset source metadata is corrupt")
	}
	result.CurrentDigest = row.CurrentDigest
	result.Failure = pluginsdk.DatasetFailureCode(row.LastFailure)
	result.LastRefreshAt = row.LastRefreshAt
	return result, nil
}

func (s *DatasetService) authorizedSource(ctx context.Context, authorization DatasetAuthorization, id string, write bool) (storage.DatasetSourceRow, error) {
	if authorization.ActorID == "" || (!authorization.Administrator && authorization.ResourceGroupID == "") || (write && !authorization.Manage) {
		return storage.DatasetSourceRow{}, errPluginHostDenied
	}
	row, err := s.store.GetDatasetSource(ctx, id)
	if err != nil {
		return row, err
	}
	if !authorization.Administrator && row.ResourceGroupID != authorization.ResourceGroupID {
		return storage.DatasetSourceRow{}, errPluginHostDenied
	}
	return row, nil
}
func (s *DatasetService) List(ctx context.Context, authorization DatasetAuthorization) ([]DatasetSourceSummary, error) {
	if authorization.ActorID == "" {
		return nil, errPluginHostDenied
	}
	rows, err := s.store.ListDatasetSources(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]DatasetSourceSummary, 0, len(rows))
	for _, row := range rows {
		if !authorization.Administrator && row.ResourceGroupID != authorization.ResourceGroupID {
			continue
		}
		summary, err := datasetSourceSummary(row)
		if err != nil {
			return nil, err
		}
		if row.CurrentDigest != "" {
			if version, err := s.store.GetDatasetVersion(ctx, row.ID, row.CurrentDigest); err == nil && version.VerificationJSON != "" {
				var evidence storage.DatasetVersionVerification
				if json.Unmarshal([]byte(version.VerificationJSON), &evidence) == nil {
					summary.Verification = &evidence
				}
			}
		}
		result = append(result, summary)
	}
	return result, nil
}

func (s *DatasetService) PutSource(ctx context.Context, authorization DatasetAuthorization, source pluginsdk.DatasetSource, retrieval DatasetRetrieval) error {
	if !authorization.Manage || authorization.ActorID == "" || authorization.ResourceGroupID == "" {
		return errPluginHostDenied
	}
	if source.Validate() != nil || retrieval.Validate(source) != nil {
		return fmt.Errorf("%w: dataset source/retrieval is invalid", ErrInvalidArgument)
	}
	if !authorization.Administrator && (retrieval.AllowPrivate || len(retrieval.RedirectHosts) > 0 || retrieval.Mode == datasetRetrievalRolling) {
		return errPluginHostDenied
	}
	current, err := s.store.GetDatasetSource(ctx, source.ID)
	if err == nil && !authorization.Administrator && current.ResourceGroupID != authorization.ResourceGroupID {
		return errPluginHostDenied
	}
	if err != nil && !errors.Is(err, storage.ErrDatasetNotFound) {
		return err
	}
	sourceJSON, _ := json.Marshal(source)
	retrievalJSON, _ := json.Marshal(retrieval)
	row := storage.DatasetSourceRow{ID: source.ID, ResourceGroupID: authorization.ResourceGroupID, SourceJSON: string(sourceJSON), RetrievalJSON: string(retrievalJSON)}
	if source.RefreshIntervalSeconds > 0 {
		next := time.Now().UTC()
		row.NextRefreshAt = &next
	}
	return s.store.PutDatasetSource(ctx, row)
}

func (s *DatasetService) Control(ctx context.Context, authorization DatasetAuthorization, request pluginsdk.DatasetControlRequest) (pluginsdk.DatasetControlResponse, error) {
	response := pluginsdk.DatasetControlResponse{OperationID: lifecycleID("dataset"), SourceID: request.SourceID}
	if err := request.Validate(); err != nil {
		return response, fmt.Errorf("%w: dataset control request is invalid", ErrInvalidArgument)
	}
	if request.Action == pluginsdk.DatasetControlPutSource {
		// Network authority is configured through the administrator endpoint;
		// this public plugin operation can create upload-only/manual sources.
		return response, s.PutSource(ctx, authorization, *request.Source, DatasetRetrieval{})
	}
	row, err := s.authorizedSource(ctx, authorization, request.SourceID, true)
	if err != nil {
		return response, err
	}
	if err := s.audit(ctx, authorization, request.SourceID, string(request.Action), "started"); err != nil {
		return response, err
	}
	switch request.Action {
	case pluginsdk.DatasetControlImport:
		_, err = s.prepare(ctx, row, *request.Candidate)
	case pluginsdk.DatasetControlRefresh:
		err = s.refresh(ctx, row, response.OperationID)
	case pluginsdk.DatasetControlActivate, pluginsdk.DatasetControlRollback:
		err = s.activate(ctx, row, request.VersionDigest, response.OperationID)
	case pluginsdk.DatasetControlDeleteVersion:
		err = s.store.DeleteDatasetVersion(ctx, request.SourceID, request.VersionDigest)
	case pluginsdk.DatasetControlDeleteSource:
		err = s.store.DeleteDatasetSource(ctx, request.SourceID)
	}
	result := "completed"
	if err != nil {
		result = "failed"
		var failure *datasets.Error
		if errors.As(err, &failure) {
			_ = s.store.RecordDatasetFailure(context.WithoutCancel(ctx), request.SourceID, failure.Code)
		}
	}
	auditErr := s.audit(ctx, authorization, request.SourceID, string(request.Action), result)
	return response, errors.Join(err, auditErr)
}

func (s *DatasetService) audit(ctx context.Context, authorization DatasetAuthorization, sourceID, action, result string) error {
	return s.store.AppendAuditEvent(ctx, storage.AuditEventRow{ID: lifecycleID("dataset-audit"), ActorID: authorization.ActorID, Action: "dataset." + action, TargetKind: "dataset", TargetID: sourceID, ResourceGroupID: authorization.ResourceGroupID, Result: result, MetadataJSON: "{}", CreatedAt: time.Now().UTC()})
}

func (s *DatasetService) prepare(ctx context.Context, row storage.DatasetSourceRow, candidate pluginsdk.DatasetImportCandidate, verification ...storage.DatasetVersionVerification) (pluginsdk.DatasetVersion, error) {
	if err := candidate.Validate(); err != nil {
		return pluginsdk.DatasetVersion{}, fmt.Errorf("%w: pinned dataset candidate is required", ErrInvalidArgument)
	}
	select {
	case s.importGate <- struct{}{}:
		defer func() { <-s.importGate }()
	case <-ctx.Done():
		return pluginsdk.DatasetVersion{}, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	summary, err := datasetSourceSummary(row)
	if err != nil {
		return pluginsdk.DatasetVersion{}, err
	}
	var payload []byte
	if candidate.ArtifactDigest != "" {
		payload, err = s.store.ReadDatasetUpload(ctx, row.ID, candidate.ArtifactDigest)
	} else {
		registered, _ := url.Parse(summary.Source.URL)
		requested, _ := url.Parse(candidate.URL)
		if registered == nil || requested == nil || registered.Scheme != requested.Scheme || !strings.EqualFold(registered.Host, requested.Host) {
			err = errPluginHostDenied
		} else {
			payload, err = fetchDatasetCandidate(ctx, candidate.URL, summary.Retrieval, candidate.ExpectedDigest)
		}
	}
	if err == nil {
		var index *datasets.Index
		source := summary.Source
		if candidate.URL != "" {
			source.URL = candidate.URL
		}
		index, err = datasets.Compile(ctx, datasets.Input{Source: source, Revision: candidate.Revision, FetchedAt: time.Now().UTC().Format(time.RFC3339Nano), ExpectedDigest: candidate.ExpectedDigest, Data: payload}, datasets.DefaultLimits())
		if err == nil {
			// A periodic re-fetch of unchanged pinned bytes does not manufacture
			// a new immutable version merely by changing FetchedAt.
			versions, listErr := s.store.ListDatasetVersions(ctx, row.ID)
			if listErr != nil {
				err = listErr
			} else {
				for _, existing := range versions {
					var version pluginsdk.DatasetVersion
					if json.Unmarshal([]byte(existing.VersionJSON), &version) == nil && version.RawDigest == candidate.ExpectedDigest && version.Revision == candidate.Revision {
						_ = s.recordRefresh(ctx, row.ID, summary.Source, "")
						return version, nil
					}
				}
			}
			if err == nil {
				var encoded []byte
				encoded, err = index.MarshalBinary()
				if err == nil {
					_, err = s.store.StoreDatasetVersion(ctx, index.Version(), encoded, verification...)
				}
				if err == nil {
					_ = s.recordRefresh(ctx, row.ID, summary.Source, "")
					return index.Version(), nil
				}
			}
		}
	}
	code := pluginsdk.DatasetFailureDownload
	var failure *datasets.Error
	if errors.As(err, &failure) {
		code = failure.Code
	}
	if errors.Is(err, errPluginHostDenied) {
		code = pluginsdk.DatasetFailureUnauthorized
	}
	_ = s.recordRefresh(context.WithoutCancel(ctx), row.ID, summary.Source, code)
	return pluginsdk.DatasetVersion{}, err
}
func (s *DatasetService) recordRefresh(ctx context.Context, id string, source pluginsdk.DatasetSource, failure pluginsdk.DatasetFailureCode) error {
	var next *time.Time
	if source.RefreshIntervalSeconds > 0 {
		value := time.Now().UTC().Add(time.Duration(source.RefreshIntervalSeconds) * time.Second)
		next = &value
	}
	return s.store.RecordDatasetRefresh(ctx, id, failure, next)
}

func (s *DatasetService) loadIndex(ctx context.Context, sourceID, digest string) (*datasets.Index, error) {
	row, encoded, err := s.store.ReadDatasetIndex(ctx, sourceID, digest)
	if err != nil {
		return nil, err
	}
	index, err := datasets.LoadIndex(ctx, encoded, datasets.DefaultLimits())
	if err != nil {
		return nil, err
	}
	var expected pluginsdk.DatasetVersion
	if json.Unmarshal([]byte(row.VersionJSON), &expected) != nil || expected != index.Version() {
		return nil, errors.New("stored dataset manifest differs from index")
	}
	return index, nil
}

func validateDatasetBoundClasses(ctx context.Context, index *datasets.Index, classes []pluginsdk.DatasetClassification) error {
	if len(classes) == 0 || len(classes) > pluginsdk.DatasetMaxQueryClassifications {
		return fmt.Errorf("%w: dataset classifications exceed binding budget", ErrInvalidArgument)
	}
	wanted := map[string]bool{}
	for _, class := range classes {
		if err := class.Validate(); err != nil {
			return err
		}
		wanted[string(class.Kind)+":"+class.Name] = true
	}
	request := pluginsdk.DatasetCatalogRequest{SourceID: index.Version().SourceID, VersionDigest: index.Version().Digest, Limit: pluginsdk.DatasetMaxCatalogPage}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		page, err := index.Catalog(request)
		if err != nil {
			return err
		}
		for _, entry := range page.Classifications {
			delete(wanted, string(entry.Classification.Kind)+":"+entry.Classification.Name)
		}
		if len(wanted) == 0 {
			return nil
		}
		if page.NextCursor == "" {
			return &datasets.Error{Code: pluginsdk.DatasetFailureMissingClassification, Detail: "a bound classification is absent"}
		}
		request.Cursor = page.NextCursor
	}
}

func (s *DatasetService) activate(ctx context.Context, row storage.DatasetSourceRow, digest, operationID string) error {
	index, err := s.loadIndex(ctx, row.ID, digest)
	if err != nil {
		return err
	}
	bindings, err := s.store.DatasetBindings(ctx, row.ID)
	if err != nil {
		return err
	}
	for _, binding := range bindings {
		var classes []pluginsdk.DatasetClassification
		if json.Unmarshal([]byte(binding.ClassificationsJSON), &classes) != nil {
			return errors.New("dataset binding is corrupt")
		}
		if err := validateDatasetBoundClasses(ctx, index, classes); err != nil {
			return err
		}
	}
	ids := storage.DatasetTargetIDs(bindings)
	if len(ids) == 0 {
		return s.store.ActivateDatasetVersion(ctx, row.ID, digest, map[string]int64{})
	}
	_, err = s.executor.Execute(ctx, revision.MutationRequest{OperationID: operationID, Kind: "dataset.activate", Request: map[string]string{"source_id": row.ID, "version_digest": digest}, Targets: configMutationTargets(s.cfg, ids, nil), ResourceState: datasetBindingResourceState(row.ID), Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
		if err := tx.LockDatasetSource(ctx, row.ID); err != nil {
			return err
		}
		latest, err := tx.DatasetBindings(ctx, row.ID)
		if err != nil {
			return err
		}
		for _, binding := range latest {
			var classes []pluginsdk.DatasetClassification
			if json.Unmarshal([]byte(binding.ClassificationsJSON), &classes) != nil {
				return errors.New("dataset binding is corrupt")
			}
			if err := validateDatasetBoundClasses(ctx, index, classes); err != nil {
				return err
			}
		}
		return tx.ActivateDatasetVersion(ctx, row.ID, digest, revisions)
	}})
	return err
}

func (s *DatasetService) Bind(ctx context.Context, authorization DatasetAuthorization, request DatasetBindingRequest) error {
	if _, err := s.authorizedSource(ctx, authorization, request.SourceID, true); err != nil {
		return err
	}
	if !authorization.Administrator {
		return errPluginHostDenied
	}
	if pluginsdk.ValidatePolicyIdentity(request.AgentID) != nil || pluginsdk.ValidatePolicyIdentity(request.InstanceID) != nil {
		return ErrInvalidArgument
	}
	if !request.Remove {
		instance, found, err := s.store.GetPluginInstance(ctx, request.InstanceID)
		if err != nil {
			return err
		}
		if !found || instance.ResourceGroupID == "" {
			return errPluginHostDenied
		}
		index, err := s.loadIndex(ctx, request.SourceID, request.VersionDigest)
		if err != nil {
			return err
		}
		if err := validateDatasetBoundClasses(ctx, index, request.Classifications); err != nil {
			return err
		}
	}
	classes, _ := json.Marshal(request.Classifications)
	_, err := s.executor.Execute(ctx, revision.MutationRequest{OperationID: lifecycleID("dataset-binding"), Kind: "dataset.bind", Request: request, Targets: configMutationTargets(s.cfg, []string{request.AgentID}, nil), ResourceState: datasetBindingResourceState(request.SourceID), Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
		if request.Remove {
			return tx.RemoveDatasetBinding(ctx, request.SourceID, request.AgentID, request.InstanceID)
		}
		return tx.PutDatasetBinding(ctx, storage.DatasetBindingRow{AgentID: request.AgentID, InstanceID: request.InstanceID, SourceID: request.SourceID, VersionDigest: request.VersionDigest, ClassificationsJSON: string(classes), Revision: revisions[request.AgentID]})
	}})
	return err
}

func (s *DatasetService) Catalog(ctx context.Context, authorization DatasetAuthorization, request pluginsdk.DatasetCatalogRequest) (pluginsdk.DatasetCatalogResponse, error) {
	if _, err := s.authorizedSource(ctx, authorization, request.SourceID, false); err != nil {
		return pluginsdk.DatasetCatalogResponse{}, err
	}
	if err := request.Validate(); err != nil {
		return pluginsdk.DatasetCatalogResponse{}, err
	}
	if request.VersionDigest == "" {
		return s.store.DatasetHistory(ctx, request)
	}
	index, err := s.loadIndex(ctx, request.SourceID, request.VersionDigest)
	if err != nil {
		return pluginsdk.DatasetCatalogResponse{}, err
	}
	return index.Catalog(request)
}
func (s *DatasetService) Status(ctx context.Context, authorization DatasetAuthorization, request pluginsdk.DatasetStatusRequest) (pluginsdk.DatasetStatusResponse, error) {
	if err := request.Validate(); err != nil {
		return pluginsdk.DatasetStatusResponse{}, err
	}
	if _, err := s.authorizedSource(ctx, authorization, request.SourceID, false); err != nil {
		return pluginsdk.DatasetStatusResponse{}, err
	}
	return s.store.DatasetNodeStatus(ctx, request.SourceID, request.NodeID, time.Now().UTC())
}
func (s *DatasetService) Upload(ctx context.Context, authorization DatasetAuthorization, sourceID string, reader io.Reader) (string, error) {
	if _, err := s.authorizedSource(ctx, authorization, sourceID, true); err != nil {
		return "", err
	}
	payload, err := io.ReadAll(io.LimitReader(reader, pluginsdk.DatasetMaxDownloadBytes+1))
	if err != nil {
		return "", err
	}
	return s.store.StoreDatasetUpload(ctx, sourceID, payload)
}

// RefreshDue resolves, prepares and activates through a coherent revision
// mutation. A failed refresh retains all active pointers and consumer bindings.
func (s *DatasetService) RefreshDue(ctx context.Context, now time.Time) error {
	rows, err := s.store.ListDatasetSources(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, row := range rows {
		if row.NextRefreshAt == nil || row.NextRefreshAt.After(now) {
			continue
		}
		summary, err := datasetSourceSummary(row)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if summary.Source.RefreshIntervalSeconds == 0 {
			continue
		}
		_, err = s.Control(ctx, DatasetAuthorization{ActorID: "system/dataset-refresh", ResourceGroupID: row.ResourceGroupID, Manage: true}, pluginsdk.DatasetControlRequest{Action: pluginsdk.DatasetControlRefresh, SourceID: row.ID})
		if err != nil {
			failures = append(failures, err)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	_ = s.store.PruneDatasetUploads(ctx, now.Add(-24*time.Hour))
	return errors.Join(failures...)
}

func (s *DatasetService) ResolveAgentDatasetArtifact(ctx context.Context, agentID string, revision int64, snapshotDigest, artifactID string) (storage.GenerationArtifactRow, error) {
	return s.store.ResolveAgentRevisionDatasetArtifact(ctx, agentID, revision, snapshotDigest, artifactID)
}

func validateSnapshotDatasets(snapshot storage.Snapshot) error {
	seen := map[string]bool{}
	for _, entry := range snapshot.Datasets {
		if entry.Version.Validate() != nil || seen[entry.Version.SourceID] || entry.Artifact.Kind != storage.DatasetArtifactKind || entry.Artifact.ID != "dataset-"+entry.Artifact.SHA256 || entry.Version.IndexDigest != "sha256:"+entry.Artifact.SHA256 || entry.Version.IndexBytes != entry.Artifact.SizeBytes || len(entry.Bindings) == 0 || len(entry.Bindings) > 4096 {
			return revision.NewError(revision.ErrorCodeInvalidRequest, "dataset snapshot identity or budget is invalid", nil)
		}
		seen[entry.Version.SourceID] = true
		for _, binding := range entry.Bindings {
			if pluginsdk.ValidatePolicyIdentity(binding.InstanceID) != nil || len(binding.Classifications) == 0 || len(binding.Classifications) > pluginsdk.DatasetMaxQueryClassifications {
				return revision.NewError(revision.ErrorCodeInvalidRequest, "dataset binding is invalid", nil)
			}
			for _, class := range binding.Classifications {
				if err := class.Validate(); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
