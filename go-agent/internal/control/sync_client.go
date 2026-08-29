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
	"net/url"
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
	// PluginCacheDir is the Agent-owned immutable policy artifact cache. Remote
	// snapshots are not returned to the runtime until every referenced artifact
	// has been downloaded and verified into this directory.
	PluginCacheDir string
	HTTPTransport  HTTPTransportConfig
	// DDNSReporter supplies the agent's last-extracted IPv4/IPv6 for the
	// heartbeat. Nil when DDNS extraction is unavailable; the heartbeat then
	// omits the fields and the master retains any previously stored value.
	DDNSReporter DDNSReporter
	// PKIHeartbeatHandler consumes tunnel PKI control data on every heartbeat,
	// independently of ordinary runtime revision changes.
	PKIHeartbeatHandler PKIHeartbeatHandler
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
	PluginStatuses            []model.PluginRuntimeStatus
	PluginLogs                []model.PluginRuntimeLogReport
	PluginLogsAcknowledged    func() error
	RuntimePackageSHA256      string
	PackageStaging            bool
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
	var pkiState PKIHeartbeatState
	if c.cfg.PKIHeartbeatHandler != nil {
		var err error
		pkiState, err = c.cfg.PKIHeartbeatHandler.PrepareHeartbeat(ctx)
		if err != nil {
			return Snapshot{}, fmt.Errorf("prepare PKI heartbeat: %w", err)
		}
	}
	payload := struct {
		Name                      string                            `json:"name"`
		AgentID                   string                            `json:"agent_id"`
		Capabilities              []string                          `json:"capabilities"`
		CurrentRevision           int                               `json:"current_revision"`
		LastApplyRevision         int                               `json:"last_apply_revision"`
		LastApplyStatus           string                            `json:"last_apply_status"`
		LastApplyMessage          string                            `json:"last_apply_message"`
		Stats                     *map[string]any                   `json:"stats,omitempty"`
		ManagedCertificateReports []model.ManagedCertificateReport  `json:"managed_certificate_reports"`
		LastSeenIPv4              string                            `json:"last_seen_ipv4,omitempty"`
		LastSeenIPv6              string                            `json:"last_seen_ipv6,omitempty"`
		Version                   string                            `json:"version"`
		Platform                  string                            `json:"platform"`
		RuntimePackage            model.RuntimePackage              `json:"runtime_package"`
		PKISecurityAck            *model.PKISecurityAcknowledgement `json:"pki_security_ack,omitempty"`
		PKIEnrollmentRequests     []model.PKIEnrollmentRequest      `json:"pki_enrollment_requests,omitempty"`
		PluginStatuses            []model.PluginRuntimeStatus       `json:"plugin_statuses,omitempty"`
		PluginLogs                []model.PluginRuntimeLogReport    `json:"plugin_logs,omitempty"`
	}{
		Name:           c.cfg.AgentName,
		AgentID:        c.cfg.AgentID,
		Capabilities:   append([]string(nil), c.cfg.Capabilities...),
		Version:        c.cfg.CurrentVersion,
		Platform:       c.cfg.Platform,
		RuntimePackage: overlayRuntimePackage(c.cfg.RuntimePackage, request),
		PKISecurityAck: pkiState.SecurityAcknowledgement,
		PKIEnrollmentRequests: append(
			[]model.PKIEnrollmentRequest(nil), pkiState.EnrollmentRequests...,
		),
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
	payload.PluginStatuses = append([]model.PluginRuntimeStatus(nil), request.PluginStatuses...)
	payload.PluginLogs = model.ClonePluginRuntimeLogReports(request.PluginLogs)

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
		if err := acknowledgePluginLogs(request); err != nil {
			return Snapshot{}, err
		}
		return Snapshot{}, nil
	}

	snapshotPayload := append([]byte(nil), reply.Sync...)
	var syncFields map[string]json.RawMessage
	if err := json.Unmarshal(reply.Sync, &syncFields); err != nil {
		return Snapshot{}, err
	}
	hasPKIReply := false
	for _, key := range []string{"pki_security", "pki_credentials", "pki_status"} {
		if _, ok := syncFields[key]; ok {
			hasPKIReply = true
			break
		}
	}
	if hasPKIReply {
		if c.cfg.PKIHeartbeatHandler == nil {
			return Snapshot{}, errors.New("heartbeat returned PKI state without an execution-plane handler")
		}
		var pkiReply PKIHeartbeatReply
		if err := json.Unmarshal(reply.Sync, &pkiReply); err != nil {
			return Snapshot{}, fmt.Errorf("decode PKI heartbeat: %w", err)
		}
		if err := c.cfg.PKIHeartbeatHandler.ApplyHeartbeat(ctx, pkiReply); err != nil {
			return Snapshot{}, fmt.Errorf("apply PKI heartbeat: %w", err)
		}
	}
	sanitized := false
	for _, key := range []string{"version_package", "pki_security", "pki_credentials", "pki_status"} {
		if _, ok := syncFields[key]; ok {
			delete(syncFields, key)
			sanitized = true
		}
	}
	if sanitized {
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
		SnapshotDigest     string                `json:"snapshot_digest"`
	}
	if err := json.Unmarshal(reply.Sync, &syncMeta); err != nil {
		return Snapshot{}, err
	}
	snapshot.VersionPackage = normalizeVersionPackage(
		syncMeta.VersionPackageMeta,
		syncMeta.VersionPackageURL,
		syncMeta.VersionSHA256,
	)
	if err := c.preparePluginArtifacts(ctx, &snapshot, snapshot.Revision, syncMeta.SnapshotDigest); err != nil {
		return Snapshot{}, err
	}
	if err := acknowledgePluginLogs(request); err != nil {
		return Snapshot{}, err
	}

	return snapshot, nil
}

