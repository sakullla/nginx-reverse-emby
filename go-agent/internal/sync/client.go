package sync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type Snapshot = model.Snapshot

type ClientConfig struct {
	MasterURL      string
	AgentToken     string
	AgentID        string
	AgentName      string
	CurrentVersion string
	Platform       string
}

type Client struct {
	cfg    ClientConfig
	client *http.Client
}

type SyncRequest struct {
	CurrentRevision           int
	LastApplyRevision         int
	LastApplyStatus           string
	LastApplyMessage          string
	ManagedCertificateReports []model.ManagedCertificateReport
}

func NewClient(cfg ClientConfig, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	cfg.MasterURL = strings.TrimRight(cfg.MasterURL, "/")
	return &Client{cfg: cfg, client: httpClient}
}

func (c *Client) Sync(ctx context.Context, request SyncRequest) (Snapshot, error) {
	payload := struct {
		Name                      string                           `json:"name"`
		AgentID                   string                           `json:"agent_id"`
		CurrentRevision           int                              `json:"current_revision"`
		LastApplyRevision         int                              `json:"last_apply_revision"`
		LastApplyStatus           string                           `json:"last_apply_status"`
		LastApplyMessage          string                           `json:"last_apply_message"`
		ManagedCertificateReports []model.ManagedCertificateReport `json:"managed_certificate_reports"`
		Version                   string                           `json:"version"`
		Platform                  string                           `json:"platform"`
	}{
		Name:     c.cfg.AgentName,
		AgentID:  c.cfg.AgentID,
		Version:  c.cfg.CurrentVersion,
		Platform: c.cfg.Platform,
	}
	payload.CurrentRevision = request.CurrentRevision
	payload.LastApplyRevision = request.LastApplyRevision
	payload.LastApplyStatus = request.LastApplyStatus
	payload.LastApplyMessage = request.LastApplyMessage
	payload.ManagedCertificateReports = request.ManagedCertificateReports

	data, err := json.Marshal(payload)
	if err != nil {
		return Snapshot{}, err
	}

	endpoint := c.cfg.MasterURL + "/api/agents/heartbeat"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return Snapshot{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-agent-token", c.cfg.AgentToken)

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return Snapshot{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Snapshot{}, fmt.Errorf("heartbeat failed: %s", resp.Status)
	}

	var reply struct {
		Sync struct {
			Snapshot
			VersionPackageURL  string                `json:"version_package"`
			VersionPackageMeta *model.VersionPackage `json:"version_package_meta"`
			VersionSHA256      string                `json:"version_sha256"`
		} `json:"sync"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		return Snapshot{}, err
	}

	snapshot := reply.Sync.Snapshot
	snapshot.VersionPackage = normalizeVersionPackage(
		reply.Sync.VersionPackageMeta,
		reply.Sync.VersionPackageURL,
		reply.Sync.VersionSHA256,
	)

	return snapshot, nil
}

func normalizeVersionPackage(pkg *model.VersionPackage, rawURL, rawSHA256 string) *model.VersionPackage {
	if pkg != nil {
		copyValue := *pkg
		if copyValue.URL == "" {
			copyValue.URL = rawURL
		}
		if copyValue.SHA256 == "" {
			copyValue.SHA256 = rawSHA256
		}
		if copyValue.URL == "" && copyValue.SHA256 == "" && copyValue.Platform == "" && copyValue.Filename == "" && copyValue.Size == 0 {
			return nil
		}
		return &copyValue
	}
	if rawURL == "" && rawSHA256 == "" {
		return nil
	}
	return &model.VersionPackage{
		URL:    rawURL,
		SHA256: rawSHA256,
	}
}
