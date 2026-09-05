package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/datasets"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/revision"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

const (
	datasetRetrievalPinned        = "pinned"
	datasetRetrievalRolling       = "rolling-sha256"
	datasetChecksumMaxBytes int64 = 4096
)

func parseDatasetChecksum(metadata []byte, dataURL string) (string, error) {
	invalid := func() (string, error) {
		return "", &datasets.Error{Code: sdk.DatasetFailureInvalidData, Detail: "checksum metadata must contain exactly one SHA256 for the dataset filename"}
	}
	if len(metadata) == 0 || int64(len(metadata)) > datasetChecksumMaxBytes {
		return invalid()
	}
	for _, b := range metadata {
		if (b < 32 && b != '\r' && b != '\n' && b != '\t') || b > 126 {
			return invalid()
		}
	}
	line := strings.TrimSpace(string(metadata))
	if strings.ContainsAny(line, "\r\n") {
		return invalid()
	}
	fields := strings.Fields(line)
	if len(fields) < 1 || len(fields) > 2 || len(fields[0]) != 64 {
		return invalid()
	}
	if _, err := hex.DecodeString(fields[0]); err != nil {
		return invalid()
	}
	if len(fields) == 2 {
		parsed, err := url.Parse(dataURL)
		if err != nil {
			return invalid()
		}
		filename := path.Base(parsed.Path)
		actual := strings.TrimPrefix(fields[1], "*")
		if filename == "." || filename == "/" || (actual != filename && actual != "./"+filename) {
			return invalid()
		}
	}
	return "sha256:" + strings.ToLower(fields[0]), nil
}

func (s *DatasetService) refresh(ctx context.Context, row storage.DatasetSourceRow, operationID string) (resultErr error) {
	value, _ := s.refreshGates.LoadOrStore(row.ID, make(chan struct{}, 1))
	gate := value.(chan struct{})
	select {
	case gate <- struct{}{}:
		defer func() { <-gate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	// Reload after the per-source gate: a queued refresh must use current
	// administrator configuration, not a stale pinned or network authority.
	var err error
	row, err = s.store.GetDatasetSource(ctx, row.ID)
	if err != nil {
		return err
	}
	summary, err := datasetSourceSummary(row)
	if err != nil {
		return err
	}
	if summary.Retrieval.Validate(summary.Source) != nil {
		return ErrInvalidArgument
	}
	defer func() {
		if resultErr != nil {
			code := sdk.DatasetFailureDownload
			var failure *datasets.Error
			if errors.As(resultErr, &failure) {
				code = failure.Code
			}
			if errors.Is(resultErr, errPluginHostDenied) {
				code = sdk.DatasetFailureUnauthorized
			}
			updateCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			resultErr = errors.Join(resultErr, s.recordRefresh(updateCtx, row.ID, summary.Source, code))
		}
	}()
	candidate := sdk.DatasetImportCandidate{URL: summary.Source.URL, Revision: summary.Retrieval.Revision, ExpectedDigest: summary.Retrieval.ExpectedDigest}
	var evidence []storage.DatasetVersionVerification
	if summary.Retrieval.Mode == datasetRetrievalRolling {
		metadata, err := fetchDatasetPayload(ctx, summary.Retrieval.ChecksumURL, summary.Retrieval, datasetChecksumMaxBytes, 15*time.Second)
		if err != nil {
			return err
		}
		digest, err := parseDatasetChecksum(metadata, summary.Source.URL)
		if err != nil {
			return err
		}
		candidate.ExpectedDigest = digest
		candidate.Revision = "checksum-" + digest
		sum := sha256.Sum256(metadata)
		evidence = []storage.DatasetVersionVerification{{Mode: datasetRetrievalRolling, ChecksumURL: summary.Retrieval.ChecksumURL, ChecksumDigest: "sha256:" + hex.EncodeToString(sum[:]), ExpectedDigest: digest, ResolvedAt: time.Now().UTC().Format(time.RFC3339Nano)}}
	}
	version, err := s.prepare(ctx, row, candidate, evidence...)
	if err != nil {
		return err
	}
	current, err := s.store.GetDatasetSource(ctx, row.ID)
	if err != nil {
		return err
	}
	if current.SourceJSON != row.SourceJSON || current.RetrievalJSON != row.RetrievalJSON {
		return errors.New("dataset source authority changed during refresh")
	}
	// Candidate validation and all consumer binding changes use the existing
	// revision mutation; failures preserve the prior active pointer and indices.
	return s.activate(ctx, current, version.Digest, operationID)
}

func datasetBindingResourceState(sourceID string) revision.ResourceStateReader {
	return func(ctx context.Context, tx *storage.GormStore, _ revision.Target) (any, error) {
		source, err := tx.GetDatasetSource(ctx, sourceID)
		if err != nil {
			return nil, err
		}
		bindings, err := tx.DatasetBindings(ctx, sourceID)
		if err != nil {
			return nil, err
		}
		for i := range bindings {
			bindings[i].Revision = 0
		}
		return struct {
			Current  string                      `json:"current"`
			Bindings []storage.DatasetBindingRow `json:"bindings"`
		}{Current: source.CurrentDigest, Bindings: bindings}, nil
	}
}
