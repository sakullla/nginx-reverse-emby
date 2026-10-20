package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

const (
	TaskTypeProbeBackendReachability = "probe_backend_reachability"

	accessPhasePrepare = "准备新入口"
	accessPhaseWait    = "等待新入口生效"
	accessPhaseRetire  = "退役旧入口"

	accessPathDirect  = "direct"
	accessPathRelay   = "relay"
	accessPathPending = "pending"

	accessSummaryPendingApply   = "已保存、待生效"
	accessSummaryPendingConfirm = "待确认"
	accessSummaryFailed         = "失败"
	accessSummaryPartial        = "部分完成"
	accessSummaryReady          = "已就绪"

	accessTrafficRule        = "http_rule"
	accessTrafficEntryAgent  = "entry_agent"
	accessTrafficUnavailable = "unavailable"

	accessServiceTagPrefix = "nre-access-service:"

	accessAddressLoopback    = "loopback"
	accessAddressLinkLocal   = "link_local"
	accessAddressPrivate     = "private"
	accessAddressUnspecified = "unspecified"
	accessAddressPublic      = "public"
)

var ErrAccessServiceNotFound = errors.New("access service not found")

// AccessStatusError keeps a failed service mutation visible. HasView is set
// when the service row was persisted and must be returned with the error.
type AccessStatusError struct {
	Status  int
	Err     error
	View    AccessServiceView
	HasView bool
}

func (e *AccessStatusError) Error() string {
	if e == nil || e.Err == nil {
		return "access service operation failed"
	}
	return e.Err.Error()
}

func (e *AccessStatusError) Unwrap() error { return e.Err }

type AccessServiceInput struct {
	Name            *string `json:"name,omitempty"`
	BackendURL      *string `json:"backend_url,omitempty"`
	ServiceAgentID  *string `json:"service_agent_id,omitempty"`
	EntryAgentID    *string `json:"entry_agent_id,omitempty"`
	EntryURL        *string `json:"entry_url,omitempty"`
	RelayListenPort *int    `json:"relay_listen_port,omitempty"`
	RelayPublicHost *string `json:"relay_public_host,omitempty"`
}

type AccessClientConfirmationInput struct {
	Confirmed bool   `json:"confirmed"`
	Note      string `json:"note,omitempty"`
}

type AccessServiceView struct {
	ID                 string                    `json:"id"`
	Name               string                    `json:"name"`
	EntryAgentID       string                    `json:"entry_agent_id"`
	ServiceAgentID     string                    `json:"service_agent_id"`
	BackendURL         string                    `json:"backend_url"`
	EntryURL           string                    `json:"entry_url"`
	RuleAgentID        string                    `json:"rule_agent_id,omitempty"`
	RuleID             int                       `json:"rule_id,omitempty"`
	PendingRuleAgentID string                    `json:"pending_rule_agent_id,omitempty"`
	PendingRuleID      int                       `json:"pending_rule_id,omitempty"`
	SwitchPhase        string                    `json:"switch_phase,omitempty"`
	Summary            string                    `json:"summary"`
	HTTPSReady         bool                      `json:"https_ready"`
	Advanced           bool                      `json:"advanced"`
	AdvancedReason     string                    `json:"advanced_reason,omitempty"`
	LastError          string                    `json:"last_error,omitempty"`
	Path               AccessPathState           `json:"path"`
	Certificate        AccessCertificateState    `json:"certificate"`
	Probes             []AccessProbeFact         `json:"probes"`
	Rules              []HTTPRule                `json:"rules"`
	Traffic            AccessServiceTraffic      `json:"traffic"`
	ClientConfirmation *AccessClientConfirmation `json:"client_confirmation,omitempty"`
	Deletion           *AccessDeletionState      `json:"deletion,omitempty"`
	Apply              AccessApplyState          `json:"apply"`
	CreatedAt          string                    `json:"created_at"`
	UpdatedAt          string                    `json:"updated_at"`
}

type AccessPathState struct {
	Mode    string `json:"mode"`
	Summary string `json:"summary,omitempty"`
	Pending bool   `json:"pending"`
}

type AccessCertificateState struct {
	State      string `json:"state"`
	NotAfter   string `json:"not_after,omitempty"`
	LastError  string `json:"last_error,omitempty"`
	HTTPSReady bool   `json:"https_ready"`
}

