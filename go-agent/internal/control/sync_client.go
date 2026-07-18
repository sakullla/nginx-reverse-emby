package control

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type Snapshot = model.Snapshot

// DDNSReporter exposes the DDNS module's extracted addresses so the heartbeat
// payload can carry them upstream without the control package depending on the
// ddns module. The reporter may perform a throttled refresh when it is due.
type DDNSReporter interface {
	LastSeenIPs(context.Context) (string, string)
}

type SyncClientConfig struct {
	MasterURL      string
	AgentToken     string
	AgentID        string
	AgentName      string
	Capabilities   []string
	CurrentVersion string
	Platform       string
	RuntimePackage model.RuntimePackage
	HTTPTransport  HTTPTransportConfig
	// DDNSReporter supplies the agent's last-extracted IPv4/IPv6 for the
	// heartbeat. Nil when DDNS extraction is unavailable; the heartbeat then
	// omits the fields and the master retains any previously stored value.
	DDNSReporter DDNSReporter
}

type SyncClient struct {
	cfg       SyncClientConfig
	client    *http.Client
	transport *http.Transport
}

type SyncRequest struct {
	CurrentRevision           int
	LastApplyRevision         int
	LastApplyStatus           string
	LastApplyMessage          string
	Stats                     map[string]any
	StatsPresent              bool
	ManagedCertificateReports []model.ManagedCertificateReport
	LastSeenIPv4              string
	LastSeenIPv6              string
}

func NewSyncClient(cfg SyncClientConfig, httpClient *http.Client) *SyncClient {
	cfg.MasterURL = strings.TrimRight(cfg.MasterURL, "/")
	cfg.MasterURL = normalizeMasterBaseURL(cfg.MasterURL)
	if httpClient != nil {
		return &SyncClient{cfg: cfg, client: httpClient}
	}
	transport := newHTTPTransport(cfg.HTTPTransport)
	client := &http.Client{
		Transport: transport,
		Timeout:   60 * time.Second,
	}
	return &SyncClient{cfg: cfg, client: client, transport: transport}
}