func overlayRuntimePackage(base model.RuntimePackage, request SyncRequest) model.RuntimePackage {
	if sha := strings.TrimSpace(request.RuntimePackageSHA256); sha != "" {
		base.SHA256 = sha
	}
	base.Staging = request.PackageStaging
	return base
}

func acknowledgePluginLogs(request SyncRequest) error {
	if len(request.PluginLogs) == 0 || request.PluginLogsAcknowledged == nil {
		return nil
	}
	return request.PluginLogsAcknowledged()
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
	// Resolve only after authenticating the immutable wire payload. The updater
	// requires an absolute URL, while the verified digest must remain the digest
	// issued by the control plane for the root-relative snapshot value.
	resolveRevisionPackageURL(c.cfg.MasterURL, snapshot.VersionPackage)
	if err := c.preparePluginArtifacts(ctx, &snapshot, pull.Lease.Revision, pull.Lease.SnapshotDigest); err != nil {
		return model.RevisionPull{}, err
	}
	pull.Snapshot = &snapshot
	pull.VerifiedSnapshotDigest = digest
	return pull, nil
}

func resolveRevisionPackageURL(masterURL string, pkg *model.VersionPackage) {
	if pkg == nil {
		return
	}
	raw := strings.TrimSpace(pkg.URL)
	if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return
	}
	base, baseErr := url.Parse(strings.TrimRight(strings.TrimSpace(masterURL), "/") + "/")
	reference, referenceErr := url.Parse(raw)
	if baseErr != nil || referenceErr != nil || base.Scheme == "" || base.Host == "" || reference.IsAbs() || reference.Host != "" {
		return
	}
	pkg.URL = base.ResolveReference(reference).String()
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

// RedeemPluginSecrets exchanges an authenticated, generation-fenced handle
// set for transient values. Response bodies and values are deliberately never
// copied into returned errors.
func (c *SyncClient) RedeemPluginSecrets(ctx context.Context, input model.PluginSecretRedemptionRequest) ([]model.PluginRedeemedSecret, error) {
	if c == nil || c.client == nil || ctx == nil {
		return nil, errors.New("plugin secret redemption client is unavailable")
	}
	if err := input.Validate(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(input)
	if err != nil {
		return nil, errors.New("encode plugin secret redemption request")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.MasterURL+"/api/agent-plugin-secrets/redeem", bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("create plugin secret redemption request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-agent-token", c.cfg.AgentToken)
	resp, err := c.client.Do(req)
	if err != nil {
		c.discardConnections()
		return nil, errors.New("plugin secret redemption transport failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		c.discardConnections()
		return nil, fmt.Errorf("plugin secret redemption failed with status %d", resp.StatusCode)
	}
	var output model.PluginSecretRedemptionResponse
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&output); err != nil {
		return nil, errors.New("decode plugin secret redemption response")
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return nil, errors.New("decode plugin secret redemption response")
	}
	if output.Secrets == nil {
		return nil, errors.New("plugin secret redemption response is incomplete")
	}
	return output.Secrets, nil
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
		return newRevisionRequestError(path, resp.Status, resp.StatusCode, message)
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
