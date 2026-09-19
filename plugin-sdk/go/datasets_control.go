package pluginsdk

import (
	"context"
	"encoding/json"
	"errors"
)

const HostRuntimeDatasetOpen = "dataset.open"

// DatasetOpenRequest asks Host to issue an instance/generation-bound reference
// for an already prepared version. It cannot fetch or activate a new version.
type DatasetOpenRequest struct {
	SourceID      string `json:"source_id"`
	VersionDigest string `json:"version_digest"`
}

func (request DatasetOpenRequest) Validate() error {
	if err := ValidatePolicyIdentity(request.SourceID); err != nil {
		return err
	}
	return validateDatasetDigest(request.VersionDigest)
}

type DatasetControlAction string

const (
	DatasetControlPutSource     DatasetControlAction = "put-source"
	DatasetControlRefresh       DatasetControlAction = "refresh"
	DatasetControlImport        DatasetControlAction = "import"
	DatasetControlActivate      DatasetControlAction = "activate"
	DatasetControlRollback      DatasetControlAction = "rollback"
	DatasetControlDeleteSource  DatasetControlAction = "delete-source"
	DatasetControlDeleteVersion DatasetControlAction = "delete-version"
)

// DatasetImportCandidate references a complete uploaded artifact or a pinned
// remote revision, never inline bytes. Host owns authorized retrieval, digest
// checking, dependency closure, parsing, indexing, and atomic preparation.
// Revision is provenance; its syntax cannot prove upstream immutability.
type DatasetImportCandidate struct {
	Revision       string `json:"revision"`
	ExpectedDigest string `json:"expected_digest"`
	ArtifactDigest string `json:"artifact_digest,omitempty"`
	URL            string `json:"url,omitempty"`
}

func (candidate DatasetImportCandidate) Validate() error {
	if err := validateDatasetText(candidate.Revision, 256); err != nil {
		return err
	}
	if err := validateDatasetDigest(candidate.ExpectedDigest); err != nil {
		return err
	}
	if (candidate.ArtifactDigest == "") == (candidate.URL == "") {
		return errors.New("dataset import requires exactly one artifact or remote URL")
	}
	if candidate.ArtifactDigest != "" {
		if err := validateDatasetDigest(candidate.ArtifactDigest); err != nil {
			return err
		}
		if candidate.ArtifactDigest != candidate.ExpectedDigest {
			return errors.New("dataset artifact and expected raw digest differ")
		}
		return nil
	}
	return validateDatasetURL(candidate.URL)
}

// DatasetControlRequest requires dataset.manage and an administrator grant.
// Activate and rollback prepare a coherent rule/data snapshot before switching.
// Deletes must be rejected while referenced by rules, sessions, last-good node
// snapshots, or rollback. A failed candidate must preserve the active version.
type DatasetControlRequest struct {
	Action        DatasetControlAction    `json:"action"`
	SourceID      string                  `json:"source_id"`
	Source        *DatasetSource          `json:"source,omitempty"`
	Candidate     *DatasetImportCandidate `json:"candidate,omitempty"`
	VersionDigest string                  `json:"version_digest,omitempty"`
}

func (request DatasetControlRequest) Validate() error {
	if err := ValidatePolicyIdentity(request.SourceID); err != nil {
		return err
	}
	if request.Action != DatasetControlPutSource && request.Source != nil {
		return errors.New("dataset source specification requires put-source")
	}
	if request.Action != DatasetControlImport && request.Candidate != nil {
		return errors.New("dataset candidate requires import")
	}
	needsVersion := request.Action == DatasetControlActivate || request.Action == DatasetControlRollback || request.Action == DatasetControlDeleteVersion
	if !needsVersion && request.VersionDigest != "" {
		return errors.New("dataset action does not accept a version digest")
	}
	switch request.Action {
	case DatasetControlPutSource:
		if request.Source == nil || request.Source.ID != request.SourceID {
			return errors.New("dataset put-source requires a matching source")
		}
		return request.Source.Validate()
	case DatasetControlImport:
		if request.Candidate == nil {
			return errors.New("dataset import requires a candidate")
		}
		return request.Candidate.Validate()
	case DatasetControlActivate, DatasetControlRollback, DatasetControlDeleteVersion:
		return validateDatasetDigest(request.VersionDigest)
	case DatasetControlRefresh, DatasetControlDeleteSource:
		return nil
	default:
		return errors.New("unsupported dataset control action")
	}
}

