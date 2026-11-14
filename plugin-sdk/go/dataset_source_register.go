package pluginsdk

import (
	"context"
	"errors"
	"net/netip"
	"net/url"
)

// HostRuntimeDatasetSourceRegister is the plugin-facing remote dataset source
// registration operation. It requires the signed and granted
// dataset.source-register capability. The Host retains all network authority:
// a request selects only the address family it names (https) and the update
// policy, never private-network access, redirect targets, or credentials.
const HostRuntimeDatasetSourceRegister = "dataset.source-register"

// Retrieval modes a plugin may request for a remote dataset source.
const (
	DatasetRetrievalPinned        = "pinned"
	DatasetRetrievalRollingSHA256 = "rolling-sha256"
)

// DatasetSourceRetrieval is the update policy a plugin requests for a remote
// dataset source. Pinned names one immutable Revision with its ExpectedDigest;
// rolling-sha256 names a ChecksumURL the Host resolves before each download.
// The request type deliberately has no private-network, redirect-host or
// credential fields; the Host adds none.
type DatasetSourceRetrieval struct {
	Mode           string `json:"mode"`
	ChecksumURL    string `json:"checksum_url,omitempty"`
	Revision       string `json:"revision,omitempty"`
	ExpectedDigest string `json:"expected_digest,omitempty"`
}

func (retrieval DatasetSourceRetrieval) Validate() error {
	switch retrieval.Mode {
	case DatasetRetrievalPinned:
		if retrieval.ChecksumURL != "" {
			return errors.New("pinned dataset retrieval cannot carry a checksum URL")
		}
		if err := validateDatasetText(retrieval.Revision, 256); err != nil {
			return err
		}
		return validateDatasetDigest(retrieval.ExpectedDigest)
	case DatasetRetrievalRollingSHA256:
		if retrieval.Revision != "" || retrieval.ExpectedDigest != "" {
			return errors.New("rolling-sha256 dataset retrieval cannot carry pinned fields")
		}
		return validateDatasetRemoteURL(retrieval.ChecksumURL)
	default:
		return errors.New("unsupported dataset source retrieval mode")
	}
}

// validateDatasetRemoteURL requires a literal https destination without
// credentials or fragments. An IP-literal host must be public; DNS names are
// authorized again per dial by the Host fetch authority.
func validateDatasetRemoteURL(raw string) error {
	if err := validateDatasetURL(raw); err != nil {
		return err
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" {
		return errors.New("dataset remote source requires https transport")
	}
	if address, err := netip.ParseAddr(parsed.Hostname()); err == nil {
		address = address.Unmap()
		if address.Zone() != "" || !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsMulticast() {
			return errors.New("dataset remote source cannot name a private or special-use address")
		}
	}
	return nil
}

// DatasetSourceRegisterRequest asks the Host to create or replace one remote
// dataset source inside the caller's resource group and to prepare its first
// version. Source.URL and every retrieval URL must be https; the Host
// re-verifies that bound before storing or fetching anything.
type DatasetSourceRegisterRequest struct {
	Source    DatasetSource          `json:"source"`
	Retrieval DatasetSourceRetrieval `json:"retrieval"`
}

func (request DatasetSourceRegisterRequest) Validate() error {
	if err := request.Source.Validate(); err != nil {
		return err
	}
	if err := validateDatasetRemoteURL(request.Source.URL); err != nil {
		return err
	}
	return request.Retrieval.Validate()
}

// DatasetSourceRegisterResponse acknowledges registration and reports the
// immutable version the Host prepared and activated for the source.
type DatasetSourceRegisterResponse struct {
	SourceID string         `json:"source_id"`
	Version  DatasetVersion `json:"version"`
}

func (response DatasetSourceRegisterResponse) Validate() error {
	if err := ValidatePolicyIdentity(response.SourceID); err != nil {
		return err
	}
	if err := response.Version.Validate(); err != nil {
		return err
	}
	if response.Version.SourceID != response.SourceID {
		return errors.New("registered dataset version belongs to another source")
	}
	return nil
}

func (response DatasetSourceRegisterResponse) ValidateFor(request DatasetSourceRegisterRequest) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if err := response.Validate(); err != nil {
		return err
	}
	if response.SourceID != request.Source.ID {
		return errors.New("dataset source registration acknowledgement differs from the request")
	}
	return nil
}

// RegisterDatasetSource registers a remote dataset source and returns the
// version the Host prepared and activated. The mutation is durable and
// idempotent under operationID, matching ControlDataset semantics.
func (client *HostRuntimeClient) RegisterDatasetSource(ctx context.Context, operationID string, request DatasetSourceRegisterRequest) (DatasetSourceRegisterResponse, error) {
	var response DatasetSourceRegisterResponse
	if err := ValidatePolicyIdentity(operationID); err != nil {
		return response, err
	}
	if err := client.callDataset(ctx, HostRuntimeDatasetSourceRegister, operationID, request, &response); err != nil {
		return DatasetSourceRegisterResponse{}, err
	}
	if err := response.ValidateFor(request); err != nil {
		return DatasetSourceRegisterResponse{}, err
	}
	return response, nil
}