type AccessProbeFact struct {
	AgentID           string `json:"agent_id"`
	Target            string `json:"target"`
	CheckedAt         string `json:"checked_at,omitempty"`
	Status            string `json:"status"`
	HTTPStatusCode    int    `json:"http_status_code,omitempty"`
	RelayListenerID   int    `json:"relay_listener_id,omitempty"`
	HasListener       bool   `json:"has_listener,omitempty"`
	Purpose           string `json:"purpose,omitempty"`
	Error             string `json:"error,omitempty"`
	TaskID            string `json:"task_id,omitempty"`
	PublishedRevision int    `json:"published_revision,omitempty"`
}

type AccessClientConfirmation struct {
	Confirmed   bool   `json:"confirmed"`
	ConfirmedAt string `json:"confirmed_at,omitempty"`
	Note        string `json:"note,omitempty"`
	Kind        string `json:"kind"`
}

type AccessDeletionState struct {
	Pending            bool                     `json:"pending"`
	RequiredRevision   map[string]int           `json:"required_revision,omitempty"`
	StoppedEntries     []AccessStoppedEntry     `json:"stopped_entries,omitempty"`
	DeletedListenerIDs []int                    `json:"deleted_listener_ids,omitempty"`
	Retained           []AccessRetainedResource `json:"retained,omitempty"`
	Note               string                   `json:"note,omitempty"`
}

type AccessStoppedEntry struct {
	AgentID string `json:"agent_id"`
	RuleID  int    `json:"rule_id"`
	URL     string `json:"url,omitempty"`
}

type AccessRetainedResource struct {
	Kind   string `json:"kind"`
	ID     string `json:"id,omitempty"`
	Reason string `json:"reason"`
}

type AccessApplyState struct {
	Pending            bool `json:"pending"`
	OldEntryMayReceive bool `json:"old_entry_may_receive,omitempty"`
	NewEntryApplied    bool `json:"new_entry_applied,omitempty"`
	ListenerApplied    bool `json:"listener_applied,omitempty"`
	CertificateReady   bool `json:"certificate_ready,omitempty"`
}

type AccessServiceTraffic struct {
	State          string  `json:"state"`
	ScopeType      string  `json:"scope_type,omitempty"`
	ScopeID        string  `json:"scope_id,omitempty"`
	Label          string  `json:"label,omitempty"`
	RXBytes        *uint64 `json:"rx_bytes,omitempty"`
	TXBytes        *uint64 `json:"tx_bytes,omitempty"`
	AccountedBytes *uint64 `json:"accounted_bytes,omitempty"`
}

type AccessRelaySecurity struct {
	Ready     bool
	Mode      string
	Transport string
	Reason    string
}

type accessRecordStore interface {
	ListAccessServices(context.Context) ([]storage.AccessServiceRow, error)
	GetAccessService(context.Context, string) (storage.AccessServiceRow, bool, error)
	SaveAccessService(context.Context, storage.AccessServiceRow) error
	DeleteAccessService(context.Context, string) error
	GetAccessServiceOperation(context.Context, string) (storage.AccessServiceOperationRow, bool, error)
	SaveAccessServiceOperation(context.Context, storage.AccessServiceOperationRow) error
}

// accessEntrySnapshotPublisher bumps the entry agent so its next applied
// snapshot can include a service-side listener before a forwarding rule exists.
// Stores that do not implement it have no snapshot to wait for.
type accessEntrySnapshotPublisher interface {
	PublishEntryRelayListener(context.Context, string) (int, error)
}

type accessRuleAPI interface {
	Get(context.Context, string, int) (HTTPRule, error)
	List(context.Context, string) ([]HTTPRule, error)
	Create(context.Context, string, HTTPRuleInput) (HTTPRule, error)
	Update(context.Context, string, int, HTTPRuleInput) (HTTPRule, error)
	Delete(context.Context, string, int) (HTTPRule, error)
}

type accessL4API interface {
	List(context.Context, string) ([]L4Rule, error)
}

type accessRelayAPI interface {
	List(context.Context, string) ([]RelayListener, error)
	Create(context.Context, string, RelayListenerInput) (RelayListener, error)
	Delete(context.Context, string, int) (RelayListener, error)
	AccessRelaySecurity(context.Context) (AccessRelaySecurity, error)
}