// DatasetControlResponse acknowledges a Host-owned operation, not node-wide
// application. Actual desired/applied/last-good state comes from dataset.status.
type DatasetControlResponse struct {
	OperationID string `json:"operation_id"`
	SourceID    string `json:"source_id"`
}

func (response DatasetControlResponse) Validate() error {
	if err := ValidatePolicyIdentity(response.OperationID); err != nil {
		return err
	}
	return ValidatePolicyIdentity(response.SourceID)
}

type DatasetNodePhase string

const (
	DatasetNodeUnavailable DatasetNodePhase = "unavailable"
	DatasetNodePreparing   DatasetNodePhase = "preparing"
	DatasetNodeApplied     DatasetNodePhase = "applied"
	DatasetNodeOffline     DatasetNodePhase = "offline"
	DatasetNodeFailed      DatasetNodePhase = "failed"
)

type DatasetFailureCode string

const (
	DatasetFailureDownload              DatasetFailureCode = "download-failed"
	DatasetFailureDigest                DatasetFailureCode = "digest-mismatch"
	DatasetFailureInvalidData           DatasetFailureCode = "invalid-data"
	DatasetFailureMissingClassification DatasetFailureCode = "missing-classification"
	DatasetFailureBudget                DatasetFailureCode = "budget-exceeded"
	DatasetFailureUnauthorized          DatasetFailureCode = "unauthorized"
	DatasetFailureInUse                 DatasetFailureCode = "in-use"
)

func (code DatasetFailureCode) Validate() error {
	switch code {
	case DatasetFailureDownload, DatasetFailureDigest, DatasetFailureInvalidData, DatasetFailureMissingClassification,
		DatasetFailureBudget, DatasetFailureUnauthorized, DatasetFailureInUse:
		return nil
	default:
		return errors.New("invalid dataset failure code")
	}
}

type DatasetStatusRequest struct {
	SourceID string `json:"source_id"`
	NodeID   string `json:"node_id"`
}

func (request DatasetStatusRequest) Validate() error {
	if err := ValidatePolicyIdentity(request.SourceID); err != nil {
		return err
	}
	return ValidatePolicyIdentity(request.NodeID)
}

// DatasetStatusResponse records an actual node acknowledgement. An offline or
// failed node can retain its applied/last-good version while desired advances.
// Generation describes Applied, never a merely desired candidate. There is no
// synthesized global "all applied" flag or arbitrary failure text.
// Preparing with an empty Desired means removal is pending: Applied, LastGood
// and Generation still describe the complete, actually running old snapshot.
// After removal is acknowledged, unavailable has no Applied or Generation;
// LastGood may retain the historical successful version.
type DatasetStatusResponse struct {
	SourceID   string             `json:"source_id"`
	NodeID     string             `json:"node_id"`
	Desired    string             `json:"desired,omitempty"`
	Applied    string             `json:"applied,omitempty"`
	LastGood   string             `json:"last_good,omitempty"`
	Generation string             `json:"generation,omitempty"`
	Phase      DatasetNodePhase   `json:"phase"`
	Failure    DatasetFailureCode `json:"failure,omitempty"`
}

