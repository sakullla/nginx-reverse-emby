package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"reflect"
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

type ManagedCertificateHeartbeatReport struct {
	ID           int                        `json:"id"`
	Domain       string                     `json:"domain"`
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
	CertificatePEM  *string                                   `json:"certificate_pem,omitempty"`
	PrivateKeyPEM   *string                                   `json:"private_key_pem,omitempty"`
	CAPEM           *string                                   `json:"ca_pem,omitempty"`
	SelfSigned      *bool                                     `json:"self_signed,omitempty"`
}

type certificateService struct {
	cfg           config.Config
	store         storage.Store
	now           func() time.Time
	renewalIssuer managedCertificateRenewalIssuer
}

func NewCertificateService(cfg config.Config, store storage.Store) *certificateService {
	return newCertificateServiceWithRenewal(cfg, store, nil)
}

func newCertificateServiceWithRenewal(cfg config.Config, store storage.Store, issuer managedCertificateRenewalIssuer) *certificateService {
	return &certificateService{
		cfg:           cfg,
		store:         store,
		now:           time.Now,
		renewalIssuer: issuer,
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
	if err := assertManagedCertificateTargetingAllowed(s.cfg, cert); err != nil {
		return ManagedCertificate{}, err
	}
	if err := s.assertCertificateDistributionTargetsAllowed(ctx, cert); err != nil {
		return ManagedCertificate{}, err
	}
	uploadMaterial, hasUploadMaterial, err := s.resolveUploadedMaterialForMutation(ctx, input, cert, nil)
	if err != nil {
		return ManagedCertificate{}, err
	}
	if hasUploadMaterial {
		cert.MaterialHash = hashManagedCertificateMaterial(uploadMaterial.CertPEM, uploadMaterial.KeyPEM)
		if cert.Enabled && cert.IssuerMode == "local_http01" {
			cert.Status = "active"
			cert.LastIssueAt = s.now().UTC().Format(time.RFC3339)
			cert.LastError = ""
		}
	}
	cert.Revision = maxRevision + 1

	originalRows := make([]storage.ManagedCertificateRow, 0, len(current))
	rows := make([]storage.ManagedCertificateRow, 0, len(current)+1)
	for _, row := range current {
		originalRows = append(originalRows, row)
		rows = append(rows, row)
	}
	rows = append(rows, managedCertificateToRow(cert))
	if err := s.store.SaveManagedCertificates(ctx, rows); err != nil {
		return ManagedCertificate{}, err
	}
	if hasUploadMaterial {
		if err := s.store.SaveManagedCertificateMaterial(ctx, cert.Domain, uploadMaterial); err != nil {
			if rollbackErr := s.store.SaveManagedCertificates(ctx, originalRows); rollbackErr != nil {
				return ManagedCertificate{}, rollbackErr
			}
			cleanupManagedCertificateMaterialBestEffort(ctx, s.store, rows, originalRows)
			return ManagedCertificate{}, err
		}
	}
	cleanupManagedCertificateMaterialBestEffort(ctx, s.store, current, rows)
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
	if err := assertManagedCertificateTargetingAllowed(s.cfg, next); err != nil {
		return ManagedCertificate{}, err
	}
	if err := s.assertCertificateDistributionTargetsAllowed(ctx, next); err != nil {
		return ManagedCertificate{}, err
	}
	uploadMaterial, hasUploadMaterial, err := s.resolveUploadedMaterialForMutation(ctx, input, next, &current)
	if err != nil {
		return ManagedCertificate{}, err
	}
	if hasUploadMaterial {
		next.MaterialHash = hashManagedCertificateMaterial(uploadMaterial.CertPEM, uploadMaterial.KeyPEM)
		if next.Enabled && next.IssuerMode == "local_http01" {
			next.Status = "active"
			next.LastIssueAt = s.now().UTC().Format(time.RFC3339)
			next.LastError = ""
		}
	}
	next.Revision = maxRevision + 1
	rows[targetIndex] = managedCertificateToRow(next)
	originalRows := append([]storage.ManagedCertificateRow(nil), rows...)
	originalRows[targetIndex] = managedCertificateToRow(current)
	if err := s.store.SaveManagedCertificates(ctx, rows); err != nil {
		return ManagedCertificate{}, err
	}
	if hasUploadMaterial {
		if err := s.store.SaveManagedCertificateMaterial(ctx, next.Domain, uploadMaterial); err != nil {
			if rollbackErr := s.store.SaveManagedCertificates(ctx, originalRows); rollbackErr != nil {
				return ManagedCertificate{}, rollbackErr
			}
			cleanupManagedCertificateMaterialBestEffort(ctx, s.store, rows, originalRows)
			return ManagedCertificate{}, err
		}
	}
	cleanupManagedCertificateMaterialBestEffort(ctx, s.store, originalRows, rows)
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
		originalRows := append([]storage.ManagedCertificateRow(nil), rows...)
		rows[targetIndex] = managedCertificateToRow(next)
		if err := s.store.SaveManagedCertificates(ctx, rows); err != nil {
			return ManagedCertificate{}, err
		}
		cleanupManagedCertificateMaterialBestEffort(ctx, s.store, originalRows, rows)
		current.TargetAgentIDs = []string{resolvedID}
		return current, nil
	}

	nextRows := append([]storage.ManagedCertificateRow(nil), rows[:targetIndex]...)
	nextRows = append(nextRows, rows[targetIndex+1:]...)
	if err := s.store.SaveManagedCertificates(ctx, nextRows); err != nil {
		return ManagedCertificate{}, err
	}
	cleanupManagedCertificateMaterialBestEffort(ctx, s.store, rows, nextRows)
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

	if err := s.assertCertificateDistributionTargetsAllowed(ctx, current); err != nil {
		return ManagedCertificate{}, err
	}
	if current.CertificateType == "uploaded" && current.IssuerMode == "local_http01" {
		material, ok, err := s.store.LoadManagedCertificateMaterial(ctx, current.Domain)
		if err != nil {
			return ManagedCertificate{}, err
		}
		if !ok {
			return ManagedCertificate{}, fmt.Errorf("%w: certificate material not found", ErrInvalidArgument)
		}
		if err := validateUploadedManagedCertificateBundle(material); err != nil {
			return ManagedCertificate{}, err
		}
		current.MaterialHash = hashManagedCertificateMaterial(strings.TrimSpace(material.CertPEM), strings.TrimSpace(material.KeyPEM))
	}
	current.Status = "active"
	current.LastIssueAt = s.now().UTC().Format(time.RFC3339)
	current.LastError = ""
	current.Revision = maxRevision + 1
	originalRows := append([]storage.ManagedCertificateRow(nil), rows...)
	rows[targetIndex] = managedCertificateToRow(current)
	if err := s.store.SaveManagedCertificates(ctx, rows); err != nil {
		return ManagedCertificate{}, err
	}
	cleanupManagedCertificateMaterialBestEffort(ctx, s.store, originalRows, rows)
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

func (s *certificateService) assertCertificateDistributionTargetsAllowed(ctx context.Context, cert ManagedCertificate) error {
	if !cert.Enabled || cert.IssuerMode != "local_http01" || cert.CertificateType != "uploaded" {
		return nil
	}
	for _, targetAgentID := range cert.TargetAgentIDs {
		resolved, capabilities, err := s.resolveCertificateTargetCapabilities(ctx, targetAgentID)
		if errors.Is(err, ErrAgentNotFound) {
			return fmt.Errorf("%w: target agent not found: %s", ErrInvalidArgument, strings.TrimSpace(targetAgentID))
		}
		if err != nil {
			return err
		}
		if !agentHasCapability(capabilities, "cert_install") {
			return fmt.Errorf("%w: target agent does not support certificate install: %s", ErrInvalidArgument, resolved)
		}
	}
	return nil
}

func (s *certificateService) resolveCertificateTargetCapabilities(ctx context.Context, agentID string) (string, []string, error) {
	resolvedID := strings.TrimSpace(agentID)
	if resolvedID == "" {
		return "", nil, ErrAgentNotFound
	}
	if s.cfg.EnableLocalAgent && resolvedID == s.cfg.LocalAgentID {
		return resolvedID, append([]string(nil), defaultLocalCapabilities...), nil
	}

	rows, err := s.store.ListAgents(ctx)
	if err != nil {
		return "", nil, err
	}
	for _, row := range rows {
		if row.ID == resolvedID {
			return resolvedID, parseStringArray(row.CapabilitiesJSON), nil
		}
	}
	return "", nil, ErrAgentNotFound
}

func (s *certificateService) resolveUploadedMaterialForMutation(ctx context.Context, input ManagedCertificateInput, next ManagedCertificate, previous *ManagedCertificate) (storage.ManagedCertificateBundle, bool, error) {
	if next.CertificateType != "uploaded" {
		return storage.ManagedCertificateBundle{}, false, nil
	}

	hasCertificate := input.CertificatePEM != nil
	hasKey := input.PrivateKeyPEM != nil
	hasCA := input.CAPEM != nil
	if !hasCertificate && !hasKey && !hasCA {
		if previous == nil {
			return storage.ManagedCertificateBundle{}, false, fmt.Errorf("%w: certificate_pem is required for uploaded certificates", ErrInvalidArgument)
		}
		material, ok, err := s.store.LoadManagedCertificateMaterial(ctx, previous.Domain)
		if err != nil {
			return storage.ManagedCertificateBundle{}, false, err
		}
		if !ok {
			return storage.ManagedCertificateBundle{}, false, fmt.Errorf("%w: certificate_pem is required for uploaded certificates", ErrInvalidArgument)
		}
		material.Domain = next.Domain
		if err := validateUploadedManagedCertificateBundle(material); err != nil {
			return storage.ManagedCertificateBundle{}, false, err
		}
		return material, true, nil
	}

	certificatePEM := normalizeUploadedPEMField(input.CertificatePEM)
	privateKeyPEM := normalizeUploadedPEMField(input.PrivateKeyPEM)
	caPEM := normalizeUploadedPEMField(input.CAPEM)
	joinedCertificatePEM := joinUploadedCertificatePEM(certificatePEM, caPEM)
	bundle := storage.ManagedCertificateBundle{
		Domain:  next.Domain,
		CertPEM: joinedCertificatePEM,
		KeyPEM:  privateKeyPEM,
	}
	if err := validateUploadedManagedCertificateBundle(bundle); err != nil {
		return storage.ManagedCertificateBundle{}, false, err
	}
	bundle.CertPEM = strings.TrimSpace(bundle.CertPEM)
	bundle.KeyPEM = strings.TrimSpace(bundle.KeyPEM)
	return bundle, true, nil
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

func normalizeManagedCertificateHeartbeatReports(reports []ManagedCertificateHeartbeatReport) []ManagedCertificateHeartbeatReport {
	normalized := make([]ManagedCertificateHeartbeatReport, 0, len(reports))
	for _, report := range reports {
		next := ManagedCertificateHeartbeatReport{
			Domain:       normalizeCertificateReportHost(report.Domain),
			Status:       normalizeManagedCertificateReportStatus(report.Status),
			LastIssueAt:  normalizeOptionalTimestamp(report.LastIssueAt),
			LastError:    report.LastError,
			MaterialHash: strings.TrimSpace(report.MaterialHash),
			ACMEInfo:     report.ACMEInfo,
			UpdatedAt:    normalizeOptionalTimestamp(report.UpdatedAt),
		}
		if report.ID > 0 {
			next.ID = report.ID
		}
		if next.ID <= 0 && next.Domain == "" {
			continue
		}
		normalized = append(normalized, next)
	}
	return normalized
}

func applyManagedCertificateHeartbeatReports(rows []storage.ManagedCertificateRow, agentID string, reports []ManagedCertificateHeartbeatReport, now time.Time) ([]storage.ManagedCertificateRow, map[int]struct{}, bool) {
	if strings.TrimSpace(agentID) == "" || len(reports) == 0 {
		return rows, map[int]struct{}{}, false
	}

	reportsByID := make(map[int]ManagedCertificateHeartbeatReport, len(reports))
	reportsByDomain := make(map[string]ManagedCertificateHeartbeatReport, len(reports))
	for _, report := range normalizeManagedCertificateHeartbeatReports(reports) {
		if report.ID > 0 {
			reportsByID[report.ID] = report
		}
		if report.Domain != "" {
			reportsByDomain[report.Domain] = report
		}
	}

	reportedCertIDs := make(map[int]struct{}, len(reportsByID))
	changed := false
	nextRows := append([]storage.ManagedCertificateRow(nil), rows...)
	for index, row := range nextRows {
		cert := managedCertificateFromRow(row)
		if cert.IssuerMode != "local_http01" || !containsString(cert.TargetAgentIDs, agentID) {
			continue
		}
		report, ok := findManagedCertificateHeartbeatReport(cert, reportsByID, reportsByDomain)
		if !ok {
			continue
		}
		reportedCertIDs[cert.ID] = struct{}{}
		next := updateManagedCertificateAgentReport(cert, agentID, report, now)
		if len(cert.TargetAgentIDs) == 1 && cert.TargetAgentIDs[0] == agentID {
			next.Status = coalesceString(report.Status, cert.Status)
			next.LastIssueAt = report.LastIssueAt
			next.LastError = report.LastError
			next.MaterialHash = report.MaterialHash
			next.ACMEInfo = report.ACMEInfo
		}
		if !managedCertificateEqual(cert, next) {
			nextRows[index] = managedCertificateToRow(next)
			changed = true
		}
	}
	return nextRows, reportedCertIDs, changed
}

func reconcileLocalHTTP01CertificatesForAgent(rows []storage.ManagedCertificateRow, agentID string, rules []storage.HTTPRuleRow, applyRevision int, applyStatus string, applyMessage string, reportedCertIDs map[int]struct{}, now time.Time) ([]storage.ManagedCertificateRow, bool) {
	if strings.TrimSpace(agentID) == "" || applyRevision <= 0 {
		return rows, false
	}
	status := strings.ToLower(strings.TrimSpace(applyStatus))
	if status != "success" && status != "error" {
		return rows, false
	}

	appliedAt := now.UTC().Format(time.RFC3339)
	changed := false
	nextRows := append([]storage.ManagedCertificateRow(nil), rows...)
	for index, row := range nextRows {
		cert := managedCertificateFromRow(row)
		if !cert.Enabled || cert.IssuerMode != "local_http01" || !containsString(cert.TargetAgentIDs, agentID) {
			continue
		}
		if _, ok := reportedCertIDs[cert.ID]; ok {
			continue
		}
		if cert.Revision > applyRevision || !hasMatchingHTTPSRuleForCertificateInRows(rules, cert) {
			continue
		}

		switch status {
		case "success":
			next := updateManagedCertificateAgentReport(cert, agentID, ManagedCertificateHeartbeatReport{
				Status:       "active",
				LastIssueAt:  appliedAt,
				LastError:    "",
				MaterialHash: cert.MaterialHash,
				ACMEInfo:     cert.ACMEInfo,
				UpdatedAt:    appliedAt,
			}, now)
			next.Status = "active"
			next.LastIssueAt = appliedAt
			next.LastError = ""
			if !managedCertificateEqual(cert, next) {
				nextRows[index] = managedCertificateToRow(next)
				changed = true
			}
		case "error":
			if cert.Status != "pending" {
				continue
			}
			message := coalesceString(strings.TrimSpace(applyMessage), "agent apply failed")
			next := cert
			next.Status = "error"
			next.LastError = message
			next = updateManagedCertificateAgentReport(next, agentID, ManagedCertificateHeartbeatReport{
				Status:       "error",
				LastIssueAt:  cert.LastIssueAt,
				LastError:    message,
				MaterialHash: cert.MaterialHash,
				ACMEInfo:     cert.ACMEInfo,
				UpdatedAt:    appliedAt,
			}, now)
			if !managedCertificateEqual(cert, next) {
				nextRows[index] = managedCertificateToRow(next)
				changed = true
			}
		}
	}
	return nextRows, changed
}

func findManagedCertificateHeartbeatReport(cert ManagedCertificate, reportsByID map[int]ManagedCertificateHeartbeatReport, reportsByDomain map[string]ManagedCertificateHeartbeatReport) (ManagedCertificateHeartbeatReport, bool) {
	if cert.ID > 0 {
		if report, ok := reportsByID[cert.ID]; ok {
			return report, true
		}
	}
	report, ok := reportsByDomain[normalizeCertificateReportHost(cert.Domain)]
	return report, ok
}

func updateManagedCertificateAgentReport(cert ManagedCertificate, agentID string, report ManagedCertificateHeartbeatReport, now time.Time) ManagedCertificate {
	if cert.AgentReports == nil {
		cert.AgentReports = map[string]ManagedCertificateAgentReport{}
	}
	updatedAt := report.UpdatedAt
	if updatedAt == "" {
		updatedAt = now.UTC().Format(time.RFC3339)
	}
	cert.AgentReports[strings.TrimSpace(agentID)] = ManagedCertificateAgentReport{
		Status:       report.Status,
		LastIssueAt:  report.LastIssueAt,
		LastError:    report.LastError,
		MaterialHash: report.MaterialHash,
		ACMEInfo:     report.ACMEInfo,
		UpdatedAt:    updatedAt,
	}
	return cert
}

func hasMatchingHTTPSRuleForCertificateInRows(rows []storage.HTTPRuleRow, cert ManagedCertificate) bool {
	for _, row := range rows {
		if !row.Enabled {
			continue
		}
		target, ok := parseHTTPSRuleTarget(row.FrontendURL)
		if !ok {
			continue
		}
		if doesManagedCertificateMatchHost(cert, target) {
			return true
		}
	}
	return false
}

func parseHTTPSRuleTarget(frontendURL string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(frontendURL))
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") {
		return "", false
	}
	return normalizeCertificateReportHost(parsed.Hostname()), true
}