type accessAgentAPI interface {
	Get(context.Context, string) (AgentSummary, error)
	List(context.Context) ([]AgentSummary, error)
}

type accessCertAPI interface {
	List(context.Context, string) ([]ManagedCertificate, error)
}

type accessProbeAPI interface {
	Dispatch(context.Context, string, map[string]any) (string, map[string]any, error)
	Result(context.Context, string, string) (map[string]any, bool, error)
}

type AccessTrafficReader interface {
	ServiceTraffic(context.Context, string, int) (AccessServiceTraffic, error)
}

type AccessService struct {
	store   accessRecordStore
	rules   accessRuleAPI
	l4      accessL4API
	relays  accessRelayAPI
	agents  accessAgentAPI
	certs   accessCertAPI
	probes  accessProbeAPI
	traffic AccessTrafficReader
	now     func() time.Time
	newID   func() (string, error)
}

// BindAccessService wires the concrete services when the relay implementation
// can report the current security mode. Callers that already hold an access
// service keep that instance, including its store.
func BindAccessService(store accessRecordStore, rules accessRuleAPI, l4 accessL4API, relays any, agents accessAgentAPI, certs accessCertAPI, tasks *TaskService, traffic any) *AccessService {
	relayAPI, ok := relays.(accessRelayAPI)
	if store == nil || rules == nil || agents == nil || !ok {
		return nil
	}
	var reader AccessTrafficReader
	if typed, ok := traffic.(AccessTrafficReader); ok {
		reader = typed
	}
	return NewAccessService(store, rules, l4, relayAPI, agents, certs, tasks, reader)
}

func NewAccessService(
	store accessRecordStore,
	rules accessRuleAPI,
	l4 accessL4API,
	relays accessRelayAPI,
	agents accessAgentAPI,
	certs accessCertAPI,
	tasks *TaskService,
	traffic AccessTrafficReader,
) *AccessService {
	var probes accessProbeAPI
	if tasks != nil {
		probes = taskProbeClient{tasks: tasks}
	}
	return &AccessService{
		store: store, rules: rules, l4: l4, relays: relays, agents: agents, certs: certs,
		probes: probes, traffic: traffic, now: time.Now, newID: newAccessServiceID,
	}
}

