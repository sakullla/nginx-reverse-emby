package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

var ErrCertificateNotFound = errors.New("certificate not found")

const systemRelayCATag = "system:relay-ca"
const systemTag = "system"
const autoRelayListenerTag = "auto:relay-listener"
const relayCADomainIdentity = "__relay-ca.internal"

type ManagedCertificateACMEInfo struct {
	MainDomain string `json:"Main_Domain"`
	KeyLength  string `json:"KeyLength"`
	SANDomains string `json:"SAN_Domains"`
	Profile    string `json:"Profile"`
	CA         string `json:"CA"`
	Created    string `json:"Created"`
	Renew      string `json:"Renew"`
}

type ManagedCertificateAgentReport struct {
	Status       string                     `json:"status"`
	LastIssueAt  string                     `json:"last_issue_at"`
	LastError    string                     `json:"last_error"`
	MaterialHash string                     `json:"material_hash"`
	ACMEInfo     ManagedCertificateACMEInfo `json:"acme_info"`
	UpdatedAt    string                     `json:"updated_at"`
}

type ManagedCertificate struct {
	ID              int                                      `json:"id"`
	Domain          string                                   `json:"domain"`
	Enabled         bool                                     `json:"enabled"`
	Scope           string                                   `json:"scope"`
	IssuerMode      string                                   `json:"issuer_mode"`
	TargetAgentIDs  []string                                 `json:"target_agent_ids"`
	Status          string                                   `json:"status"`
	LastIssueAt     string                                   `json:"last_issue_at"`
	LastError       string                                   `json:"last_error"`
	MaterialHash    string                                   `json:"material_hash"`
	AgentReports    map[string]ManagedCertificateAgentReport `json:"agent_reports"`
	ACMEInfo        ManagedCertificateACMEInfo               `json:"acme_info"`
	Tags            []string                                 `json:"tags"`
	Usage           string                                   `json:"usage"`
	CertificateType string                                   `json:"certificate_type"`
	SelfSigned      bool                                     `json:"self_signed"`
	Revision        int                                      `json:"revision"`
}

type ManagedCertificateInput struct {
	ID              *int                                      `json:"id,omitempty"`
	Domain          *string                                   `json:"domain,omitempty"`
	Enabled         *bool                                     `json:"enabled,omitempty"`
	Scope           *string                                   `json:"scope,omitempty"`
	IssuerMode      *string                                   `json:"issuer_mode,omitempty"`
	TargetAgentIDs  *[]string                                 `json:"target_agent_ids,omitempty"`
	Status          *string                                   `json:"status,omitempty"`
	LastIssueAt     *string                                   `json:"last_issue_at,omitempty"`
	LastError       *string                                   `json:"last_error,omitempty"`
	MaterialHash    *string                                   `json:"material_hash,omitempty"`
	AgentReports    *map[string]ManagedCertificateAgentReport `json:"agent_reports,omitempty"`
	ACMEInfo        *ManagedCertificateACMEInfo               `json:"acme_info,omitempty"`
	Tags            *[]string                                 `json:"tags,omitempty"`
	Usage           *string                                   `json:"usage,omitempty"`
	CertificateType *string                                   `json:"certificate_type,omitempty"`
	SelfSigned      *bool                                     `json:"self_signed,omitempty"`
}

type certificateService struct {
	cfg   config.Config
	store storage.Store
	now   func() time.Time
}

func NewCertificateService(cfg config.Config, store storage.Store) *certificateService {
	return &certificateService{
		cfg:   cfg,
		store: store,
		now:   time.Now,
	}
}