func (response DatasetStatusResponse) Validate() error {
	if err := (DatasetStatusRequest{SourceID: response.SourceID, NodeID: response.NodeID}).Validate(); err != nil {
		return err
	}
	for _, digest := range []string{response.Desired, response.Applied, response.LastGood} {
		if digest != "" {
			if err := validateDatasetDigest(digest); err != nil {
				return err
			}
		}
	}
	if (response.Applied == "") != (response.Generation == "") {
		return errors.New("dataset applied version and generation must be present together")
	}
	if response.Generation != "" {
		if err := ValidatePolicyIdentity(response.Generation); err != nil {
			return err
		}
		if response.Applied != response.LastGood {
			return errors.New("dataset applied version must be the last successful version")
		}
	}
	if response.Phase == DatasetNodeFailed {
		if err := response.Failure.Validate(); err != nil {
			return err
		}
	} else if response.Failure != "" {
		return errors.New("dataset failure code requires failed node phase")
	}
	switch response.Phase {
	case DatasetNodeApplied:
		if response.Applied == "" || response.Desired != response.Applied {
			return errors.New("dataset applied phase requires acknowledgement of desired version")
		}
	case DatasetNodePreparing:
		if response.Desired == "" && (response.Applied == "" || response.LastGood == "" || response.Generation == "") {
			return errors.New("dataset preparing phase requires a desired version or a complete applied snapshot pending removal")
		}
	case DatasetNodeUnavailable:
		if response.Applied != "" {
			return errors.New("unavailable dataset cannot claim an applied version")
		}
	case DatasetNodeOffline, DatasetNodeFailed:
	default:
		return errors.New("invalid dataset node phase")
	}
	return nil
}

// DatasetCatalogRequest pages either version history (empty VersionDigest) or
// the classifications of one immutable version. Cursor is an opaque Host token.
type DatasetCatalogRequest struct {
	SourceID      string `json:"source_id"`
	VersionDigest string `json:"version_digest,omitempty"`
	Cursor        string `json:"cursor,omitempty"`
	Limit         int    `json:"limit"`
}

func (request DatasetCatalogRequest) Validate() error {
	if err := ValidatePolicyIdentity(request.SourceID); err != nil {
		return err
	}
	if request.VersionDigest != "" {
		if err := validateDatasetDigest(request.VersionDigest); err != nil {
			return err
		}
	}
	if request.Limit < 1 || request.Limit > DatasetMaxCatalogPage {
		return errors.New("dataset catalog requires a bounded page size")
	}
	return validateDatasetCursor(request.Cursor)
}

func validateDatasetCursor(cursor string) error {
	if cursor == "" {
		return nil
	}
	if !datasetHandlePattern.MatchString(cursor) {
		return errors.New("invalid opaque dataset catalog cursor")
	}
	return nil
}

// DatasetCatalogEntry reports metadata, not raw CIDRs, regexes or domain lists.
// Coverage is per classification: country IPv6 coverage cannot imply province
// IPv6 coverage. Attributes enumerate supported typed predicates.
type DatasetCatalogEntry struct {
	Classification DatasetClassification  `json:"classification"`
	DisplayName    string                 `json:"display_name"`
	EntryCount     int                    `json:"entry_count"`
	Coverage       DatasetAddressCoverage `json:"coverage"`
}

func (entry DatasetCatalogEntry) Validate() error {
	if err := entry.Classification.Validate(); err != nil {
		return err
	}
	if err := validateDatasetText(entry.DisplayName, 128); err != nil {
		return err
	}
	if entry.EntryCount < 1 || entry.EntryCount > DatasetMaxEntries {
		return errors.New("invalid dataset catalog entry count")
	}
	for _, attribute := range entry.Classification.Attributes {
		if attribute.Negate {
			return errors.New("dataset catalog attributes describe values, not negated filters")
		}
	}
	return entry.Coverage.Validate()
}

type DatasetCatalogResponse struct {
	SourceID        string                `json:"source_id"`
	VersionDigest   string                `json:"version_digest,omitempty"`
	Versions        []DatasetVersion      `json:"versions,omitempty"`
	Classifications []DatasetCatalogEntry `json:"classifications,omitempty"`
	NextCursor      string                `json:"next_cursor,omitempty"`
}

