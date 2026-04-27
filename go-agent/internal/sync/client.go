package sync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/config"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type Snapshot = model.Snapshot
type HTTPTransportConfig = config.HTTPTransportConfig

type ClientConfig struct {
	MasterURL      string
	AgentToken     string
	AgentID        string
	AgentName      string
	Capabilities   []string
	CurrentVersion string
	Platform       string
	RuntimePackage model.RuntimePackage
	HTTPTransport  HTTPTransportConfig
}

type Client struct {
	cfg       ClientConfig
	client    *http.Client
	transport *http.Transport
}

type SyncRequest struct {
	CurrentRevision           int
	LastApplyRevision         int
	LastApplyStatus           string
	LastApplyMessage          string
	ManagedCertificateReports []model.ManagedCertificateReport
}

func NewClient(cfg ClientConfig, httpClient *http.Client) *Client {
	cfg.MasterURL = strings.TrimRight(cfg.MasterURL, "/")
	cfg.MasterURL = normalizeMasterBaseURL(cfg.MasterURL)
	if httpClient != nil {
		return &Client{cfg: cfg, client: httpClient}
	}
	transportCfg := config.Default().HTTPTransport
	if cfg.HTTPTransport.DialTimeout > 0 {
		transportCfg.DialTimeout = cfg.HTTPTransport.DialTimeout
	}
	if cfg.HTTPTransport.TLSHandshakeTimeout > 0 {
		transportCfg.TLSHandshakeTimeout = cfg.HTTPTransport.TLSHandshakeTimeout
	}
	if cfg.HTTPTransport.ResponseHeaderTimeout > 0 {
		transportCfg.ResponseHeaderTimeout = cfg.HTTPTransport.ResponseHeaderTimeout
	}
	if cfg.HTTPTransport.IdleConnTimeout > 0 {
		transportCfg.IdleConnTimeout = cfg.HTTPTransport.IdleConnTimeout
	}
	if cfg.HTTPTransport.KeepAlive > 0 {
		transportCfg.KeepAlive = cfg.HTTPTransport.KeepAlive
	}
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   transportCfg.DialTimeout,
			KeepAlive: transportCfg.KeepAlive,
		}).DialContext,
		TLSHandshakeTimeout:   transportCfg.TLSHandshakeTimeout,
		ResponseHeaderTimeout: transportCfg.ResponseHeaderTimeout,
		IdleConnTimeout:       transportCfg.IdleConnTimeout,
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   60 * time.Second,
	}
	return &Client{cfg: cfg, client: client, transport: transport}
}

func (c *Client) Sync(ctx context.Context, request SyncRequest) (Snapshot, error) {
	payload := struct {
		Name                      string                           `json:"name"`
		AgentID                   string                           `json:"agent_id"`
		Capabilities              []string                         `json:"capabilities"`
		CurrentRevision           int                              `json:"current_revision"`
		LastApplyRevision         int                              `json:"last_apply_revision"`
		LastApplyStatus           string                           `json:"last_apply_status"`
		LastApplyMessage          string                           `json:"last_apply_message"`
		ManagedCertificateReports []model.ManagedCertificateReport `json:"managed_certificate_reports"`
		Version                   string                           `json:"version"`
		Platform                  string                           `json:"platform"`
		RuntimePackage            model.RuntimePackage             `json:"runtime_package"`
	}{
		Name:           c.cfg.AgentName,
		AgentID:        c.cfg.AgentID,
		Capabilities:   append([]string(nil), c.cfg.Capabilities...),
		Version:        c.cfg.CurrentVersion,
		Platform:       c.cfg.Platform,
		RuntimePackage: c.cfg.RuntimePackage,
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
		c.discardConnections()
		return Snapshot{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.discardConnections()
		return Snapshot{}, fmt.Errorf("heartbeat failed: %s", resp.Status)
	}

	var reply struct {
		Sync json.RawMessage `json:"sync"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		return Snapshot{}, err
	}
	if len(reply.Sync) == 0 {
		return Snapshot{}, nil
	}

	snapshotPayload := append([]byte(nil), reply.Sync...)
	var syncFields map[string]json.RawMessage
	if err := json.Unmarshal(reply.Sync, &syncFields); err != nil {
		return Snapshot{}, err
	}
	if _, ok := syncFields["version_package"]; ok {
		delete(syncFields, "version_package")
		var err error
		snapshotPayload, err = json.Marshal(syncFields)
		if err != nil {
			return Snapshot{}, err
		}
	}

	var snapshot Snapshot
	if err := json.Unmarshal(snapshotPayload, &snapshot); err != nil {
		return Snapshot{}, err
	}
	var syncMeta struct {
		VersionPackageURL  string                `json:"version_package"`
		VersionPackageMeta *model.VersionPackage `json:"version_package_meta"`
		VersionSHA256      string                `json:"version_sha256"`
	}
	if err := json.Unmarshal(reply.Sync, &syncMeta); err != nil {
		return Snapshot{}, err
	}
	snapshot.VersionPackage = normalizeVersionPackage(
		syncMeta.VersionPackageMeta,
		syncMeta.VersionPackageURL,
		syncMeta.VersionSHA256,
	)

	return snapshot, nil
}

func (c *Client) discardConnections() {
	if c.transport != nil {
		c.transport.CloseIdleConnections()
	}
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

func normalizeMasterBaseURL(raw string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	switch {
	case strings.HasSuffix(trimmed, "/panel-api"):
		return strings.TrimSuffix(trimmed, "/panel-api")
	case strings.HasSuffix(trimmed, "/api"):
		return strings.TrimSuffix(trimmed, "/api")
	default:
		return trimmed
	}
}