func (s *certificateService) List(ctx context.Context, agentID string) ([]ManagedCertificate, error) {
	resolvedID, err := s.ensureAgentExists(ctx, agentID)
	if err != nil {
		return nil, err
	}

	rows, err := s.store.ListManagedCertificates(ctx)
	if err != nil {
		return nil, err
	}

	certs := make([]ManagedCertificate, 0, len(rows))
	for _, row := range rows {
		cert := managedCertificateFromRow(row)
		if containsString(cert.TargetAgentIDs, resolvedID) {
			cert = overlayManagedCertificateForAgent(cert, resolvedID)
			certs = append(certs, cert)
		}
	}
	return certs, nil
}

func (s *certificateService) Create(ctx context.Context, agentID string, input ManagedCertificateInput) (ManagedCertificate, error) {
	resolvedID, err := s.ensureAgentExists(ctx, agentID)
	if err != nil {
		return ManagedCertificate{}, err
	}

	current, err := s.store.ListManagedCertificates(ctx)
	if err != nil {
		return ManagedCertificate{}, err
	}

	maxID := 0
	maxRevision := 0
	for _, row := range current {
		if row.ID > maxID {
			maxID = row.ID
		}
		if row.Revision > maxRevision {
			maxRevision = row.Revision
		}
	}

	cert, err := normalizeManagedCertificateInput(input, ManagedCertificate{}, maxID+1, resolvedID)
	if err != nil {
		return ManagedCertificate{}, err
	}
	if err := assertManagedCertificateMutationAllowed(nil, cert); err != nil {
		return ManagedCertificate{}, err
	}
	cert.Revision = maxRevision + 1

	rows := make([]storage.ManagedCertificateRow, 0, len(current)+1)
	for _, row := range current {
		rows = append(rows, row)
	}
	rows = append(rows, managedCertificateToRow(cert))
	if err := s.store.SaveManagedCertificates(ctx, rows); err != nil {
		return ManagedCertificate{}, err
	}
	return cert, nil
}

func (s *certificateService) Update(ctx context.Context, agentID string, id int, input ManagedCertificateInput) (ManagedCertificate, error) {
	resolvedID, err := s.ensureAgentExists(ctx, agentID)
	if err != nil {
		return ManagedCertificate{}, err
	}

	rows, err := s.store.ListManagedCertificates(ctx)
	if err != nil {
		return ManagedCertificate{}, err
	}

	maxRevision := 0
	targetIndex := -1
	var current ManagedCertificate
	for i, row := range rows {
		if row.Revision > maxRevision {
			maxRevision = row.Revision
		}
		cert := managedCertificateFromRow(row)
		if cert.ID == id && containsString(cert.TargetAgentIDs, resolvedID) {
			targetIndex = i
			current = cert
		}
	}
	if targetIndex < 0 {
		return ManagedCertificate{}, ErrCertificateNotFound
	}

	next, err := normalizeManagedCertificateInput(input, current, id, resolvedID)
	if err != nil {
		return ManagedCertificate{}, err
	}
	if err := assertManagedCertificateMutationAllowed(&current, next); err != nil {
		return ManagedCertificate{}, err
	}
	next.Revision = maxRevision + 1
	rows[targetIndex] = managedCertificateToRow(next)
	if err := s.store.SaveManagedCertificates(ctx, rows); err != nil {
		return ManagedCertificate{}, err
	}
	return next, nil
}