func (s *AccessService) List(ctx context.Context) ([]AccessServiceView, error) {
	rows, err := s.store.ListAccessServices(ctx)
	if err != nil {
		return nil, err
	}
	views := make([]AccessServiceView, 0, len(rows))
	for _, row := range rows {
		row, err = s.observe(ctx, row)
		if err != nil {
			return nil, err
		}
		if row.ID == "" {
			continue
		}
		view, err := s.present(ctx, row)
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	return views, nil
}

func (s *AccessService) Get(ctx context.Context, id string) (AccessServiceView, error) {
	row, found, err := s.load(ctx, id)
	if err != nil || !found {
		if err == nil {
			err = ErrAccessServiceNotFound
		}
		return AccessServiceView{}, err
	}
	row, err = s.observe(ctx, row)
	if err != nil {
		return AccessServiceView{}, err
	}
	if row.ID == "" {
		return AccessServiceView{}, ErrAccessServiceNotFound
	}
	return s.present(ctx, row)
}

func (s *AccessService) Create(ctx context.Context, key string, body []byte, input AccessServiceInput) (AccessServiceView, error) {
	if view, err, ok := s.replay(ctx, "create", key, body); ok {
		return view, err
	}
	row, err := s.prepareNew(ctx, input)
	if err != nil {
		return AccessServiceView{}, err
	}
	opErr := s.converge(ctx, &row, false)
	return s.finish(ctx, "create", key, body, row, opErr)
}

func (s *AccessService) Correct(ctx context.Context, id, key string, body []byte, input AccessServiceInput) (AccessServiceView, error) {
	return s.change(ctx, "correct", id, key, body, input, true)
}

func (s *AccessService) Update(ctx context.Context, id, key string, body []byte, input AccessServiceInput) (AccessServiceView, error) {
	return s.change(ctx, "update", id, key, body, input, false)
}

func (s *AccessService) Retry(ctx context.Context, id, key string, body []byte) (AccessServiceView, error) {
	if view, err, ok := s.replay(ctx, "retry", key, body); ok {
		return view, err
	}
	row, found, err := s.load(ctx, id)
	if err != nil || !found {
		if err == nil {
			err = ErrAccessServiceNotFound
		}
		return AccessServiceView{}, err
	}
	opErr := s.converge(ctx, &row, true)
	return s.finish(ctx, "retry", key, body, row, opErr)
}

func (s *AccessService) ConfirmClient(ctx context.Context, id, key string, body []byte, input AccessClientConfirmationInput) (AccessServiceView, error) {
	if view, err, ok := s.replay(ctx, "confirm", key, body); ok {
		return view, err
	}
	row, found, err := s.load(ctx, id)
	if err != nil || !found {
		if err == nil {
			err = ErrAccessServiceNotFound
		}
		return AccessServiceView{}, err
	}
	confirmedAt := ""
	if input.Confirmed {
		confirmedAt = s.now().UTC().Format(time.RFC3339)
	}
	encoded, err := json.Marshal(AccessClientConfirmation{
		Confirmed: input.Confirmed, ConfirmedAt: confirmedAt, Note: strings.TrimSpace(input.Note), Kind: "user_confirmation",
	})
	if err != nil {
		return AccessServiceView{}, err
	}
	row.ClientConfirmJSON = string(encoded)
	return s.finish(ctx, "confirm", key, body, row, nil)
}

func (s *AccessService) change(ctx context.Context, operation, id, key string, body []byte, input AccessServiceInput, force bool) (AccessServiceView, error) {
	if view, err, ok := s.replay(ctx, operation, key, body); ok {
		return view, err
	}
	row, found, err := s.load(ctx, id)
	if err != nil || !found {
		if err == nil {
			err = ErrAccessServiceNotFound
		}
		return AccessServiceView{}, err
	}
	before := row
	if err := s.applyInput(ctx, &row, input, false); err != nil {
		return AccessServiceView{}, err
	}
	if !force && !accessForwardingChanged(before, row) {
		return s.finish(ctx, operation, key, body, row, nil)
	}
	opErr := s.converge(ctx, &row, force)
	return s.finish(ctx, operation, key, body, row, opErr)
}

func accessForwardingChanged(before, next storage.AccessServiceRow) bool {
	return before.BackendURL != next.BackendURL ||
		before.EntryURL != next.EntryURL ||
		before.EntryAgentID != next.EntryAgentID ||
		before.ServiceAgentID != next.ServiceAgentID ||
		before.RelayListenPort != next.RelayListenPort ||
		before.RelayPublicHost != next.RelayPublicHost
}

func (s *AccessService) load(ctx context.Context, id string) (storage.AccessServiceRow, bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return storage.AccessServiceRow{}, false, fmt.Errorf("%w: service id is required", ErrInvalidArgument)
	}
	return s.store.GetAccessService(ctx, id)
}

func (s *AccessService) prepareNew(ctx context.Context, input AccessServiceInput) (storage.AccessServiceRow, error) {
	id, err := s.newID()
	if err != nil {
		return storage.AccessServiceRow{}, err
	}
	now := s.now().UTC().Format(time.RFC3339)
	row := storage.AccessServiceRow{ID: id, CreatedAt: now, UpdatedAt: now, ProbeJSON: "[]"}
	if err := s.applyInput(ctx, &row, input, true); err != nil {
		return storage.AccessServiceRow{}, err
	}
	return row, nil
}