func normalizeCertificateReportHost(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(trimmed); err == nil {
		trimmed = host
	}
	return strings.ToLower(normalizeCertificateHost(trimmed))
}

func normalizeOptionalTimestamp(value string) string {
	return strings.TrimSpace(value)
}

func normalizeManagedCertificateReportStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "pending", "active", "error":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func managedCertificateEqual(left ManagedCertificate, right ManagedCertificate) bool {
	return reflect.DeepEqual(left, right)
}

func coalesceString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
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

func assertManagedCertificateTargetingAllowed(cfg config.Config, cert ManagedCertificate) error {
	if cert.IssuerMode != "master_cf_dns" {
		return nil
	}
	if cert.CertificateType != "acme" {
		return fmt.Errorf("%w: master_cf_dns certificates must use certificate_type=acme", ErrInvalidArgument)
	}
	localAgentID := strings.TrimSpace(cfg.LocalAgentID)
	if localAgentID == "" {
		return nil
	}
	if len(cert.TargetAgentIDs) != 1 || strings.TrimSpace(cert.TargetAgentIDs[0]) != localAgentID {
		return fmt.Errorf("%w: master_cf_dns certificates must target only the local master agent", ErrInvalidArgument)
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

func deriveRelayTrustMaterial(ctx context.Context, store storage.Store, cert ManagedCertificate, rows []storage.ManagedCertificateRow, pending []storage.ManagedCertificateBundle) (string, []RelayPin, []int, bool, error) {
	material, ok, err := loadManagedCertificateMaterial(ctx, store, cert.Domain, pending)
	if err != nil {
		return "", nil, nil, false, err
	}
	if !ok || strings.TrimSpace(material.CertPEM) == "" {
		return "", nil, nil, false, fmt.Errorf("%w: unable to derive relay listener trust material for certificate %d", ErrInvalidArgument, cert.ID)
	}
	pins, err := deriveRelayPinSetFromCertificate(material.CertPEM)
	if err != nil {
		return "", nil, nil, false, err
	}

	trustedCAIDs := []int{}
	if relayCA, ok := findRelayCACertificate(rows); ok {
		relayCABundle, relayCAOk, err := loadManagedCertificateMaterial(ctx, store, relayCA.Domain, pending)
		if err != nil {
			return "", nil, nil, false, err
		}
		if relayCAOk && certificateChainUsesRelayCA(material, relayCABundle) {
			trustedCAIDs = []int{relayCA.ID}
		}
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

func stableManagedCertificateMaterialHash(cert ManagedCertificate) string {
	return hashManagedCertificateMaterial(
		fmt.Sprintf("%d|%s|%s|%v|%s", cert.ID, cert.Domain, cert.Usage, cert.SelfSigned, strings.Join(cert.Tags, ",")),
		cert.CertificateType,
	)
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