func (s *certificateService) Delete(ctx context.Context, agentID string, id int) (ManagedCertificate, error) {
	resolvedID, err := s.ensureAgentExists(ctx, agentID)
	if err != nil {
		return ManagedCertificate{}, err
	}

	rows, err := s.store.ListManagedCertificates(ctx)
	if err != nil {
		return ManagedCertificate{}, err
	}

	maxRevision := 0
	targetIndex := -1
	var current ManagedCertificate
	for i, row := range rows {
		if row.Revision > maxRevision {
			maxRevision = row.Revision
		}
		cert := managedCertificateFromRow(row)
		if cert.ID == id && containsString(cert.TargetAgentIDs, resolvedID) {
			targetIndex = i
			current = cert
		}
	}
	if targetIndex < 0 {
		return ManagedCertificate{}, ErrCertificateNotFound
	}
	if isSystemRelayCACertificate(current) {
		return ManagedCertificate{}, fmt.Errorf("%w: system relay ca cannot be deleted", ErrInvalidArgument)
	}

	if len(current.TargetAgentIDs) > 1 {
		nextTargets := removeString(current.TargetAgentIDs, resolvedID)
		next := current
		next.TargetAgentIDs = nextTargets
		next.Revision = maxRevision + 1
		rows[targetIndex] = managedCertificateToRow(next)
		if err := s.store.SaveManagedCertificates(ctx, rows); err != nil {
			return ManagedCertificate{}, err
		}
		current.TargetAgentIDs = []string{resolvedID}
		return current, nil
	}

	nextRows := append([]storage.ManagedCertificateRow(nil), rows[:targetIndex]...)
	nextRows = append(nextRows, rows[targetIndex+1:]...)
	if err := s.store.SaveManagedCertificates(ctx, nextRows); err != nil {
		return ManagedCertificate{}, err
	}
	return current, nil
}

func (s *certificateService) Issue(ctx context.Context, agentID string, id int) (ManagedCertificate, error) {
	resolvedID, err := s.ensureAgentExists(ctx, agentID)
	if err != nil {
		return ManagedCertificate{}, err
	}

	rows, err := s.store.ListManagedCertificates(ctx)
	if err != nil {
		return ManagedCertificate{}, err
	}

	maxRevision := 0
	targetIndex := -1
	var current ManagedCertificate
	for i, row := range rows {
		if row.Revision > maxRevision {
			maxRevision = row.Revision
		}
		cert := managedCertificateFromRow(row)
		if cert.ID == id && containsString(cert.TargetAgentIDs, resolvedID) {
			targetIndex = i
			current = cert
		}
	}
	if targetIndex < 0 {
		return ManagedCertificate{}, ErrCertificateNotFound
	}

	current.Status = "active"
	current.LastIssueAt = s.now().UTC().Format(time.RFC3339)
	current.LastError = ""
	current.Revision = maxRevision + 1
	rows[targetIndex] = managedCertificateToRow(current)
	if err := s.store.SaveManagedCertificates(ctx, rows); err != nil {
		return ManagedCertificate{}, err
	}
	return current, nil
}

func (s *certificateService) ensureAgentExists(ctx context.Context, agentID string) (string, error) {
	resolvedID := strings.TrimSpace(agentID)
	if resolvedID == "" {
		resolvedID = s.cfg.LocalAgentID
	}
	if s.cfg.EnableLocalAgent && resolvedID == s.cfg.LocalAgentID {
		return resolvedID, nil
	}

	rows, err := s.store.ListAgents(ctx)
	if err != nil {
		return "", err
	}
	for _, row := range rows {
		if row.ID == resolvedID {
			return resolvedID, nil
		}
	}
	return "", ErrAgentNotFound
}