func (s *AccessService) applyInput(ctx context.Context, row *storage.AccessServiceRow, input AccessServiceInput, creating bool) error {
	next := *row
	if input.Name != nil {
		next.Name = strings.TrimSpace(*input.Name)
	}
	changed := false
	if input.BackendURL != nil && strings.TrimSpace(*input.BackendURL) != row.BackendURL {
		next.BackendURL = strings.TrimSpace(*input.BackendURL)
		changed = true
	}
	if input.EntryURL != nil && strings.TrimSpace(*input.EntryURL) != row.EntryURL {
		next.EntryURL = strings.TrimSpace(*input.EntryURL)
		changed = true
	}
	if input.EntryAgentID != nil && strings.TrimSpace(*input.EntryAgentID) != row.EntryAgentID {
		next.EntryAgentID = strings.TrimSpace(*input.EntryAgentID)
		changed = true
	}
	if input.ServiceAgentID != nil && strings.TrimSpace(*input.ServiceAgentID) != row.ServiceAgentID {
		next.ServiceAgentID = strings.TrimSpace(*input.ServiceAgentID)
		changed = true
	}
	if input.RelayListenPort != nil && *input.RelayListenPort != row.RelayListenPort {
		next.RelayListenPort = *input.RelayListenPort
		changed = true
	}
	if input.RelayPublicHost != nil && strings.TrimSpace(*input.RelayPublicHost) != row.RelayPublicHost {
		next.RelayPublicHost = strings.TrimSpace(*input.RelayPublicHost)
		changed = true
	}
	if creating || next.BackendURL == "" || next.EntryURL == "" || next.EntryAgentID == "" || next.ServiceAgentID == "" {
		if strings.TrimSpace(next.BackendURL) == "" || strings.TrimSpace(next.EntryURL) == "" || strings.TrimSpace(next.EntryAgentID) == "" || strings.TrimSpace(next.ServiceAgentID) == "" {
			return fmt.Errorf("%w: backend_url, entry_url, entry_agent_id, and service_agent_id are required", ErrInvalidArgument)
		}
	}
	if _, err := parseAccessHTTPURL(next.BackendURL); err != nil {
		return err
	}
	entry, err := parseAccessHTTPURL(next.EntryURL)
	if err != nil {
		return err
	}
	if _, err := s.agents.Get(ctx, next.EntryAgentID); err != nil {
		return err
	}
	if _, err := s.agents.Get(ctx, next.ServiceAgentID); err != nil {
		return err
	}
	if strings.TrimSpace(next.Name) == "" {
		next.Name = entry.Hostname()
	}
	if changed {
		next.ProbeJSON = "[]"
		next.ClientConfirmJSON = ""
		next.LastError = ""
	}
	if !creating && changed && row.RuleID > 0 && next.EntryAgentID != row.RuleAgentID && row.RuleAgentID != "" {
		next.SwitchPhase = accessPhasePrepare
	}
	*row = next
	return nil
}

type storedAccessResponse struct {
	Status int               `json:"status"`
	Code   string            `json:"code,omitempty"`
	Error  string            `json:"error,omitempty"`
	View   AccessServiceView `json:"view"`
}

func (s *AccessService) replay(ctx context.Context, operation, key string, body []byte) (AccessServiceView, error, bool) {
	key = strings.TrimSpace(key)
	if key == "" {
		return AccessServiceView{}, fmt.Errorf("%w: Idempotency-Key is required", ErrInvalidArgument), true
	}
	if len(key) > 256 {
		return AccessServiceView{}, fmt.Errorf("%w: Idempotency-Key is too long", ErrInvalidArgument), true
	}
	row, found, err := s.store.GetAccessServiceOperation(ctx, key)
	if err != nil {
		return AccessServiceView{}, err, true
	}
	if !found {
		return AccessServiceView{}, nil, false
	}
	fingerprint := accessOperationFingerprint(operation, body)
	if row.Fingerprint != fingerprint {
		return AccessServiceView{}, newConflictError("idempotency key was already used with a different request"), true
	}
	var saved storedAccessResponse
	if err := json.Unmarshal([]byte(row.ResponseJSON), &saved); err != nil {
		return AccessServiceView{}, err, true
	}
	if saved.Error == "" {
		return saved.View, nil, true
	}
	var cause error
	switch saved.Code {
	case "conflict":
		cause = newConflictError("%s", saved.Error)
	case "not_found":
		cause = ErrAccessServiceNotFound
	default:
		cause = fmt.Errorf("%w: %s", ErrInvalidArgument, saved.Error)
	}
	return saved.View, &AccessStatusError{Status: saved.Status, Err: cause, View: saved.View, HasView: true}, true
}