func (c *SyncClient) Sync(ctx context.Context, request SyncRequest) (Snapshot, error) {
	// The DDNS module is the source of truth for the agent's extracted IPs.
	// It is consulted here (on the caller's heartbeat goroutine) so the data
	// does not have to be threaded through BuildSyncPlan; the reporter is a
	// non-blocking cache read.
	if c.cfg.DDNSReporter != nil {
		ipv4, ipv6 := c.cfg.DDNSReporter.LastSeenIPs(ctx)
		request.LastSeenIPv4 = ipv4
		request.LastSeenIPv6 = ipv6
	}
	payload := struct {
		Name                      string                           `json:"name"`
		AgentID                   string                           `json:"agent_id"`
		Capabilities              []string                         `json:"capabilities"`
		CurrentRevision           int                              `json:"current_revision"`
		LastApplyRevision         int                              `json:"last_apply_revision"`
		LastApplyStatus           string                           `json:"last_apply_status"`
		LastApplyMessage          string                           `json:"last_apply_message"`
		Stats                     *map[string]any                  `json:"stats,omitempty"`
		ManagedCertificateReports []model.ManagedCertificateReport `json:"managed_certificate_reports"`
		LastSeenIPv4              string                           `json:"last_seen_ipv4,omitempty"`
		LastSeenIPv6              string                           `json:"last_seen_ipv6,omitempty"`
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
	if request.StatsPresent || request.Stats != nil {
		stats := request.Stats
		payload.Stats = &stats
	}
	payload.ManagedCertificateReports = request.ManagedCertificateReports
	payload.LastSeenIPv4 = request.LastSeenIPv4
	payload.LastSeenIPv6 = request.LastSeenIPv6

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

func (c *SyncClient) PullRevision(ctx context.Context) (model.RevisionPull, error) {
	var envelope struct {
		Revision struct {
			HasUpdate       bool                 `json:"has_update"`
			DesiredRevision int64                `json:"desired_revision"`
			Lease           *model.RevisionLease `json:"lease,omitempty"`
			Snapshot        json.RawMessage      `json:"snapshot,omitempty"`
		} `json:"revision"`
	}
	if err := c.doRevisionRequest(ctx, "/api/agent-revisions/pull", nil, &envelope); err != nil {
		return model.RevisionPull{}, err
	}
	pull := model.RevisionPull{
		HasUpdate:       envelope.Revision.HasUpdate,
		DesiredRevision: envelope.Revision.DesiredRevision,
		Lease:           envelope.Revision.Lease,
	}
	if !pull.HasUpdate {
		return pull, nil
	}
	if pull.Lease == nil || len(envelope.Revision.Snapshot) == 0 || bytes.Equal(envelope.Revision.Snapshot, []byte("null")) {
		return model.RevisionPull{}, errors.New("revision pull returned an incomplete update")
	}
	if err := validateRevisionLeaseMetadata(*pull.Lease, pull.DesiredRevision, time.Now().UTC()); err != nil {
		return model.RevisionPull{}, err
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(envelope.Revision.Snapshot))
	if !strings.EqualFold(digest, pull.Lease.SnapshotDigest) {
		return model.RevisionPull{}, errors.New("revision snapshot digest does not match lease")
	}
	var snapshot model.Snapshot
	if err := json.Unmarshal(envelope.Revision.Snapshot, &snapshot); err != nil {
		return model.RevisionPull{}, fmt.Errorf("decode revision snapshot: %w", err)
	}
	if !snapshot.HasFullRevisionPayload() {
		return model.RevisionPull{}, errors.New("revision pull snapshot is not a full snapshot")
	}
	if snapshot.Revision != pull.Lease.Revision || snapshot.DesiredVersion != pull.Lease.DesiredVersion {
		return model.RevisionPull{}, errors.New("revision pull snapshot identity does not match lease")
	}
	if agentID := strings.TrimSpace(c.cfg.AgentID); agentID != "" && strings.TrimSpace(pull.Lease.AgentID) != agentID {
		return model.RevisionPull{}, errors.New("revision pull lease belongs to a different agent")
	}
	pull.Snapshot = &snapshot
	pull.VerifiedSnapshotDigest = digest
	return pull, nil
}

func validateRevisionLeaseMetadata(lease model.RevisionLease, desiredRevision int64, now time.Time) error {
	if lease.DeadlineAt.IsZero() || !now.Before(lease.DeadlineAt) {
		return errors.New("revision lease deadline is missing or expired")
	}
	if lease.ApplyTimeoutSeconds <= 0 || lease.DrainTimeoutSeconds <= 0 {
		return errors.New("revision lease timeout metadata must be positive")
	}
	if strings.TrimSpace(lease.AgentID) == "" || lease.Revision <= 0 || lease.Revision != desiredRevision ||
		lease.RetryCycle < 0 || lease.Attempt <= 0 || strings.TrimSpace(lease.LeaseID) == "" ||
		strings.TrimSpace(lease.SnapshotDigest) == "" {
		return errors.New("revision lease identity is inconsistent")
	}
	return nil
}

func (c *SyncClient) StartRevision(ctx context.Context, input model.RevisionStart) error {
	return c.doRevisionRequest(ctx, "/api/agent-revisions/"+strconv.FormatInt(input.Revision, 10)+"/start", input, nil)
}

func (c *SyncClient) ReportRevision(ctx context.Context, input model.RevisionReport) error {
	return c.doRevisionRequest(ctx, "/api/agent-revisions/"+strconv.FormatInt(input.Revision, 10)+"/report", input, nil)
}

func (c *SyncClient) doRevisionRequest(ctx context.Context, path string, input any, output any) error {
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.MasterURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-agent-token", c.cfg.AgentToken)
	resp, err := c.client.Do(req)
	if err != nil {
		c.discardConnections()
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		c.discardConnections()
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("revision request %s failed: %s: %s", path, resp.Status, strings.TrimSpace(string(message)))
	}
	if output == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(output)
}

func (c *SyncClient) discardConnections() {
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