func normalizeManagedCertificateInput(input ManagedCertificateInput, fallback ManagedCertificate, suggestedID int, defaultAgentID string) (ManagedCertificate, error) {
	id := fallback.ID
	if input.ID != nil && *input.ID > 0 {
		id = *input.ID
	}
	if id <= 0 {
		id = suggestedID
	}

	domain := strings.TrimSpace(pointerString(input.Domain))
	if domain == "" {
		domain = strings.TrimSpace(fallback.Domain)
	}
	if domain == "" {
		return ManagedCertificate{}, fmt.Errorf("%w: domain must be a valid domain or IP", ErrInvalidArgument)
	}

	enabled := true
	if fallback.ID > 0 {
		enabled = fallback.Enabled
	}
	if input.Enabled != nil {
		enabled = *input.Enabled
	}

	scope := strings.TrimSpace(pointerString(input.Scope))
	if scope == "" {
		scope = fallback.Scope
	}
	if scope == "" {
		scope = "domain"
	}
	if scope != "domain" && scope != "ip" {
		return ManagedCertificate{}, fmt.Errorf("%w: scope must be domain or ip", ErrInvalidArgument)
	}

	issuerMode := strings.TrimSpace(pointerString(input.IssuerMode))
	if issuerMode == "" {
		issuerMode = fallback.IssuerMode
	}
	if issuerMode == "" {
		issuerMode = "master_cf_dns"
	}
	if issuerMode != "master_cf_dns" && issuerMode != "local_http01" {
		return ManagedCertificate{}, fmt.Errorf("%w: issuer_mode must be master_cf_dns or local_http01", ErrInvalidArgument)
	}
	if scope == "ip" && issuerMode != "local_http01" {
		return ManagedCertificate{}, fmt.Errorf("%w: ip certificates must use local_http01", ErrInvalidArgument)
	}

	targetAgentIDs := append([]string(nil), fallback.TargetAgentIDs...)
	if input.TargetAgentIDs != nil {
		targetAgentIDs = normalizeTags(*input.TargetAgentIDs)
	}
	if len(targetAgentIDs) == 0 {
		targetAgentIDs = []string{defaultAgentID}
	}

	status := strings.TrimSpace(pointerString(input.Status))
	if status == "" {
		status = fallback.Status
	}
	if status == "" {
		status = "pending"
	}

	lastIssueAt := strings.TrimSpace(pointerString(input.LastIssueAt))
	if lastIssueAt == "" {
		lastIssueAt = fallback.LastIssueAt
	}

	lastError := strings.TrimSpace(pointerString(input.LastError))
	if lastError == "" {
		lastError = fallback.LastError
	}

	materialHash := strings.TrimSpace(pointerString(input.MaterialHash))
	if materialHash == "" {
		materialHash = fallback.MaterialHash
	}

	agentReports := fallback.AgentReports
	if agentReports == nil {
		agentReports = map[string]ManagedCertificateAgentReport{}
	}
	if input.AgentReports != nil {
		agentReports = *input.AgentReports
	}

	acmeInfo := fallback.ACMEInfo
	if input.ACMEInfo != nil {
		acmeInfo = *input.ACMEInfo
	}

	tags := append([]string(nil), fallback.Tags...)
	if input.Tags != nil {
		tags = normalizeTags(*input.Tags)
	}

	usage := strings.TrimSpace(pointerString(input.Usage))
	if usage == "" {
		usage = fallback.Usage
	}
	if usage == "" {
		usage = "https"
	}
	switch usage {
	case "https", "relay_tunnel", "relay_ca", "mixed":
	default:
		return ManagedCertificate{}, fmt.Errorf("%w: usage must be https, relay_tunnel, relay_ca, or mixed", ErrInvalidArgument)
	}

	certificateType := strings.TrimSpace(pointerString(input.CertificateType))
	if certificateType == "" {
		certificateType = fallback.CertificateType
	}
	if certificateType == "" {
		certificateType = "acme"
	}
	switch certificateType {
	case "acme", "uploaded", "internal_ca":
	default:
		return ManagedCertificate{}, fmt.Errorf("%w: certificate_type must be acme, uploaded, or internal_ca", ErrInvalidArgument)
	}

	selfSigned := fallback.SelfSigned
	if input.SelfSigned != nil {
		selfSigned = *input.SelfSigned
	}

	return ManagedCertificate{
		ID:              id,
		Domain:          domain,
		Enabled:         enabled,
		Scope:           scope,
		IssuerMode:      issuerMode,
		TargetAgentIDs:  targetAgentIDs,
		Status:          status,
		LastIssueAt:     lastIssueAt,
		LastError:       lastError,
		MaterialHash:    materialHash,
		AgentReports:    agentReports,
		ACMEInfo:        acmeInfo,
		Tags:            tags,
		Usage:           usage,
		CertificateType: certificateType,
		SelfSigned:      selfSigned,
		Revision:        fallback.Revision,
	}, nil
}

