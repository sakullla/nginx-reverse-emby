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
	cfg               config.Config
	store             storage.Store
	now               func() time.Time
	renewalIssuer     managedCertificateRenewalIssuer
	localApplyTrigger func(context.Context) error
}

type localManagedCertificateSyncStore interface {
	LoadLocalSnapshot(context.Context, string) (storage.Snapshot, error)
	SaveLocalRuntimeState(context.Context, string, storage.RuntimeState) error
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

func (s *certificateService) SetLocalApplyTrigger(trigger func(context.Context) error) {
	s.localApplyTrigger = trigger
}

func (s *certificateService) List(ctx context.Context, agentID string) ([]ManagedCertificate, error) {
	if strings.TrimSpace(agentID) == "" {
		rows, err := s.store.ListManagedCertificates(ctx)
		if err != nil {
			return nil, err
		}
		certs := make([]ManagedCertificate, 0, len(rows))
		for _, row := range rows {
			certs = append(certs, managedCertificateFromRow(row))
		}
		return certs, nil
	}

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
	resolvedID := strings.TrimSpace(agentID)
	var err error
	if resolvedID != "" {
		resolvedID, err = s.ensureAgentExists(ctx, resolvedID)
		if err != nil {
			return ManagedCertificate{}, err
		}
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

	allowEmptyTargets := resolvedID == ""
	cert, err := normalizeManagedCertificateInput(input, ManagedCertificate{}, maxID+1, resolvedID, allowEmptyTargets)
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
			cert.Status = "pending"
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
	return s.finishManagedCertificateMutation(ctx, rows, len(rows)-1, nil, cert, maxRevision)
}

func (s *certificateService) Update(ctx context.Context, agentID string, id int, input ManagedCertificateInput) (ManagedCertificate, error) {
	resolvedID := strings.TrimSpace(agentID)
	var err error
	if resolvedID != "" {
		resolvedID, err = s.ensureAgentExists(ctx, resolvedID)
		if err != nil {
			return ManagedCertificate{}, err
		}
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
		if cert.ID == id && (resolvedID == "" || containsString(cert.TargetAgentIDs, resolvedID)) {
			targetIndex = i
			current = cert
		}
	}
	if targetIndex < 0 {
		return ManagedCertificate{}, ErrCertificateNotFound
	}

	defaultAgentID := resolvedID
	if defaultAgentID == "" {
		defaultAgentID = s.cfg.LocalAgentID
	}
	allowEmptyTargets := resolvedID == ""
	next, err := normalizeManagedCertificateInput(input, current, id, defaultAgentID, allowEmptyTargets)
	if err != nil {
		return ManagedCertificate{}, err
	}
	if err := assertManagedCertificateMutationAllowed(&current, next); err != nil {
		return ManagedCertificate{}, err
	}
	if current.CertificateType == "uploaded" && next.CertificateType != "uploaded" {
		return ManagedCertificate{}, fmt.Errorf("%w: cannot change certificate_type from uploaded to %s", ErrInvalidArgument, next.CertificateType)
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
			next.Status = "pending"
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
		previousMaterial := storage.ManagedCertificateBundle{}
		previousMaterialFound := false
		if strings.EqualFold(strings.TrimSpace(current.Domain), strings.TrimSpace(next.Domain)) {
			loaded, ok, loadErr := s.store.LoadManagedCertificateMaterial(ctx, current.Domain)
			if loadErr != nil {
				return ManagedCertificate{}, loadErr
			}
			if ok {
				previousMaterial = loaded
				previousMaterialFound = true
			}
		}
		if err := s.store.SaveManagedCertificateMaterial(ctx, next.Domain, uploadMaterial); err != nil {
			if rollbackErr := s.store.SaveManagedCertificates(ctx, originalRows); rollbackErr != nil {
				return ManagedCertificate{}, rollbackErr
			}
			if previousMaterialFound {
				if restoreErr := s.store.SaveManagedCertificateMaterial(ctx, current.Domain, previousMaterial); restoreErr != nil {
					return ManagedCertificate{}, fmt.Errorf("persist uploaded certificate material: %w (restore failed: %v)", err, restoreErr)
				}
			}
			cleanupManagedCertificateMaterialBestEffort(ctx, s.store, rows, originalRows)
			return ManagedCertificate{}, err
		}
	}
	cleanupManagedCertificateMaterialBestEffort(ctx, s.store, originalRows, rows)
	return s.finishManagedCertificateMutation(ctx, rows, targetIndex, &current, next, maxRevision)
}

func (s *certificateService) Delete(ctx context.Context, agentID string, id int) (ManagedCertificate, error) {
	resolvedID := strings.TrimSpace(agentID)
	var err error
	if resolvedID != "" {
		resolvedID, err = s.ensureAgentExists(ctx, resolvedID)
		if err != nil {
			return ManagedCertificate{}, err
		}
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
		if cert.ID == id && (resolvedID == "" || containsString(cert.TargetAgentIDs, resolvedID)) {
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
	if err := s.assertManagedCertificateNotReferencedByRelayListener(ctx, current); err != nil {
		return ManagedCertificate{}, err
	}

	if resolvedID != "" && len(current.TargetAgentIDs) > 1 {
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

func (s *certificateService) assertManagedCertificateNotReferencedByRelayListener(ctx context.Context, cert ManagedCertificate) error {
	if !isAutoRelayListenerCertificate(cert, 0) {
		return nil
	}

	rows, err := s.store.ListRelayListeners(ctx, "")
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.CertificateID == nil || *row.CertificateID != cert.ID {
			continue
		}
		return fmt.Errorf("%w: certificate %d is referenced by relay listener %d on agent %s", ErrInvalidArgument, cert.ID, row.ID, strings.TrimSpace(row.AgentID))
	}
	return nil
}

func (s *certificateService) Issue(ctx context.Context, agentID string, id int) (ManagedCertificate, error) {
	rows, err := s.store.ListManagedCertificates(ctx)
	if err != nil {
		return ManagedCertificate{}, err
	}

	maxRevision := 0
	for _, row := range rows {
		if row.Revision > maxRevision {
			maxRevision = row.Revision
		}
	}

	current, targetIndex, ok := findManagedCertificateByID(rows, id)
	if !ok {
		return ManagedCertificate{}, ErrCertificateNotFound
	}
	requestedAgentID := strings.TrimSpace(agentID)
	if current.IssuerMode == "local_http01" && current.CertificateType == "acme" {
		return s.issueLocalHTTP01ACME(ctx, rows, targetIndex, current, maxRevision, requestedAgentID)
	}
	if current.IssuerMode == "local_http01" && current.CertificateType == "internal_ca" {
		return s.issueLocalHTTP01InternalCA(ctx, rows, targetIndex, current, maxRevision, requestedAgentID)
	}

	resolvedID := ""
	if requestedAgentID != "" {
		resolvedID, err = s.ensureAgentExists(ctx, requestedAgentID)
		if err != nil {
			return ManagedCertificate{}, err
		}
		if !containsString(current.TargetAgentIDs, resolvedID) {
			return ManagedCertificate{}, ErrCertificateNotFound
		}
	}

	if err := s.assertCertificateDistributionTargetsAllowed(ctx, current); err != nil {
		return ManagedCertificate{}, err
	}
	if current.IssuerMode == "master_cf_dns" {
		if err := s.assertManagedCertificateManualIssueAllowed(current); err != nil {
			return ManagedCertificate{}, err
		}
		issuer := s.renewalIssuer
		if issuer == nil && s.cfg.ManagedDNSCertificatesEnabled {
			issuer = newMasterCFDNSManagedCertificateIssuer()
		}
		if issuer == nil {
			return ManagedCertificate{}, fmt.Errorf("%w: managed certificates require ACME_DNS_PROVIDER=cf and CF_Token", ErrInvalidArgument)
		}

		issueResult, err := issuer.Issue(ctx, current)
		if err != nil {
			return s.failManagedCertificateIssue(ctx, rows, targetIndex, current, maxRevision, err, true)
		}
		issuedMaterial, err := resolveManagedCertificateIssueMaterial(current, issueResult)
		if err != nil {
			return s.failManagedCertificateIssue(ctx, rows, targetIndex, current, maxRevision, err, true)
		}

		previousMaterial, previousMaterialFound, err := s.store.LoadManagedCertificateMaterial(ctx, current.Domain)
		if err != nil {
			return ManagedCertificate{}, err
		}
		if err := s.store.SaveManagedCertificateMaterial(ctx, current.Domain, issuedMaterial); err != nil {
			if restoreErr := s.restoreManagedCertificateMaterialAfterIssueFailure(ctx, current, previousMaterial, previousMaterialFound); restoreErr != nil {
				return ManagedCertificate{}, fmt.Errorf("persist issued certificate material: %w (restore failed: %v)", err, restoreErr)
			}
			return s.failManagedCertificateIssue(ctx, rows, targetIndex, current, maxRevision, err, true)
		}

		next := current
		next.Status = "active"
		next.LastIssueAt = issueResult.LastIssueAt
		if strings.TrimSpace(next.LastIssueAt) == "" {
			next.LastIssueAt = s.now().UTC().Format(time.RFC3339)
		}
		next.LastError = ""
		next.MaterialHash = issueResult.MaterialHash
		if strings.TrimSpace(next.MaterialHash) == "" {
			next.MaterialHash = hashManagedCertificateMaterial(strings.TrimSpace(issuedMaterial.CertPEM), strings.TrimSpace(issuedMaterial.KeyPEM))
		}
		next.ACMEInfo = issueResult.ACMEInfo
		next.Revision = managedCertificateMutationRevision(current, maxRevision, true)
		originalRows := append([]storage.ManagedCertificateRow(nil), rows...)
		rows[targetIndex] = managedCertificateToRow(next)
		if err := s.store.SaveManagedCertificates(ctx, rows); err != nil {
			if restoreErr := s.restoreManagedCertificateMaterialAfterIssueFailure(ctx, current, previousMaterial, previousMaterialFound); restoreErr != nil {
				return ManagedCertificate{}, fmt.Errorf("save issued certificate metadata: %w (restore failed: %v)", err, restoreErr)
			}
			return ManagedCertificate{}, err
		}
		cleanupManagedCertificateMaterialBestEffort(ctx, s.store, originalRows, rows)
		return next, nil
	}
	if current.CertificateType == "uploaded" && current.IssuerMode == "local_http01" {
		return s.syncStaticLocalCertificate(ctx, rows, targetIndex, current, maxRevision, resolvedID, true)
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

func (s *certificateService) issueLocalHTTP01InternalCA(ctx context.Context, rows []storage.ManagedCertificateRow, targetIndex int, current ManagedCertificate, maxRevision int, requestedAgentID string) (ManagedCertificate, error) {
	if !current.Enabled {
		return ManagedCertificate{}, fmt.Errorf("%w: certificate is disabled", ErrInvalidArgument)
	}
	if current.IssuerMode != "local_http01" {
		return ManagedCertificate{}, fmt.Errorf("%w: certificate is not configured for local_http01", ErrInvalidArgument)
	}

	requestedTargetIDs := append([]string(nil), current.TargetAgentIDs...)
	if requestedAgentID != "" {
		requestedAgentID = strings.TrimSpace(requestedAgentID)
		requestedTargetIDs = requestedTargetIDs[:0]
		for _, targetAgentID := range current.TargetAgentIDs {
			if strings.TrimSpace(targetAgentID) == requestedAgentID {
				requestedTargetIDs = append(requestedTargetIDs, requestedAgentID)
			}
		}
	}
	if requestedAgentID != "" && len(requestedTargetIDs) == 0 {
		return ManagedCertificate{}, fmt.Errorf("%w: certificate is not assigned to the requested agent", ErrInvalidArgument)
	}

	for _, targetAgentID := range requestedTargetIDs {
		_, displayName, capabilities, err := s.resolveCertificateTarget(ctx, targetAgentID)
		if errors.Is(err, ErrAgentNotFound) {
			return ManagedCertificate{}, fmt.Errorf("%w: target agent not found: %s", ErrInvalidArgument, strings.TrimSpace(targetAgentID))
		}
		if err != nil {
			return ManagedCertificate{}, err
		}
		if !agentHasCapability(capabilities, "cert_install") {
			return ManagedCertificate{}, fmt.Errorf("%w: target agent does not support certificate install: %s", ErrInvalidArgument, displayName)
		}
	}

	material, materialFound, err := s.store.LoadManagedCertificateMaterial(ctx, current.Domain)
	if err != nil {
		return ManagedCertificate{}, err
	}
	if !materialFound || validateUploadedManagedCertificateBundle(material) != nil {
		previousMaterial := material
		previousMaterialFound := materialFound
		material, err = generateInternalCAMaterial(current.Domain)
		if err != nil {
			return ManagedCertificate{}, err
		}
		if err := s.store.SaveManagedCertificateMaterial(ctx, current.Domain, material); err != nil {
			if restoreErr := s.restoreManagedCertificateMaterialAfterIssueFailure(ctx, current, previousMaterial, previousMaterialFound); restoreErr != nil {
				return ManagedCertificate{}, fmt.Errorf("persist internal ca material: %w (restore failed: %v)", err, restoreErr)
			}
			return ManagedCertificate{}, err
		}
	}

	now := s.now().UTC()
	issuedAt := now.Format(time.RFC3339)
	materialHash := hashManagedCertificateMaterial(strings.TrimSpace(material.CertPEM), strings.TrimSpace(material.KeyPEM))
	next := current
	next.Status = "active"
	next.LastIssueAt = issuedAt
	next.LastError = ""
	next.MaterialHash = materialHash
	next.Revision = maxRevision + 1
	for _, targetAgentID := range requestedTargetIDs {
		next = updateManagedCertificateAgentReport(next, targetAgentID, ManagedCertificateHeartbeatReport{
			Status:       "active",
			LastIssueAt:  issuedAt,
			LastError:    "",
			MaterialHash: materialHash,
			ACMEInfo:     current.ACMEInfo,
			UpdatedAt:    issuedAt,
		}, now)
	}

	originalRows := append([]storage.ManagedCertificateRow(nil), rows...)
	rows[targetIndex] = managedCertificateToRow(next)
	if err := s.store.SaveManagedCertificates(ctx, rows); err != nil {
		return ManagedCertificate{}, err
	}
	cleanupManagedCertificateMaterialBestEffort(ctx, s.store, originalRows, rows)
	if err := s.syncManagedCertificateAgentIDs(ctx, requestedTargetIDs, next.Revision); err != nil {
		return ManagedCertificate{}, err
	}
	return next, nil
}

func (s *certificateService) issueLocalHTTP01ACME(ctx context.Context, rows []storage.ManagedCertificateRow, targetIndex int, current ManagedCertificate, maxRevision int, requestedAgentID string) (ManagedCertificate, error) {
	if !current.Enabled {
		return ManagedCertificate{}, fmt.Errorf("%w: certificate is disabled", ErrInvalidArgument)
	}
	if current.IssuerMode != "local_http01" {
		return ManagedCertificate{}, fmt.Errorf("%w: certificate is not configured for local_http01", ErrInvalidArgument)
	}

	requestedTargetIDs := append([]string(nil), current.TargetAgentIDs...)
	if requestedAgentID != "" {
		requestedAgentID = strings.TrimSpace(requestedAgentID)
		requestedTargetIDs = requestedTargetIDs[:0]
		for _, targetAgentID := range current.TargetAgentIDs {
			if strings.TrimSpace(targetAgentID) == requestedAgentID {
				requestedTargetIDs = append(requestedTargetIDs, requestedAgentID)
			}
		}
	}
	if len(requestedTargetIDs) == 0 {
		return ManagedCertificate{}, fmt.Errorf("%w: certificate is not assigned to the requested agent", ErrInvalidArgument)
	}
	if requestedAgentID == "" && len(requestedTargetIDs) > 1 {
		return ManagedCertificate{}, fmt.Errorf("%w: local_http01 certificates must be issued from the per-agent endpoint", ErrInvalidArgument)
	}

	for _, targetAgentID := range requestedTargetIDs {
		resolvedID, displayName, capabilities, err := s.resolveCertificateTarget(ctx, targetAgentID)
		if errors.Is(err, ErrAgentNotFound) {
			return ManagedCertificate{}, fmt.Errorf("%w: target agent not found: %s", ErrInvalidArgument, strings.TrimSpace(targetAgentID))
		}
		if err != nil {
			return ManagedCertificate{}, err
		}
		if !agentHasCapability(capabilities, "cert_install") {
			return ManagedCertificate{}, fmt.Errorf("%w: target agent does not support certificate install: %s", ErrInvalidArgument, displayName)
		}
		if !agentHasCapability(capabilities, "local_acme") {
			return ManagedCertificate{}, fmt.Errorf("%w: target agent does not support local ACME issuance: %s", ErrInvalidArgument, displayName)
		}

		rules, err := s.store.ListHTTPRules(ctx, resolvedID)
		if err != nil {
			return ManagedCertificate{}, err
		}
		if !hasMatchingHTTPSRuleForCertificateInRows(rules, current) {
			return ManagedCertificate{}, fmt.Errorf("%w: no enabled HTTPS HTTP rule found for %s on agent %s", ErrInvalidArgument, current.Domain, displayName)
		}
	}

	now := s.now().UTC()
	next := current
	next.Status = "pending"
	next.LastError = ""
	next.Revision = maxRevision + 1
	for _, targetAgentID := range requestedTargetIDs {
		previousReport := current.AgentReports[strings.TrimSpace(targetAgentID)]
		next = updateManagedCertificateAgentReport(next, targetAgentID, ManagedCertificateHeartbeatReport{
			Status:       "pending",
			LastIssueAt:  previousReport.LastIssueAt,
			LastError:    "",
			MaterialHash: "",
			ACMEInfo:     ManagedCertificateACMEInfo{},
			UpdatedAt:    now.Format(time.RFC3339),
		}, now)
	}

	originalRows := append([]storage.ManagedCertificateRow(nil), rows...)
	rows[targetIndex] = managedCertificateToRow(next)
	if err := s.store.SaveManagedCertificates(ctx, rows); err != nil {
		return ManagedCertificate{}, err
	}
	cleanupManagedCertificateMaterialBestEffort(ctx, s.store, originalRows, rows)
	if err := s.syncManagedCertificateAgentIDs(ctx, requestedTargetIDs, next.Revision); err != nil {
		return ManagedCertificate{}, err
	}
	return next, nil
}

func (s *certificateService) finishManagedCertificateMutation(ctx context.Context, rows []storage.ManagedCertificateRow, targetIndex int, previous *ManagedCertificate, current ManagedCertificate, maxRevision int) (ManagedCertificate, error) {
	affectedAgentIDs := append([]string(nil), current.TargetAgentIDs...)
	removedAgentIDs := []string(nil)
	if previous != nil {
		affectedAgentIDs = unionManagedCertificateAgentIDs(previous.TargetAgentIDs, current.TargetAgentIDs)
		removedAgentIDs = differenceManagedCertificateAgentIDs(previous.TargetAgentIDs, current.TargetAgentIDs)
	}

	if current.Enabled && current.Scope == "domain" && current.IssuerMode == "master_cf_dns" {
		issued, err := s.issueManagedCertificateWithoutRevisionBump(ctx, rows, targetIndex, current, maxRevision)
		if err != nil {
			return ManagedCertificate{}, err
		}
		if err := s.syncManagedCertificateAgentIDs(ctx, removedAgentIDs, issued.Revision); err != nil {
			return ManagedCertificate{}, err
		}
		return issued, nil
	}
	if current.Enabled && current.IssuerMode == "local_http01" && current.CertificateType == "uploaded" {
		synced, err := s.syncStaticLocalCertificate(ctx, rows, targetIndex, current, maxRevision, "", false)
		if err != nil {
			return ManagedCertificate{}, err
		}
		if err := s.syncManagedCertificateAgentIDs(ctx, removedAgentIDs, synced.Revision); err != nil {
			return ManagedCertificate{}, err
		}
		return synced, nil
	}
	if err := s.syncManagedCertificateAgentIDs(ctx, affectedAgentIDs, current.Revision); err != nil {
		return ManagedCertificate{}, err
	}
	return current, nil
}

func (s *certificateService) issueManagedCertificateWithoutRevisionBump(ctx context.Context, rows []storage.ManagedCertificateRow, targetIndex int, current ManagedCertificate, maxRevision int) (ManagedCertificate, error) {
	if err := s.assertCertificateDistributionTargetsAllowed(ctx, current); err != nil {
		return ManagedCertificate{}, err
	}
	if err := s.assertManagedCertificateManualIssueAllowed(current); err != nil {
		return ManagedCertificate{}, err
	}

	issuer := s.renewalIssuer
	if issuer == nil && s.cfg.ManagedDNSCertificatesEnabled {
		issuer = newMasterCFDNSManagedCertificateIssuer()
	}
	if issuer == nil {
		return ManagedCertificate{}, fmt.Errorf("%w: managed certificates require ACME_DNS_PROVIDER=cf and CF_Token", ErrInvalidArgument)
	}

	issueResult, err := issuer.Issue(ctx, current)
	if err != nil {
		return s.failManagedCertificateIssue(ctx, rows, targetIndex, current, maxRevision, err, false)
	}
	issuedMaterial, err := resolveManagedCertificateIssueMaterial(current, issueResult)
	if err != nil {
		return s.failManagedCertificateIssue(ctx, rows, targetIndex, current, maxRevision, err, false)
	}

	previousMaterial, previousMaterialFound, err := s.store.LoadManagedCertificateMaterial(ctx, current.Domain)
	if err != nil {
		return ManagedCertificate{}, err
	}
	if err := s.store.SaveManagedCertificateMaterial(ctx, current.Domain, issuedMaterial); err != nil {
		if restoreErr := s.restoreManagedCertificateMaterialAfterIssueFailure(ctx, current, previousMaterial, previousMaterialFound); restoreErr != nil {
			return ManagedCertificate{}, fmt.Errorf("persist issued certificate material: %w (restore failed: %v)", err, restoreErr)
		}
		return s.failManagedCertificateIssue(ctx, rows, targetIndex, current, maxRevision, err, false)
	}

	next := current
	next.Status = "active"
	next.LastIssueAt = issueResult.LastIssueAt
	if strings.TrimSpace(next.LastIssueAt) == "" {
		next.LastIssueAt = s.now().UTC().Format(time.RFC3339)
	}
	next.LastError = ""
	next.MaterialHash = issueResult.MaterialHash
	if strings.TrimSpace(next.MaterialHash) == "" {
		next.MaterialHash = hashManagedCertificateMaterial(strings.TrimSpace(issuedMaterial.CertPEM), strings.TrimSpace(issuedMaterial.KeyPEM))
	}
	next.ACMEInfo = issueResult.ACMEInfo
	next.Revision = managedCertificateMutationRevision(current, maxRevision, false)

	originalRows := append([]storage.ManagedCertificateRow(nil), rows...)
	rows[targetIndex] = managedCertificateToRow(next)
	if err := s.store.SaveManagedCertificates(ctx, rows); err != nil {
		if restoreErr := s.restoreManagedCertificateMaterialAfterIssueFailure(ctx, current, previousMaterial, previousMaterialFound); restoreErr != nil {
			return ManagedCertificate{}, fmt.Errorf("save issued certificate metadata: %w (restore failed: %v)", err, restoreErr)
		}
		return ManagedCertificate{}, err
	}
	cleanupManagedCertificateMaterialBestEffort(ctx, s.store, originalRows, rows)
	if err := s.syncManagedCertificateAgentIDs(ctx, next.TargetAgentIDs, next.Revision); err != nil {
		return ManagedCertificate{}, err
	}
	return next, nil
}

func (s *certificateService) syncStaticLocalCertificate(ctx context.Context, rows []storage.ManagedCertificateRow, targetIndex int, current ManagedCertificate, maxRevision int, requestedAgentID string, bumpRevision bool) (ManagedCertificate, error) {
	if !current.Enabled {
		return ManagedCertificate{}, fmt.Errorf("%w: certificate is disabled", ErrInvalidArgument)
	}
	if current.IssuerMode != "local_http01" {
		return ManagedCertificate{}, fmt.Errorf("%w: certificate is not configured for local_http01", ErrInvalidArgument)
	}
	if current.CertificateType == "acme" {
		return ManagedCertificate{}, fmt.Errorf("%w: certificate requires local ACME issuance", ErrInvalidArgument)
	}

	requestedTargetIDs := append([]string(nil), current.TargetAgentIDs...)
	if requestedAgentID != "" {
		requestedTargetIDs = requestedTargetIDs[:0]
		for _, targetAgentID := range current.TargetAgentIDs {
			if strings.TrimSpace(targetAgentID) == requestedAgentID {
				requestedTargetIDs = append(requestedTargetIDs, requestedAgentID)
			}
		}
	}
	if len(requestedTargetIDs) == 0 {
		return ManagedCertificate{}, fmt.Errorf("%w: certificate is not assigned to the requested agent", ErrInvalidArgument)
	}
	for _, targetAgentID := range requestedTargetIDs {
		_, displayName, capabilities, err := s.resolveCertificateTarget(ctx, targetAgentID)
		if errors.Is(err, ErrAgentNotFound) {
			return ManagedCertificate{}, fmt.Errorf("%w: target agent not found: %s", ErrInvalidArgument, strings.TrimSpace(targetAgentID))
		}
		if err != nil {
			return ManagedCertificate{}, err
		}
		if !agentHasCapability(capabilities, "cert_install") {
			return ManagedCertificate{}, fmt.Errorf("%w: target agent does not support certificate install: %s", ErrInvalidArgument, displayName)
		}
	}

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

	now := s.now().UTC()
	issuedAt := now.Format(time.RFC3339)
	materialHash := hashManagedCertificateMaterial(material.CertPEM, material.KeyPEM)
	next := current
	next.Status = "active"
	next.LastIssueAt = issuedAt
	next.LastError = ""
	next.MaterialHash = materialHash
	next.Revision = managedCertificateMutationRevision(current, maxRevision, bumpRevision)
	for _, targetAgentID := range requestedTargetIDs {
		next = updateManagedCertificateAgentReport(next, targetAgentID, ManagedCertificateHeartbeatReport{
			Status:       "active",
			LastIssueAt:  issuedAt,
			LastError:    "",
			MaterialHash: materialHash,
			ACMEInfo:     current.ACMEInfo,
			UpdatedAt:    issuedAt,
		}, now)
	}

	originalRows := append([]storage.ManagedCertificateRow(nil), rows...)
	rows[targetIndex] = managedCertificateToRow(next)
	if err := s.store.SaveManagedCertificates(ctx, rows); err != nil {
		return ManagedCertificate{}, err
	}
	cleanupManagedCertificateMaterialBestEffort(ctx, s.store, originalRows, rows)
	if err := s.syncManagedCertificateAgentIDs(ctx, requestedTargetIDs, next.Revision); err != nil {
		return ManagedCertificate{}, err
	}
	return next, nil
}

func (s *certificateService) syncManagedCertificateAgentIDs(ctx context.Context, agentIDs []string, revision int) error {
	seen := make(map[string]struct{}, len(agentIDs))
	for _, agentID := range agentIDs {
		resolvedID := strings.TrimSpace(agentID)
		if resolvedID == "" {
			continue
		}
		if _, ok := seen[resolvedID]; ok {
			continue
		}
		seen[resolvedID] = struct{}{}

		resolvedID, _, capabilities, err := s.resolveCertificateTarget(ctx, resolvedID)
		if errors.Is(err, ErrAgentNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if !agentHasCapability(capabilities, "cert_install") {
			continue
		}
		if s.cfg.EnableLocalAgent && resolvedID == s.cfg.LocalAgentID {
			if s.localApplyTrigger != nil {
				if err := s.localApplyTrigger(ctx); err != nil {
					return err
				}
				continue
			}
			if err := s.applyLocalManagedCertificateSync(ctx); err != nil {
				return err
			}
			continue
		}
		if err := s.bumpRemoteDesiredRevision(ctx, resolvedID, revision); err != nil {
			if errors.Is(err, ErrAgentNotFound) {
				continue
			}
			return err
		}
	}
	return nil
}

func (s *certificateService) applyLocalManagedCertificateSync(ctx context.Context) error {
	localStore, ok := s.store.(localManagedCertificateSyncStore)
	if !ok {
		return fmt.Errorf("local managed certificate sync requires local snapshot support")
	}
	snapshot, err := localStore.LoadLocalSnapshot(ctx, s.cfg.LocalAgentID)
	if err != nil {
		return err
	}
	return localStore.SaveLocalRuntimeState(ctx, s.cfg.LocalAgentID, storage.RuntimeState{
		CurrentRevision:   snapshot.Revision,
		Status:            "success",
		LastApplyRevision: snapshot.Revision,
		LastApplyStatus:   "success",
	})
}

func (s *certificateService) bumpRemoteDesiredRevision(ctx context.Context, agentID string, revision int) error {
	if s.cfg.EnableLocalAgent && agentID == s.cfg.LocalAgentID {
		return nil
	}

	rows, err := s.store.ListAgents(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.ID != agentID {
			continue
		}
		snapshot, err := s.store.LoadAgentSnapshot(ctx, row.ID, storage.AgentSnapshotInput{
			DesiredVersion:  row.DesiredVersion,
			DesiredRevision: row.DesiredRevision,
			CurrentRevision: row.CurrentRevision,
			Platform:        row.Platform,
		})
		if err != nil {
			return err
		}
		nextRevision := revision
		if int(snapshot.Revision) > nextRevision {
			nextRevision = int(snapshot.Revision)
		}
		if row.DesiredRevision < nextRevision {
			row.DesiredRevision = nextRevision
		}
		return s.store.SaveAgent(ctx, row)
	}
	return ErrAgentNotFound
}

func managedCertificateMutationRevision(current ManagedCertificate, maxRevision int, bumpRevision bool) int {
	if !bumpRevision {
		return current.Revision
	}
	return maxRevision + 1
}

func unionManagedCertificateAgentIDs(previous []string, next []string) []string {
	seen := make(map[string]struct{}, len(previous)+len(next))
	combined := make([]string, 0, len(previous)+len(next))
	for _, values := range [][]string{previous, next} {
		for _, value := range values {
			trimmed := strings.TrimSpace(value)
			if trimmed == "" {
				continue
			}
			if _, ok := seen[trimmed]; ok {
				continue
			}
			seen[trimmed] = struct{}{}
			combined = append(combined, trimmed)
		}
	}
	return combined
}

func differenceManagedCertificateAgentIDs(previous []string, next []string) []string {
	nextSet := make(map[string]struct{}, len(next))
	for _, value := range next {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		nextSet[trimmed] = struct{}{}
	}

	removed := make([]string, 0, len(previous))
	seen := make(map[string]struct{}, len(previous))
	for _, value := range previous {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := nextSet[trimmed]; ok {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		removed = append(removed, trimmed)
	}
	return removed
}

func (s *certificateService) assertManagedCertificateManualIssueAllowed(cert ManagedCertificate) error {
	if cert.IssuerMode != "master_cf_dns" {
		return fmt.Errorf("%w: certificate is not configured for master_cf_dns", ErrInvalidArgument)
	}
	if !cert.Enabled {
		return fmt.Errorf("%w: certificate is disabled", ErrInvalidArgument)
	}
	if cert.Scope != "domain" {
		return fmt.Errorf("%w: only domain certificates can be managed by master", ErrInvalidArgument)
	}
	if err := assertManagedCertificateTargetingAllowed(s.cfg, cert); err != nil {
		return err
	}
	return nil
}

func resolveManagedCertificateIssueMaterial(cert ManagedCertificate, result managedCertificateRenewalResult) (storage.ManagedCertificateBundle, error) {
	bundle := result.Material
	bundle.Domain = cert.Domain
	bundle.CertPEM = strings.TrimSpace(bundle.CertPEM)
	bundle.KeyPEM = strings.TrimSpace(bundle.KeyPEM)
	if bundle.CertPEM == "" || bundle.KeyPEM == "" {
		return storage.ManagedCertificateBundle{}, fmt.Errorf("%w: issuer did not return certificate material", ErrInvalidArgument)
	}
	if err := validateUploadedManagedCertificateBundle(bundle); err != nil {
		return storage.ManagedCertificateBundle{}, err
	}
	return bundle, nil
}

func (s *certificateService) failManagedCertificateIssue(ctx context.Context, rows []storage.ManagedCertificateRow, targetIndex int, current ManagedCertificate, maxRevision int, issueErr error, bumpRevision bool) (ManagedCertificate, error) {
	failed := current
	failed.Status = "error"
	failed.LastError = issueErr.Error()
	failed.Revision = managedCertificateMutationRevision(current, maxRevision, bumpRevision)

	nextRows := append([]storage.ManagedCertificateRow(nil), rows...)
	originalRows := append([]storage.ManagedCertificateRow(nil), rows...)
	nextRows[targetIndex] = managedCertificateToRow(failed)
	if err := s.store.SaveManagedCertificates(ctx, nextRows); err != nil {
		return ManagedCertificate{}, err
	}
	cleanupManagedCertificateMaterialBestEffort(ctx, s.store, originalRows, nextRows)
	return ManagedCertificate{}, issueErr
}

func (s *certificateService) restoreManagedCertificateMaterialAfterIssueFailure(ctx context.Context, cert ManagedCertificate, previous storage.ManagedCertificateBundle, previousFound bool) error {
	if previousFound {
		return s.store.SaveManagedCertificateMaterial(ctx, cert.Domain, previous)
	}
	return s.store.CleanupManagedCertificateMaterial(
		ctx,
		[]storage.ManagedCertificateRow{managedCertificateToRow(cert)},
		[]storage.ManagedCertificateRow{},
	)
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
		_, displayName, capabilities, err := s.resolveCertificateTarget(ctx, targetAgentID)
		if errors.Is(err, ErrAgentNotFound) {
			return fmt.Errorf("%w: target agent not found: %s", ErrInvalidArgument, strings.TrimSpace(targetAgentID))
		}
		if err != nil {
			return err
		}
		if !agentHasCapability(capabilities, "cert_install") {
			return fmt.Errorf("%w: target agent does not support certificate install: %s", ErrInvalidArgument, displayName)
		}
	}
	return nil
}

func (s *certificateService) resolveCertificateTarget(ctx context.Context, agentID string) (string, string, []string, error) {
	resolvedID := strings.TrimSpace(agentID)
	if resolvedID == "" {
		return "", "", nil, ErrAgentNotFound
	}
	if s.cfg.EnableLocalAgent && resolvedID == s.cfg.LocalAgentID {
		return resolvedID, resolvedID, append([]string(nil), defaultLocalCapabilities...), nil
	}

	rows, err := s.store.ListAgents(ctx)
	if err != nil {
		return "", "", nil, err
	}
	for _, row := range rows {
		if row.ID == resolvedID {
			displayName := resolvedID
			if strings.TrimSpace(row.Name) != "" {
				displayName = strings.TrimSpace(row.Name)
			}
			return resolvedID, displayName, parseStringArray(row.CapabilitiesJSON), nil
		}
	}
	return "", "", nil, ErrAgentNotFound
}

func (s *certificateService) resolveUploadedMaterialForMutation(ctx context.Context, input ManagedCertificateInput, next ManagedCertificate, previous *ManagedCertificate) (storage.ManagedCertificateBundle, bool, error) {
	if next.CertificateType != "uploaded" {
		return storage.ManagedCertificateBundle{}, false, nil
	}

	hasCertificate := input.CertificatePEM != nil
	hasKey := input.PrivateKeyPEM != nil
	hasCA := input.CAPEM != nil
	certificatePEM := normalizeUploadedPEMField(input.CertificatePEM)
	privateKeyPEM := normalizeUploadedPEMField(input.PrivateKeyPEM)
	caPEM := normalizeUploadedPEMField(input.CAPEM)
	certificateFromPreviousRaw := false
	caFromPreviousRaw := false

	if previous == nil {
		joinedCertificatePEM := joinUploadedCertificatePEM(certificatePEM, caPEM)
		bundle := storage.ManagedCertificateBundle{
			Domain:  next.Domain,
			CertPEM: joinedCertificatePEM,
			KeyPEM:  privateKeyPEM,
		}
		if err := validateUploadedManagedCertificateBundle(bundle); err != nil {
			return storage.ManagedCertificateBundle{}, false, err
		}
		return bundle, true, nil
	}

	if !hasCertificate || !hasKey || !hasCA {
		previousMaterial, ok, err := s.store.LoadManagedCertificateMaterial(ctx, previous.Domain)
		if err != nil {
			return storage.ManagedCertificateBundle{}, false, err
		}
		if !ok {
			return storage.ManagedCertificateBundle{}, false, fmt.Errorf("%w: certificate_pem is required for uploaded certificates", ErrInvalidArgument)
		}
		previousLeafPEM, previousCAPEM, splitErr := splitUploadedCertificatePEM(previousMaterial.CertPEM)
		if splitErr != nil {
			return storage.ManagedCertificateBundle{}, false, splitErr
		}
		if !hasCertificate {
			certificatePEM = previousLeafPEM
			certificateFromPreviousRaw = true
		}
		if !hasKey {
			privateKeyPEM = previousMaterial.KeyPEM
		}
		if !hasCA {
			caPEM = previousCAPEM
			caFromPreviousRaw = true
		}
	}

	joinedCertificatePEM := ""
	switch {
	case certificateFromPreviousRaw && caFromPreviousRaw:
		joinedCertificatePEM = certificatePEM + caPEM
	case certificateFromPreviousRaw:
		if strings.TrimSpace(caPEM) == "" {
			joinedCertificatePEM = certificatePEM
		} else {
			joinedCertificatePEM = certificatePEM + "\n" + strings.TrimSpace(caPEM)
		}
	case caFromPreviousRaw:
		if strings.TrimSpace(certificatePEM) == "" {
			joinedCertificatePEM = caPEM
		} else {
			joinedCertificatePEM = strings.TrimSpace(certificatePEM) + caPEM
		}
	default:
		joinedCertificatePEM = joinUploadedCertificatePEM(certificatePEM, caPEM)
	}
	bundle := storage.ManagedCertificateBundle{
		Domain:  next.Domain,
		CertPEM: joinedCertificatePEM,
		KeyPEM:  privateKeyPEM,
	}
	if err := validateUploadedManagedCertificateBundle(bundle); err != nil {
		return storage.ManagedCertificateBundle{}, false, err
	}
	return bundle, true, nil
}

func normalizeManagedCertificateInput(input ManagedCertificateInput, fallback ManagedCertificate, suggestedID int, defaultAgentID string, allowEmptyTargets bool) (ManagedCertificate, error) {
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
	if len(targetAgentIDs) == 0 && !allowEmptyTargets {
		targetAgentIDs = []string{defaultAgentID}
	}
	if allowEmptyTargets && len(targetAgentIDs) == 0 {
		targetAgentIDs = []string{}
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
	cert.Status = coalesceString(report.Status, cert.Status)
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

func reconcileLocalHTTP01CertificatesForAgent(rows []storage.ManagedCertificateRow, agentID string, capabilities []string, rules []storage.HTTPRuleRow, applyRevision int, applyStatus string, applyMessage string, reportedCertIDs map[int]struct{}, now time.Time) ([]storage.ManagedCertificateRow, bool) {
	if strings.TrimSpace(agentID) == "" || applyRevision <= 0 {
		return rows, false
	}
	if !agentHasCapability(capabilities, "cert_install") || !agentHasCapability(capabilities, "local_acme") {
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
	return cert.Usage == "relay_ca" || usesReservedRelayCAIdentity(cert)
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
	if !containsString(cert.Tags, "auto") {
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