func (s *AccessService) finish(ctx context.Context, operation, key string, body []byte, row storage.AccessServiceRow, opErr error) (AccessServiceView, error) {
	row.UpdatedAt = s.now().UTC().Format(time.RFC3339)
	if row.CreatedAt == "" {
		row.CreatedAt = row.UpdatedAt
	}
	if err := s.store.SaveAccessService(ctx, row); err != nil {
		return AccessServiceView{}, err
	}
	view, err := s.present(ctx, row)
	if err != nil {
		return AccessServiceView{}, err
	}
	saved := storedAccessResponse{Status: 200, View: view}
	var ret error
	if opErr != nil {
		saved.Status = accessErrorStatus(opErr)
		saved.Code = accessErrorCode(opErr)
		saved.Error = opErr.Error()
		ret = &AccessStatusError{Status: saved.Status, Err: opErr, View: view, HasView: true}
	}
	encoded, err := json.Marshal(saved)
	if err != nil {
		return AccessServiceView{}, err
	}
	if err := s.store.SaveAccessServiceOperation(ctx, storage.AccessServiceOperationRow{
		IdempotencyKey: strings.TrimSpace(key),
		Fingerprint:    accessOperationFingerprint(operation, body),
		ServiceID:      row.ID,
		Operation:      operation,
		ResponseJSON:   string(encoded),
		CreatedAt:      row.UpdatedAt,
	}); err != nil {
		return AccessServiceView{}, err
	}
	return view, ret
}

func accessOperationFingerprint(operation string, body []byte) string {
	sum := sha256.Sum256(append([]byte(operation+"\n"), body...))
	return hex.EncodeToString(sum[:])
}

func accessErrorStatus(err error) int {
	switch {
	case errors.Is(err, ErrConflict):
		return 409
	case errors.Is(err, ErrAccessServiceNotFound), errors.Is(err, ErrAgentNotFound), errors.Is(err, ErrRuleNotFound):
		return 404
	default:
		return 400
	}
}

func accessErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrConflict):
		return "conflict"
	case errors.Is(err, ErrAccessServiceNotFound), errors.Is(err, ErrAgentNotFound), errors.Is(err, ErrRuleNotFound):
		return "not_found"
	default:
		return "invalid"
	}
}

func newAccessServiceID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16]), nil
}

func parseAccessHTTPURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed == nil {
		return nil, fmt.Errorf("%w: invalid url", ErrInvalidArgument)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
	default:
		return nil, fmt.Errorf("%w: url scheme must be http or https", ErrInvalidArgument)
	}
	if parsed.Hostname() == "" {
		return nil, fmt.Errorf("%w: url host is required", ErrInvalidArgument)
	}
	return parsed, nil
}

func accessAddressClass(host string) string {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if strings.EqualFold(host, "localhost") {
		return accessAddressLoopback
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return accessAddressPublic
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	switch {
	case ip.IsLoopback():
		return accessAddressLoopback
	case ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast():
		return accessAddressLinkLocal
	case ip.IsPrivate():
		return accessAddressPrivate
	case ip.IsUnspecified():
		return accessAddressUnspecified
	default:
		return accessAddressPublic
	}
}

func accessAddressConstrained(class string) bool {
	switch class {
	case accessAddressLoopback, accessAddressLinkLocal, accessAddressPrivate, accessAddressUnspecified:
		return true
	default:
		return false
	}
}

func accessProbeReachable(status string) bool {
	return status == "connected" || status == "http_status"
}

type taskProbeClient struct{ tasks *TaskService }

func (c taskProbeClient) Dispatch(ctx context.Context, agentID string, payload map[string]any) (string, map[string]any, error) {
	if c.tasks == nil {
		return "", nil, errTaskSessionUnavailable
	}
	record, err := c.tasks.CreateAndDispatchContext(ctx, TaskCreateRequest{
		AgentID: agentID,
		Type:    TaskTypeProbeBackendReachability,
		Payload: payload,
	})
	if err != nil {
		return "", nil, err
	}
	if record.State == "completed" {
		return record.ID, record.Result, nil
	}
	return record.ID, nil, nil
}

func (c taskProbeClient) Result(ctx context.Context, agentID, taskID string) (map[string]any, bool, error) {
	if c.tasks == nil || strings.TrimSpace(taskID) == "" {
		return nil, false, nil
	}
	record, err := c.tasks.Get(ctx, agentID, taskID)
	if err != nil {
		if errors.Is(err, ErrTaskNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	switch record.State {
	case "completed":
		return record.Result, true, nil
	case "failed":
		return map[string]any{"status": "untested", "error": record.Error}, true, nil
	default:
		return nil, false, nil
	}
}