func managedCertificateFromRow(row storage.ManagedCertificateRow) ManagedCertificate {
	cert := ManagedCertificate{
		ID:              row.ID,
		Domain:          row.Domain,
		Enabled:         row.Enabled,
		Scope:           defaultString(row.Scope, "domain"),
		IssuerMode:      defaultString(row.IssuerMode, "master_cf_dns"),
		Status:          defaultString(row.Status, "pending"),
		LastIssueAt:     row.LastIssueAt,
		LastError:       row.LastError,
		MaterialHash:    row.MaterialHash,
		Tags:            parseStringArray(row.TagsJSON),
		Usage:           defaultString(row.Usage, "https"),
		CertificateType: defaultString(row.CertificateType, "acme"),
		SelfSigned:      row.SelfSigned,
		Revision:        row.Revision,
		AgentReports:    map[string]ManagedCertificateAgentReport{},
	}
	cert.TargetAgentIDs = parseStringArray(row.TargetAgentIDs)
	_ = json.Unmarshal([]byte(defaultString(row.AgentReports, "{}")), &cert.AgentReports)
	_ = json.Unmarshal([]byte(defaultString(row.ACMEInfo, "{}")), &cert.ACMEInfo)
	return cert
}

func managedCertificateToRow(cert ManagedCertificate) storage.ManagedCertificateRow {
	return storage.ManagedCertificateRow{
		ID:              cert.ID,
		Domain:          cert.Domain,
		Enabled:         cert.Enabled,
		Scope:           cert.Scope,
		IssuerMode:      cert.IssuerMode,
		TargetAgentIDs:  marshalJSON(cert.TargetAgentIDs, "[]"),
		Status:          cert.Status,
		LastIssueAt:     cert.LastIssueAt,
		LastError:       cert.LastError,
		MaterialHash:    cert.MaterialHash,
		AgentReports:    marshalJSON(cert.AgentReports, "{}"),
		ACMEInfo:        marshalJSON(cert.ACMEInfo, "{}"),
		Usage:           cert.Usage,
		CertificateType: cert.CertificateType,
		SelfSigned:      cert.SelfSigned,
		TagsJSON:        marshalJSON(cert.Tags, "[]"),
		Revision:        cert.Revision,
	}
}

func overlayManagedCertificateForAgent(cert ManagedCertificate, agentID string) ManagedCertificate {
	report, ok := cert.AgentReports[agentID]
	if !ok {
		return cert
	}
	cert.Status = report.Status
	cert.LastIssueAt = report.LastIssueAt
	cert.LastError = report.LastError
	cert.MaterialHash = report.MaterialHash
	cert.ACMEInfo = report.ACMEInfo
	return cert
}

func assertManagedCertificateMutationAllowed(previous *ManagedCertificate, next ManagedCertificate) error {
	if previous != nil && isSystemRelayCACertificate(*previous) {
		return fmt.Errorf("%w: system relay ca is managed automatically", ErrInvalidArgument)
	}
	if isReservedSystemRelayCANext(next) {
		if usesReservedRelayCAIdentity(next) {
			return fmt.Errorf("%w: relay ca domain and system tag are reserved for the system relay ca", ErrInvalidArgument)
		}
		return fmt.Errorf("%w: relay ca certificates are managed automatically", ErrInvalidArgument)
	}
	return nil
}

func isReservedSystemRelayCANext(cert ManagedCertificate) bool {
	return cert.Usage == "relay_ca" || usesReservedRelayCAIdentity(cert)
}

func isSystemRelayCACertificate(cert ManagedCertificate) bool {
	return cert.Usage == "relay_ca" || usesReservedRelayCATags(cert.Tags)
}

func usesReservedRelayCATags(tags []string) bool {
	if containsString(tags, systemRelayCATag) {
		return true
	}
	return containsString(tags, "relay-ca") && containsString(tags, systemTag)
}