func (response DatasetCatalogResponse) Validate() error {
	if err := ValidatePolicyIdentity(response.SourceID); err != nil {
		return err
	}
	if err := validateDatasetCursor(response.NextCursor); err != nil {
		return err
	}
	if len(response.Versions) > DatasetMaxCatalogPage || len(response.Classifications) > DatasetMaxCatalogPage {
		return errors.New("dataset catalog response exceeds page bound")
	}
	seen := make(map[string]bool)
	if response.VersionDigest == "" {
		if len(response.Classifications) != 0 {
			return errors.New("dataset classification catalog requires a version")
		}
		for _, version := range response.Versions {
			if err := version.Validate(); err != nil {
				return err
			}
			if version.SourceID != response.SourceID || seen[version.Digest] {
				return errors.New("dataset history contains a foreign or duplicate version")
			}
			seen[version.Digest] = true
		}
	} else {
		if err := validateDatasetDigest(response.VersionDigest); err != nil {
			return err
		}
		if len(response.Versions) != 0 {
			return errors.New("dataset classification page cannot include version history")
		}
		for _, entry := range response.Classifications {
			if err := entry.Validate(); err != nil {
				return err
			}
			key := string(entry.Classification.Kind) + ":" + entry.Classification.Name
			if seen[key] {
				return errors.New("dataset catalog contains a duplicate classification")
			}
			seen[key] = true
		}
	}
	return validateDatasetFrame(response, PluginHostPayloadMaxBytes)
}

func (response DatasetCatalogResponse) ValidateFor(request DatasetCatalogRequest) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if err := response.Validate(); err != nil {
		return err
	}
	if response.SourceID != request.SourceID || response.VersionDigest != request.VersionDigest || len(response.Versions)+len(response.Classifications) > request.Limit {
		return errors.New("dataset catalog response differs from the requested source, version, or page size")
	}
	if response.NextCursor != "" && (response.NextCursor == request.Cursor || len(response.Versions)+len(response.Classifications) == 0) {
		return errors.New("dataset catalog cursor did not advance")
	}
	return nil
}

func (client *HostRuntimeClient) OpenDataset(ctx context.Context, request DatasetOpenRequest) (DatasetReference, error) {
	var response DatasetReference
	if err := client.callDataset(ctx, HostRuntimeDatasetOpen, "", request, &response); err != nil {
		return DatasetReference{}, err
	}
	if response.SourceID != request.SourceID || response.VersionDigest != request.VersionDigest {
		return DatasetReference{}, errors.New("opened dataset differs from requested snapshot")
	}
	return response, nil
}

func (client *HostRuntimeClient) ControlDataset(ctx context.Context, operationID string, request DatasetControlRequest) (DatasetControlResponse, error) {
	var response DatasetControlResponse
	if err := ValidatePolicyIdentity(operationID); err != nil {
		return response, err
	}
	if err := client.callDataset(ctx, HostRuntimeDatasetControl, operationID, request, &response); err != nil {
		return DatasetControlResponse{}, err
	}
	if response.SourceID != request.SourceID || response.OperationID != operationID {
		return DatasetControlResponse{}, errors.New("dataset control acknowledgement differs from request")
	}
	return response, nil
}

func (client *HostRuntimeClient) DatasetStatus(ctx context.Context, request DatasetStatusRequest) (DatasetStatusResponse, error) {
	var response DatasetStatusResponse
	if err := client.callDataset(ctx, HostRuntimeDatasetStatus, "", request, &response); err != nil {
		return DatasetStatusResponse{}, err
	}
	if response.SourceID != request.SourceID || response.NodeID != request.NodeID {
		return DatasetStatusResponse{}, errors.New("dataset status response differs from requested source or node")
	}
	return response, nil
}

func (client *HostRuntimeClient) DatasetCatalog(ctx context.Context, request DatasetCatalogRequest) (DatasetCatalogResponse, error) {
	var response DatasetCatalogResponse
	if err := client.callDataset(ctx, HostRuntimeDatasetCatalog, "", request, &response); err != nil {
		return DatasetCatalogResponse{}, err
	}
	if err := response.ValidateFor(request); err != nil {
		return DatasetCatalogResponse{}, err
	}
	return response, nil
}

func (client *HostRuntimeClient) callDataset(ctx context.Context, operation, operationID string, request interface{ Validate() error }, response interface{ Validate() error }) error {
	if err := request.Validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return err
	}
	if err := client.Call(ctx, HostRuntimeCall{Operation: operation, OperationID: operationID, Payload: payload}, response); err != nil {
		return err
	}
	return response.Validate()
}