func usesReservedRelayCAIdentity(cert ManagedCertificate) bool {
	return strings.EqualFold(strings.TrimSpace(cert.Domain), relayCADomainIdentity) || usesReservedRelayCATags(cert.Tags)
}

func isAutoRelayListenerCertificate(cert ManagedCertificate, listenerID int) bool {
	if cert.Usage != "relay_tunnel" || cert.CertificateType != "internal_ca" {
		return false
	}
	if !containsString(cert.Tags, "auto") || !containsString(cert.Tags, autoRelayListenerTag) {
		return false
	}
	if listenerID <= 0 {
		return true
	}
	return containsString(cert.Tags, relayListenerTag(listenerID))
}

func relayListenerTag(listenerID int) string {
	return fmt.Sprintf("listener:%d", listenerID)
}

func relayAgentTag(agentID string) string {
	return fmt.Sprintf("agent:%s", strings.TrimSpace(agentID))
}

func autoRelayListenerCertificateTags(listenerID int, agentID string) []string {
	return normalizeTags([]string{
		"relay",
		"auto",
		autoRelayListenerTag,
		relayListenerTag(listenerID),
		relayAgentTag(agentID),
	})
}

func findManagedCertificateByID(rows []storage.ManagedCertificateRow, certID int) (ManagedCertificate, int, bool) {
	for index, row := range rows {
		cert := managedCertificateFromRow(row)
		if cert.ID == certID {
			return cert, index, true
		}
	}
	return ManagedCertificate{}, -1, false
}

func findRelayCACertificate(rows []storage.ManagedCertificateRow) (ManagedCertificate, bool) {
	for _, row := range rows {
		cert := managedCertificateFromRow(row)
		if isSystemRelayCACertificate(cert) {
			return cert, true
		}
	}
	return ManagedCertificate{}, false
}

func deriveRelayTrustMaterial(cert ManagedCertificate, rows []storage.ManagedCertificateRow) (string, []RelayPin, []int, bool, error) {
	pins := []RelayPin{syntheticRelayPin(cert)}
	trustedCAIDs := []int{}
	if relayCA, ok := findRelayCACertificate(rows); ok && cert.CertificateType == "internal_ca" {
		trustedCAIDs = []int{relayCA.ID}
	}
	allowSelfSigned := cert.SelfSigned || len(trustedCAIDs) > 0
	if len(pins) > 0 && len(trustedCAIDs) > 0 {
		return "pin_and_ca", pins, trustedCAIDs, allowSelfSigned, nil
	}
	if len(pins) > 0 {
		return "pin_only", pins, trustedCAIDs, allowSelfSigned, nil
	}
	if len(trustedCAIDs) > 0 {
		return "ca_only", []RelayPin{}, trustedCAIDs, allowSelfSigned, nil
	}
	return "", nil, nil, false, fmt.Errorf("%w: unable to derive relay listener trust material for certificate %d", ErrInvalidArgument, cert.ID)
}

func syntheticRelayPin(cert ManagedCertificate) RelayPin {
	material := strings.TrimSpace(cert.MaterialHash)
	if material == "" {
		material = stableManagedCertificateMaterialHash(cert)
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d|%s|%s|%s", cert.ID, cert.Domain, material, cert.CertificateType)))
	return RelayPin{
		Type:  "spki_sha256",
		Value: base64.StdEncoding.EncodeToString(sum[:]),
	}
}

func stableManagedCertificateMaterialHash(cert ManagedCertificate) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d|%s|%s|%v|%s", cert.ID, cert.Domain, cert.Usage, cert.SelfSigned, strings.Join(cert.Tags, ","))))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func removeString(values []string, target string) []string {
	next := make([]string, 0, len(values))
	for _, value := range values {
		if value != target {
			next = append(next, value)
		}
	}
	return next
}
