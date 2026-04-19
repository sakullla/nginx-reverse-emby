package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

var ErrRelayListenerNotFound = errors.New("relay listener not found")

var relayListenerAutoCertificateNonce = func() string {
	var buf [6]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "000000000000"
	}
	return hex.EncodeToString(buf[:])
}

type RelayPin struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

type RelayListener struct {
	ID                      int        `json:"id"`
	AgentID                 string     `json:"agent_id"`
	Name                    string     `json:"name"`
	BindHosts               []string   `json:"bind_hosts"`
	ListenHost              string     `json:"listen_host"`
	ListenPort              int        `json:"listen_port"`
	PublicHost              string     `json:"public_host"`
	PublicPort              int        `json:"public_port"`
	Enabled                 bool       `json:"enabled"`
	CertificateID           *int       `json:"certificate_id"`
	TLSMode                 string     `json:"tls_mode"`
	TransportMode           string     `json:"transport_mode"`
	AllowTransportFallback  bool       `json:"allow_transport_fallback"`
	ObfsMode                string     `json:"obfs_mode"`
	PinSet                  []RelayPin `json:"pin_set"`
	TrustedCACertificateIDs []int      `json:"trusted_ca_certificate_ids"`
	AllowSelfSigned         bool       `json:"allow_self_signed"`
	Tags                    []string   `json:"tags"`
	Revision                int        `json:"revision"`
}

type RelayListenerInput struct {
	ID                         *int        `json:"id,omitempty"`
	Name                       *string     `json:"name,omitempty"`
	BindHosts                  *[]string   `json:"bind_hosts,omitempty"`
	ListenHost                 *string     `json:"listen_host,omitempty"`
	ListenPort                 *int        `json:"listen_port,omitempty"`
	PublicHost                 *string     `json:"public_host,omitempty"`
	PublicPort                 *int        `json:"public_port,omitempty"`
	Enabled                    *bool       `json:"enabled,omitempty"`
	CertificateID              *int        `json:"certificate_id,omitempty"`
	TLSMode                    *string     `json:"tls_mode,omitempty"`
	TransportMode              *string     `json:"transport_mode,omitempty"`
	AllowTransportFallback     *bool       `json:"allow_transport_fallback,omitempty"`
	ObfsMode                   *string     `json:"obfs_mode,omitempty"`
	PinSet                     *[]RelayPin `json:"pin_set,omitempty"`
	TrustedCACertificateIDs    *[]int      `json:"trusted_ca_certificate_ids,omitempty"`
	AllowSelfSigned            *bool       `json:"allow_self_signed,omitempty"`
	Tags                       *[]string   `json:"tags,omitempty"`
	CertificateSource          *string     `json:"certificate_source,omitempty"`
	TrustModeSource            *string     `json:"trust_mode_source,omitempty"`
	HasCertificateID           bool        `json:"-"`
	HasTLSMode                 bool        `json:"-"`
	HasPinSet                  bool        `json:"-"`
	HasTrustedCACertificateIDs bool        `json:"-"`
	HasAllowSelfSigned         bool        `json:"-"`
}

type relayNormalizeOptions struct {
	AllowMissingCertificate bool
	SkipTrustValidation     bool
}

type relayPreparation struct {
	Listener            RelayListener
	OriginalCertRows    []storage.ManagedCertificateRow
	NextCertRows        []storage.ManagedCertificateRow
	MaterialBundles     []storage.ManagedCertificateBundle
	PersistCertificates bool
}

type relayService struct {
	cfg               config.Config
	store             storage.Store
	localApplyTrigger func(context.Context) error
}

func NewRelayListenerService(cfg config.Config, store storage.Store) *relayService {
	return &relayService{cfg: cfg, store: store}
}

func (s *relayService) SetLocalApplyTrigger(trigger func(context.Context) error) {
	s.localApplyTrigger = wrapLocalApplyTrigger(trigger)
}

func (s *relayService) triggerLocalApply(ctx context.Context, agentID string) error {
	if !s.cfg.EnableLocalAgent || agentID != s.cfg.LocalAgentID || s.localApplyTrigger == nil {
		return nil
	}
	return s.localApplyTrigger(ctx)
}

func (s *relayService) Bootstrap(ctx context.Context) error {
	rows, err := s.store.ListManagedCertificates(ctx)
	if err != nil {
		return err
	}

	_, nextRows, bundles, err := s.ensureGlobalRelayCA(ctx, rows)
	if err != nil {
		return err
	}

	rowsChanged := !managedCertificateRowsEqual(rows, nextRows)
	if rowsChanged {
		if err := s.store.SaveManagedCertificates(ctx, nextRows); err != nil {
			return err
		}
	}
	if len(bundles) > 0 {
		if err := s.persistManagedCertificateMaterialBundles(ctx, bundles, rows, nextRows); err != nil {
			if rowsChanged {
				if rollbackErr := s.store.SaveManagedCertificates(ctx, rows); rollbackErr != nil {
					return fmt.Errorf("%v (rollback failed: %v)", err, rollbackErr)
				}
			}
			return err
		}
	}
	if rowsChanged || len(bundles) > 0 {
		cleanupManagedCertificateMaterialBestEffort(ctx, s.store, rows, nextRows)
	}
	return nil
}

func (s *relayService) List(ctx context.Context, agentID string) ([]RelayListener, error) {
	resolvedID, err := s.ensureAgentExists(ctx, agentID)
	if err != nil {
		return nil, err
	}

	rows, err := s.store.ListRelayListeners(ctx, resolvedID)
	if err != nil {
		return nil, err
	}

	listeners := make([]RelayListener, 0, len(rows))
	for _, row := range rows {
		listeners = append(listeners, relayListenerFromRow(row))
	}
	return listeners, nil
}

func (s *relayService) Create(ctx context.Context, agentID string, input RelayListenerInput) (RelayListener, error) {
	resolvedID, err := s.ensureAgentExists(ctx, agentID)
	if err != nil {
		return RelayListener{}, err
	}

	allRows, err := s.store.ListRelayListeners(ctx, "")
	if err != nil {
		return RelayListener{}, err
	}
	rows, err := s.store.ListRelayListeners(ctx, resolvedID)
	if err != nil {
		return RelayListener{}, err
	}
	allocator, err := newConfigIdentityAllocatorFromStore(ctx, s.cfg, s.store)
	if err != nil {
		return RelayListener{}, err
	}

	maxRevision := 0
	for _, row := range allRows {
		if row.Revision > maxRevision {
			maxRevision = row.Revision
		}
	}

	allocatedID := allocator.AllocateListenerID(preferredInt(input.ID))
	normalizedInput := input
	// Keep the caller's preferred ID only for allocator conflict resolution.
	// Normalization should see the assigned ID, not re-read the raw preference.
	normalizedInput.ID = nil
	prepared, err := s.prepareRelayListener(ctx, resolvedID, normalizedInput, RelayListener{}, allocatedID)
	if err != nil {
		return RelayListener{}, err
	}
	listener := prepared.Listener
	listener.AgentID = resolvedID
	listener.Revision = allocator.AllocateRevisionForAgent(resolvedID, maxRevision)

	if prepared.PersistCertificates {
		if err := s.store.SaveManagedCertificates(ctx, prepared.NextCertRows); err != nil {
			return RelayListener{}, err
		}
		if err := s.persistManagedCertificateMaterialBundles(ctx, prepared.MaterialBundles, prepared.OriginalCertRows, prepared.NextCertRows); err != nil {
			if rollbackErr := s.store.SaveManagedCertificates(ctx, prepared.OriginalCertRows); rollbackErr != nil {
				return RelayListener{}, fmt.Errorf("%v (rollback failed: %v)", err, rollbackErr)
			}
			return RelayListener{}, err
		}
	}
	rows = append(rows, relayListenerToRow(listener))
	if err := s.store.SaveRelayListeners(ctx, resolvedID, rows); err != nil {
		if prepared.PersistCertificates {
			if rollbackErr := s.store.SaveManagedCertificates(ctx, prepared.OriginalCertRows); rollbackErr != nil {
				return RelayListener{}, fmt.Errorf("%v (rollback failed: %v)", err, rollbackErr)
			}
			cleanupManagedCertificateMaterialBestEffort(ctx, s.store, prepared.NextCertRows, prepared.OriginalCertRows)
		}
		return RelayListener{}, err
	}
	if err := s.bumpRemoteDesiredRevision(ctx, resolvedID, listener.Revision); err != nil {
		return RelayListener{}, err
	}
	if prepared.PersistCertificates {
		cleanupManagedCertificateMaterialBestEffort(ctx, s.store, prepared.OriginalCertRows, prepared.NextCertRows)
	}
	if err := s.triggerLocalApply(ctx, resolvedID); err != nil {
		return RelayListener{}, err
	}
	return listener, nil
}

func (s *relayService) Update(ctx context.Context, agentID string, id int, input RelayListenerInput) (RelayListener, error) {
	resolvedID, err := s.ensureAgentExists(ctx, agentID)
	if err != nil {
		return RelayListener{}, err
	}

	rows, err := s.store.ListRelayListeners(ctx, resolvedID)
	if err != nil {
		return RelayListener{}, err
	}
	allocator, err := newConfigIdentityAllocatorFromStore(ctx, s.cfg, s.store)
	if err != nil {
		return RelayListener{}, err
	}

	maxRevision := 0
	targetIndex := -1
	var current RelayListener
	for i, row := range rows {
		if row.Revision > maxRevision {
			maxRevision = row.Revision
		}
		if row.ID == id {
			targetIndex = i
			current = relayListenerFromRow(row)
		}
	}
	if targetIndex < 0 {
		return RelayListener{}, ErrRelayListenerNotFound
	}

	prepared, err := s.prepareRelayListener(ctx, resolvedID, input, current, id)
	if err != nil {
		return RelayListener{}, err
	}
	listener := prepared.Listener
	if current.Enabled && !listener.Enabled {
		reference, err := s.findRelayListenerReference(ctx, listener.ID)
		if err != nil {
			return RelayListener{}, err
		}
		if reference != nil {
			return RelayListener{}, fmt.Errorf(
				"%w: relay listener %d is referenced by %s rule #%d on agent %s; disable is not allowed",
				ErrInvalidArgument,
				listener.ID,
				reference.RuleType,
				reference.RuleID,
				reference.AgentID,
			)
		}
	}
	listener.AgentID = resolvedID
	listener.Revision = allocator.AllocateRevisionForAgent(resolvedID, maxRevision)

	if prepared.PersistCertificates {
		if err := s.store.SaveManagedCertificates(ctx, prepared.NextCertRows); err != nil {
			return RelayListener{}, err
		}
		if err := s.persistManagedCertificateMaterialBundles(ctx, prepared.MaterialBundles, prepared.OriginalCertRows, prepared.NextCertRows); err != nil {
			if rollbackErr := s.store.SaveManagedCertificates(ctx, prepared.OriginalCertRows); rollbackErr != nil {
				return RelayListener{}, fmt.Errorf("%v (rollback failed: %v)", err, rollbackErr)
			}
			return RelayListener{}, err
		}
	}
	rows[targetIndex] = relayListenerToRow(listener)
	if err := s.store.SaveRelayListeners(ctx, resolvedID, rows); err != nil {
		if prepared.PersistCertificates {
			if rollbackErr := s.store.SaveManagedCertificates(ctx, prepared.OriginalCertRows); rollbackErr != nil {
				return RelayListener{}, fmt.Errorf("%v (rollback failed: %v)", err, rollbackErr)
			}
			cleanupManagedCertificateMaterialBestEffort(ctx, s.store, prepared.NextCertRows, prepared.OriginalCertRows)
		}
		return RelayListener{}, err
	}
	if err := s.bumpRemoteDesiredRevision(ctx, resolvedID, listener.Revision); err != nil {
		return RelayListener{}, err
	}
	if prepared.PersistCertificates {
		cleanupManagedCertificateMaterialBestEffort(ctx, s.store, prepared.OriginalCertRows, prepared.NextCertRows)
	}
	if current.CertificateID != nil && relayListenerCertificateChanged(current.CertificateID, listener.CertificateID) {
		if err := s.cleanupUnusedAutoRelayListenerCertificate(ctx, *current.CertificateID); err != nil {
			return RelayListener{}, err
		}
	}
	if err := s.triggerLocalApply(ctx, resolvedID); err != nil {
		return RelayListener{}, err
	}
	return listener, nil
}

func (s *relayService) Delete(ctx context.Context, agentID string, id int) (RelayListener, error) {
	resolvedID, err := s.ensureAgentExists(ctx, agentID)
	if err != nil {
		return RelayListener{}, err
	}

	rows, err := s.store.ListRelayListeners(ctx, resolvedID)
	if err != nil {
		return RelayListener{}, err
	}

	targetIndex := -1
	var deleted RelayListener
	for i, row := range rows {
		if row.ID == id {
			targetIndex = i
			deleted = relayListenerFromRow(row)
			break
		}
	}
	if targetIndex < 0 {
		return RelayListener{}, ErrRelayListenerNotFound
	}
	reference, err := s.findRelayListenerReference(ctx, deleted.ID)
	if err != nil {
		return RelayListener{}, err
	}
	if reference != nil {
		return RelayListener{}, fmt.Errorf(
			"%w: relay listener %d is referenced by %s rule #%d on agent %s",
			ErrInvalidArgument,
			deleted.ID,
			reference.RuleType,
			reference.RuleID,
			reference.AgentID,
		)
	}

	next := append([]storage.RelayListenerRow(nil), rows[:targetIndex]...)
	next = append(next, rows[targetIndex+1:]...)
	if err := s.store.SaveRelayListeners(ctx, resolvedID, next); err != nil {
		return RelayListener{}, err
	}
	allocator, err := newConfigIdentityAllocatorFromStore(ctx, s.cfg, s.store)
	if err != nil {
		return RelayListener{}, err
	}
	nextRevision := allocator.AllocateRevisionForAgent(resolvedID, deleted.Revision)
	if err := s.bumpRemoteDesiredRevision(ctx, resolvedID, nextRevision); err != nil {
		return RelayListener{}, err
	}
	if deleted.CertificateID != nil {
		if err := s.cleanupUnusedAutoRelayListenerCertificate(ctx, *deleted.CertificateID); err != nil {
			return RelayListener{}, err
		}
	}
	if err := s.triggerLocalApply(ctx, resolvedID); err != nil {
		return RelayListener{}, err
	}
	return deleted, nil
}

func (s *relayService) ensureAgentExists(ctx context.Context, agentID string) (string, error) {
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

func (s *relayService) bumpRemoteDesiredRevision(ctx context.Context, agentID string, revision int) error {
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

func (s *relayService) prepareRelayListener(ctx context.Context, agentID string, input RelayListenerInput, fallback RelayListener, suggestedID int) (relayPreparation, error) {
	certificateSource, err := normalizeRelayCertificateSource(input.CertificateSource)
	if err != nil {
		return relayPreparation{}, err
	}
	trustModeSource, err := normalizeRelayTrustModeSource(input.TrustModeSource)
	if err != nil {
		return relayPreparation{}, err
	}

	certRows, err := s.store.ListManagedCertificates(ctx)
	if err != nil {
		return relayPreparation{}, err
	}
	originalCertRows := append([]storage.ManagedCertificateRow(nil), certRows...)

	draft, err := normalizeRelayListenerInput(input, fallback, suggestedID, relayNormalizeOptions{
		AllowMissingCertificate: true,
		SkipTrustValidation:     true,
	})
	if err != nil {
		return relayPreparation{}, err
	}
	previousUsesAutoCert := relayListenerUsesAutoCertificate(certRows, fallback)
	shouldIssueCert := shouldAutoIssueRelayListenerCertificate(certificateSource, draft, previousUsesAutoCert)
	shouldDeriveTrust := shouldAutoDeriveRelayTrust(trustModeSource, certificateSource, input, draft, fallback, previousUsesAutoCert)

	workingInput := input
	persistCertificates := false
	materialBundles := make([]storage.ManagedCertificateBundle, 0)
	if shouldIssueCert {
		if previousUsesAutoCert && fallback.CertificateID != nil {
			workingInput.CertificateID = fallback.CertificateID
		}
		if workingInput.CertificateID == nil || *workingInput.CertificateID <= 0 {
			certID, nextRows, nextBundles, err := s.ensureAutoRelayListenerCertificate(ctx, certRows, agentID, draft)
			if err != nil {
				return relayPreparation{}, err
			}
			certRows = nextRows
			persistCertificates = true
			workingInput.CertificateID = &certID
			materialBundles = append(materialBundles, nextBundles...)
		}
	}
	if shouldDeriveTrust {
		selectedCertID := 0
		switch {
		case workingInput.CertificateID != nil && *workingInput.CertificateID > 0:
			selectedCertID = *workingInput.CertificateID
		case draft.CertificateID != nil:
			selectedCertID = *draft.CertificateID
		}
		if selectedCertID <= 0 {
			return relayPreparation{}, fmt.Errorf("%w: certificate_id is required when relay listener trust_mode_source is auto", ErrInvalidArgument)
		}
		selectedCert, _, ok := findManagedCertificateByID(certRows, selectedCertID)
		if !ok {
			return relayPreparation{}, fmt.Errorf("%w: certificate %d not found for relay listener", ErrInvalidArgument, selectedCertID)
		}
		if !containsString(selectedCert.TargetAgentIDs, agentID) {
			return relayPreparation{}, fmt.Errorf("%w: certificate %d is not assigned to agent %s", ErrInvalidArgument, selectedCertID, agentID)
		}
		tlsMode, pinSet, trustedCAIDs, allowSelfSigned, err := deriveRelayTrustMaterial(ctx, s.store, selectedCert, certRows, materialBundles)
		if err != nil {
			return relayPreparation{}, err
		}
		workingInput.TLSMode = &tlsMode
		workingInput.PinSet = &pinSet
		workingInput.TrustedCACertificateIDs = &trustedCAIDs
		workingInput.AllowSelfSigned = &allowSelfSigned
	}

	listener, err := normalizeRelayListenerInput(workingInput, fallback, suggestedID, relayNormalizeOptions{})
	if err != nil {
		return relayPreparation{}, err
	}
	return relayPreparation{
		Listener:            listener,
		OriginalCertRows:    originalCertRows,
		NextCertRows:        certRows,
		MaterialBundles:     materialBundles,
		PersistCertificates: persistCertificates,
	}, nil
}

func relayListenerUsesAutoCertificate(rows []storage.ManagedCertificateRow, listener RelayListener) bool {
	if listener.ID <= 0 || listener.CertificateID == nil {
		return false
	}
	cert, _, ok := findManagedCertificateByID(rows, *listener.CertificateID)
	if !ok {
		return false
	}
	return isAutoRelayListenerCertificate(cert, listener.ID)
}

func shouldAutoIssueRelayListenerCertificate(certificateSource string, draft RelayListener, previousUsesAutoCert bool) bool {
	if !draft.Enabled {
		return false
	}
	if certificateSource != "" {
		return certificateSource == "auto_relay_ca" && draft.CertificateID == nil
	}
	return previousUsesAutoCert && draft.CertificateID == nil
}

func shouldAutoDeriveRelayTrust(
	trustModeSource string,
	certificateSource string,
	input RelayListenerInput,
	draft RelayListener,
	fallback RelayListener,
	previousUsesAutoCert bool,
) bool {
	if !draft.Enabled {
		return false
	}
	switch trustModeSource {
	case "custom":
		return false
	case "auto":
		return true
	}
	if input.hasExplicitRelayTrustFields() {
		return false
	}
	if certificateSource == "auto_relay_ca" {
		return true
	}
	if fallback.ID <= 0 || !previousUsesAutoCert {
		return false
	}
	return !input.hasCertificateIDField()
}

func normalizeRelayListenerInput(input RelayListenerInput, fallback RelayListener, suggestedID int, options relayNormalizeOptions) (RelayListener, error) {
	id := fallback.ID
	if input.ID != nil && *input.ID > 0 {
		id = *input.ID
	}
	if id <= 0 {
		id = suggestedID
	}

	name := strings.TrimSpace(pointerString(input.Name))
	if name == "" {
		name = strings.TrimSpace(fallback.Name)
	}
	if name == "" {
		return RelayListener{}, fmt.Errorf("%w: name is required", ErrInvalidArgument)
	}

	listenPort := fallback.ListenPort
	if input.ListenPort != nil {
		listenPort = *input.ListenPort
	}
	if listenPort < 1 || listenPort > 65535 {
		return RelayListener{}, fmt.Errorf("%w: listen_port must be an integer between 1 and 65535", ErrInvalidArgument)
	}

	bindHosts := append([]string(nil), fallback.BindHosts...)
	if input.BindHosts != nil {
		bindHosts = normalizeRelayBindHosts(*input.BindHosts)
	}
	listenHost := strings.TrimSpace(pointerString(input.ListenHost))
	if listenHost == "" {
		listenHost = strings.TrimSpace(fallback.ListenHost)
	}
	if len(bindHosts) == 0 {
		if listenHost == "" {
			listenHost = "0.0.0.0"
		}
		bindHosts = []string{listenHost}
	}
	listenHost = bindHosts[0]

	publicHost := strings.TrimSpace(pointerString(input.PublicHost))
	if publicHost == "" {
		publicHost = strings.TrimSpace(fallback.PublicHost)
	}
	if publicHost == "" {
		publicHost = listenHost
	}

	publicPort := fallback.PublicPort
	if input.PublicPort != nil {
		publicPort = *input.PublicPort
	}
	if publicPort <= 0 {
		publicPort = listenPort
	}
	if publicPort < 1 || publicPort > 65535 {
		return RelayListener{}, fmt.Errorf("%w: public_port must be an integer between 1 and 65535", ErrInvalidArgument)
	}

	enabled := true
	if fallback.ID > 0 {
		enabled = fallback.Enabled
	}
	if input.Enabled != nil {
		enabled = *input.Enabled
	}

	var certID *int
	if fallback.CertificateID != nil {
		value := *fallback.CertificateID
		certID = &value
	}
	if input.hasCertificateIDField() {
		if input.CertificateID != nil && *input.CertificateID > 0 {
			value := *input.CertificateID
			certID = &value
		} else {
			certID = nil
		}
	}

	tlsMode := strings.TrimSpace(pointerString(input.TLSMode))
	if tlsMode == "" {
		tlsMode = fallback.TLSMode
	}
	if tlsMode == "" {
		tlsMode = "pin_or_ca"
	}
	switch tlsMode {
	case "pin_only", "ca_only", "pin_or_ca", "pin_and_ca":
	default:
		return RelayListener{}, fmt.Errorf("%w: tls_mode must be pin_only, ca_only, pin_or_ca, or pin_and_ca", ErrInvalidArgument)
	}

	transportMode := strings.TrimSpace(pointerString(input.TransportMode))
	if transportMode == "" {
		transportMode = fallback.TransportMode
	}
	switch transportMode {
	case "", "tls_tcp":
		transportMode = "tls_tcp"
	case "quic":
	default:
		return RelayListener{}, fmt.Errorf("%w: transport_mode must be tls_tcp or quic", ErrInvalidArgument)
	}

	allowTransportFallback := fallback.AllowTransportFallback
	if fallback.ID <= 0 {
		allowTransportFallback = true
	}
	if input.AllowTransportFallback != nil {
		allowTransportFallback = *input.AllowTransportFallback
	}

	obfsMode := strings.TrimSpace(pointerString(input.ObfsMode))
	if obfsMode == "" {
		obfsMode = fallback.ObfsMode
	}
	switch obfsMode {
	case "":
		obfsMode = "off"
	case "off", "early_window_v2":
	default:
		return RelayListener{}, fmt.Errorf("%w: obfs_mode must be off or early_window_v2", ErrInvalidArgument)
	}
	if transportMode == "quic" {
		obfsMode = "off"
	}

	pinSet := append([]RelayPin(nil), fallback.PinSet...)
	if input.PinSet != nil {
		pinSet = normalizeRelayPins(*input.PinSet)
	}

	trustedCAIDs := append([]int(nil), fallback.TrustedCACertificateIDs...)
	if input.TrustedCACertificateIDs != nil {
		trustedCAIDs = normalizeRelayCAIDs(*input.TrustedCACertificateIDs)
	}

	allowSelfSigned := fallback.AllowSelfSigned
	if input.AllowSelfSigned != nil {
		allowSelfSigned = *input.AllowSelfSigned
	}

	tags := append([]string(nil), fallback.Tags...)
	if input.Tags != nil {
		tags = normalizeTags(*input.Tags)
	}

	if enabled {
		if certID == nil && !options.AllowMissingCertificate {
			return RelayListener{}, fmt.Errorf("%w: certificate_id is required when relay listener is enabled", ErrInvalidArgument)
		}
		if !options.SkipTrustValidation && certID != nil {
			switch tlsMode {
			case "pin_and_ca":
				if len(pinSet) == 0 || len(trustedCAIDs) == 0 {
					return RelayListener{}, fmt.Errorf("%w: pin_and_ca requires both pin_set and trusted_ca_certificate_ids", ErrInvalidArgument)
				}
			case "pin_only":
				if len(pinSet) == 0 {
					return RelayListener{}, fmt.Errorf("%w: pin_only requires pin_set", ErrInvalidArgument)
				}
			case "ca_only":
				if len(trustedCAIDs) == 0 {
					return RelayListener{}, fmt.Errorf("%w: ca_only requires trusted_ca_certificate_ids", ErrInvalidArgument)
				}
			default:
				if len(pinSet) == 0 && len(trustedCAIDs) == 0 {
					return RelayListener{}, fmt.Errorf("%w: pin_set and trusted_ca_certificate_ids cannot both be empty", ErrInvalidArgument)
				}
			}
		}
	}

	return RelayListener{
		ID:                      id,
		AgentID:                 fallback.AgentID,
		Name:                    name,
		BindHosts:               bindHosts,
		ListenHost:              listenHost,
		ListenPort:              listenPort,
		PublicHost:              publicHost,
		PublicPort:              publicPort,
		Enabled:                 enabled,
		CertificateID:           certID,
		TLSMode:                 tlsMode,
		TransportMode:           transportMode,
		AllowTransportFallback:  allowTransportFallback,
		ObfsMode:                obfsMode,
		PinSet:                  pinSet,
		TrustedCACertificateIDs: trustedCAIDs,
		AllowSelfSigned:         allowSelfSigned,
		Tags:                    tags,
		Revision:                fallback.Revision,
	}, nil
}

func (input RelayListenerInput) hasCertificateIDField() bool {
	return input.HasCertificateID || input.CertificateID != nil
}

func (input RelayListenerInput) hasTLSModeField() bool {
	return input.HasTLSMode || input.TLSMode != nil
}

func (input RelayListenerInput) hasPinSetField() bool {
	return input.HasPinSet || input.PinSet != nil
}

func (input RelayListenerInput) hasTrustedCACertificateIDsField() bool {
	return input.HasTrustedCACertificateIDs || input.TrustedCACertificateIDs != nil
}

func (input RelayListenerInput) hasAllowSelfSignedField() bool {
	return input.HasAllowSelfSigned || input.AllowSelfSigned != nil
}

func (input RelayListenerInput) hasExplicitRelayTrustFields() bool {
	return input.hasTLSModeField() ||
		input.hasPinSetField() ||
		input.hasTrustedCACertificateIDsField() ||
		input.hasAllowSelfSignedField()
}

func normalizeRelayCertificateSource(value *string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(pointerString(value)))
	switch normalized {
	case "", "auto_relay_ca", "existing_certificate":
		return normalized, nil
	default:
		return "", fmt.Errorf("%w: certificate_source must be auto_relay_ca or existing_certificate", ErrInvalidArgument)
	}
}

func normalizeRelayTrustModeSource(value *string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(pointerString(value)))
	switch normalized {
	case "", "auto", "custom":
		return normalized, nil
	default:
		return "", fmt.Errorf("%w: trust_mode_source must be auto or custom", ErrInvalidArgument)
	}
}

func (s *relayService) persistManagedCertificateMaterialBundles(ctx context.Context, bundles []storage.ManagedCertificateBundle, originalRows []storage.ManagedCertificateRow, nextRows []storage.ManagedCertificateRow) error {
	for _, bundle := range bundles {
		if strings.TrimSpace(bundle.Domain) == "" {
			continue
		}
		if err := s.store.SaveManagedCertificateMaterial(ctx, bundle.Domain, bundle); err != nil {
			cleanupManagedCertificateMaterialBestEffort(ctx, s.store, nextRows, originalRows)
			return err
		}
	}
	return nil
}

func (s *relayService) ensureAutoRelayListenerCertificate(ctx context.Context, rows []storage.ManagedCertificateRow, agentID string, listener RelayListener) (int, []storage.ManagedCertificateRow, []storage.ManagedCertificateBundle, error) {
	maxID := 0
	for _, row := range rows {
		if row.ID > maxID {
			maxID = row.ID
		}
	}

	relayCA, nextRows, bundles, err := s.ensureGlobalRelayCA(ctx, rows)
	if err != nil {
		return 0, nil, nil, err
	}
	relayCABundle, ok, err := loadManagedCertificateMaterial(ctx, s.store, relayCA.Domain, bundles)
	if err != nil {
		return 0, nil, nil, err
	}
	if !ok || strings.TrimSpace(relayCABundle.CertPEM) == "" || strings.TrimSpace(relayCABundle.KeyPEM) == "" {
		return 0, nil, nil, fmt.Errorf("%w: global relay ca material not found", ErrInvalidArgument)
	}

	maxRevision := 0
	for _, row := range nextRows {
		if row.ID > maxID {
			maxID = row.ID
		}
		if row.Revision > maxRevision {
			maxRevision = row.Revision
		}
	}
	nextID := maxID + 1
	autoCert := ManagedCertificate{
		ID:              nextID,
		Domain:          relayListenerAutoCertificateDomain(listener, agentID),
		Enabled:         true,
		Scope:           "domain",
		IssuerMode:      "local_http01",
		TargetAgentIDs:  []string{agentID},
		Status:          "active",
		Usage:           "relay_tunnel",
		CertificateType: "internal_ca",
		SelfSigned:      false,
		Tags:            autoRelayListenerCertificateTags(listener.ID, agentID),
		Revision:        maxRevision + 1,
	}
	materialBundle, err := generateRelayLeafMaterial(autoCert.Domain, relayCABundle, listener.PublicHost)
	if err != nil {
		return 0, nil, nil, err
	}
	materialBundle.ID = autoCert.ID
	materialBundle.Revision = int64(autoCert.Revision)
	autoCert.MaterialHash = hashManagedCertificateMaterial(materialBundle.CertPEM, materialBundle.KeyPEM)

	nextRows = append(nextRows, managedCertificateToRow(autoCert))
	bundles = append(bundles, materialBundle)
	return nextID, nextRows, bundles, nil
}

func (s *relayService) ensureGlobalRelayCA(ctx context.Context, rows []storage.ManagedCertificateRow) (ManagedCertificate, []storage.ManagedCertificateRow, []storage.ManagedCertificateBundle, error) {
	maxID := 0
	maxRevision := 0
	candidateIndexes := make([]int, 0)
	for index, row := range rows {
		if row.ID > maxID {
			maxID = row.ID
		}
		if row.Revision > maxRevision {
			maxRevision = row.Revision
		}
		if isRelayCACandidateForStartup(managedCertificateFromRow(row)) {
			candidateIndexes = append(candidateIndexes, index)
		}
	}
	if len(candidateIndexes) > 1 {
		return ManagedCertificate{}, nil, nil, fmt.Errorf("%w: multiple relay ca candidates found; manual cleanup required", ErrInvalidArgument)
	}

	nextRows := append([]storage.ManagedCertificateRow(nil), rows...)
	bundles := make([]storage.ManagedCertificateBundle, 0, 1)
	relayCA := ManagedCertificate{}
	relayCAIndex := -1
	if len(candidateIndexes) == 1 {
		relayCAIndex = candidateIndexes[0]
		current := managedCertificateFromRow(nextRows[relayCAIndex])
		canonical := s.buildCanonicalGlobalRelayCA(current, current.ID)
		if !managedCertificateInvariantFieldsEqual(current, canonical) {
			canonical.Status = current.Status
			canonical.LastIssueAt = current.LastIssueAt
			canonical.LastError = current.LastError
			canonical.MaterialHash = current.MaterialHash
			canonical.AgentReports = current.AgentReports
			canonical.ACMEInfo = current.ACMEInfo
			canonical.Revision = maxRevision + 1
			nextRows[relayCAIndex] = managedCertificateToRow(canonical)
			maxRevision = canonical.Revision
			relayCA = canonical
		} else {
			relayCA = current
		}
	} else {
		relayCA = s.buildCanonicalGlobalRelayCA(ManagedCertificate{}, maxID+1)
		relayCA.Revision = maxRevision + 1
		nextRows = append(nextRows, managedCertificateToRow(relayCA))
		relayCAIndex = len(nextRows) - 1
		maxRevision = relayCA.Revision
	}

	material, ok, err := s.store.LoadManagedCertificateMaterial(ctx, relayCA.Domain)
	if err != nil {
		return ManagedCertificate{}, nil, nil, err
	}
	if relayCA.Status == "active" && ok && validateUploadedManagedCertificateBundle(material) == nil {
		return relayCA, nextRows, bundles, nil
	}

	if !ok || validateUploadedManagedCertificateBundle(material) != nil {
		material, err = generateInternalCAMaterial(relayCA.Domain)
		if err != nil {
			return ManagedCertificate{}, nil, nil, err
		}
		bundles = append(bundles, material)
	}

	now := time.Now().UTC()
	issuedAt := now.Format(time.RFC3339)
	materialHash := hashManagedCertificateMaterial(strings.TrimSpace(material.CertPEM), strings.TrimSpace(material.KeyPEM))
	relayCA.Status = "active"
	relayCA.LastIssueAt = issuedAt
	relayCA.LastError = ""
	relayCA.MaterialHash = materialHash
	for _, targetAgentID := range relayCA.TargetAgentIDs {
		relayCA = updateManagedCertificateAgentReport(relayCA, targetAgentID, ManagedCertificateHeartbeatReport{
			Status:       "active",
			LastIssueAt:  issuedAt,
			LastError:    "",
			MaterialHash: materialHash,
			ACMEInfo:     relayCA.ACMEInfo,
			UpdatedAt:    issuedAt,
		}, now)
	}
	nextRows[relayCAIndex] = managedCertificateToRow(relayCA)
	return relayCA, nextRows, bundles, nil
}

func isRelayCACandidateForStartup(cert ManagedCertificate) bool {
	return strings.EqualFold(strings.TrimSpace(cert.Domain), relayCADomainIdentity) || cert.Usage == "relay_ca" || usesReservedRelayCATags(cert.Tags)
}

func (s *relayService) buildCanonicalGlobalRelayCA(existing ManagedCertificate, certID int) ManagedCertificate {
	targetAgentIDs := []string{}
	if s.cfg.EnableLocalAgent && strings.TrimSpace(s.cfg.LocalAgentID) != "" {
		targetAgentIDs = []string{strings.TrimSpace(s.cfg.LocalAgentID)}
	}
	return ManagedCertificate{
		ID:              certID,
		Domain:          relayCADomainIdentity,
		Enabled:         true,
		Scope:           "domain",
		IssuerMode:      "local_http01",
		TargetAgentIDs:  targetAgentIDs,
		Status:          existing.Status,
		LastIssueAt:     existing.LastIssueAt,
		LastError:       existing.LastError,
		MaterialHash:    existing.MaterialHash,
		AgentReports:    existing.AgentReports,
		ACMEInfo:        existing.ACMEInfo,
		Tags:            normalizeTags([]string{systemRelayCATag, systemTag}),
		Usage:           "relay_ca",
		CertificateType: "internal_ca",
		SelfSigned:      true,
		Revision:        existing.Revision,
	}
}

func managedCertificateInvariantFieldsEqual(left ManagedCertificate, right ManagedCertificate) bool {
	if !strings.EqualFold(strings.TrimSpace(left.Domain), strings.TrimSpace(right.Domain)) {
		return false
	}
	if left.Enabled != right.Enabled || left.Scope != right.Scope || left.IssuerMode != right.IssuerMode {
		return false
	}
	if left.Usage != right.Usage || left.CertificateType != right.CertificateType || left.SelfSigned != right.SelfSigned {
		return false
	}
	if len(left.TargetAgentIDs) != len(right.TargetAgentIDs) || len(left.Tags) != len(right.Tags) {
		return false
	}
	for index, value := range left.TargetAgentIDs {
		if value != right.TargetAgentIDs[index] {
			return false
		}
	}
	for index, value := range left.Tags {
		if value != right.Tags[index] {
			return false
		}
	}
	return true
}

func relayListenerAutoCertificateDomain(listener RelayListener, agentID string) string {
	host := strings.TrimSpace(listener.PublicHost)
	if host == "" && len(listener.BindHosts) > 0 {
		host = strings.TrimSpace(listener.BindHosts[0])
	}
	if host == "" {
		host = strings.TrimSpace(listener.ListenHost)
	}
	return fmt.Sprintf(
		"listener-%d.%s.%s-%s.relay.internal",
		listener.ID,
		normalizeRelayListenerDomainLabel(host, "listener"),
		normalizeRelayListenerDomainLabel(agentID, "agent"),
		relayListenerAutoCertificateNonce(),
	)
}

func normalizeRelayListenerDomainLabel(value string, fallback string) string {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	builder := strings.Builder{}
	lastDash := false
	for _, r := range trimmed {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			builder.WriteRune(r)
			lastDash = false
		case !lastDash:
			builder.WriteByte('-')
			lastDash = true
		}
	}
	normalized := strings.Trim(builder.String(), "-")
	if normalized == "" {
		return fallback
	}
	return normalized
}

func relayListenerCertificateChanged(current *int, next *int) bool {
	switch {
	case current == nil && next == nil:
		return false
	case current == nil || next == nil:
		return true
	default:
		return *current != *next
	}
}

type relayRuleReference struct {
	AgentID  string
	RuleID   int
	RuleType string
}

func (s *relayService) findRelayListenerReference(ctx context.Context, listenerID int) (*relayRuleReference, error) {
	agentIDs, err := s.allKnownAgentIDs(ctx)
	if err != nil {
		return nil, err
	}
	for _, agentID := range agentIDs {
		httpRules, err := s.store.ListHTTPRules(ctx, agentID)
		if err != nil {
			return nil, err
		}
		for _, row := range httpRules {
			if containsInt(parseIntArray(row.RelayChainJSON), listenerID) {
				return &relayRuleReference{AgentID: agentID, RuleID: row.ID, RuleType: "HTTP"}, nil
			}
		}

		l4Rules, err := s.store.ListL4Rules(ctx, agentID)
		if err != nil {
			return nil, err
		}
		for _, row := range l4Rules {
			if containsInt(parseIntArray(row.RelayChainJSON), listenerID) {
				return &relayRuleReference{AgentID: agentID, RuleID: row.ID, RuleType: "L4"}, nil
			}
		}
	}
	return nil, nil
}

func (s *relayService) allKnownAgentIDs(ctx context.Context) ([]string, error) {
	return allKnownAgentIDs(ctx, s.cfg, s.store)
}

func (s *relayService) cleanupUnusedAutoRelayListenerCertificate(ctx context.Context, certID int) error {
	certRows, err := s.store.ListManagedCertificates(ctx)
	if err != nil {
		return err
	}
	cert, certIndex, ok := findManagedCertificateByID(certRows, certID)
	if !ok || !isAutoRelayListenerCertificate(cert, 0) {
		return nil
	}
	listeners, err := s.store.ListRelayListeners(ctx, "")
	if err != nil {
		return err
	}
	for _, row := range listeners {
		if row.CertificateID != nil && *row.CertificateID == certID {
			return nil
		}
	}
	nextRows := append([]storage.ManagedCertificateRow(nil), certRows[:certIndex]...)
	nextRows = append(nextRows, certRows[certIndex+1:]...)
	if err := s.store.SaveManagedCertificates(ctx, nextRows); err != nil {
		return err
	}
	cleanupManagedCertificateMaterialBestEffort(ctx, s.store, certRows, nextRows)
	return nil
}

func containsInt(values []int, target int) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func normalizeRelayPins(pins []RelayPin) []RelayPin {
	normalized := make([]RelayPin, 0, len(pins))
	for _, pin := range pins {
		if strings.TrimSpace(pin.Type) == "" || strings.TrimSpace(pin.Value) == "" {
			continue
		}
		normalized = append(normalized, RelayPin{
			Type:  strings.TrimSpace(pin.Type),
			Value: strings.TrimSpace(pin.Value),
		})
	}
	return normalized
}

func normalizeRelayBindHosts(values []string) []string {
	seen := map[string]struct{}{}
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	return normalized
}

func normalizeRelayCAIDs(values []int) []int {
	seen := map[int]struct{}{}
	normalized := make([]int, 0, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	return normalized
}

func relayListenerFromRow(row storage.RelayListenerRow) RelayListener {
	listener := RelayListener{
		ID:                     row.ID,
		AgentID:                row.AgentID,
		Name:                   row.Name,
		ListenHost:             defaultString(row.ListenHost, "0.0.0.0"),
		ListenPort:             row.ListenPort,
		PublicHost:             defaultString(row.PublicHost, row.ListenHost),
		PublicPort:             row.PublicPort,
		Enabled:                row.Enabled,
		CertificateID:          row.CertificateID,
		TLSMode:                defaultString(row.TLSMode, "pin_or_ca"),
		TransportMode:          defaultString(row.TransportMode, "tls_tcp"),
		ObfsMode:               defaultString(row.ObfsMode, "off"),
		AllowTransportFallback: row.AllowTransportFallback,
		AllowSelfSigned:        row.AllowSelfSigned,
		Tags:                   parseStringArray(row.TagsJSON),
		Revision:               row.Revision,
	}
	if strings.TrimSpace(row.TransportMode) == "" {
		listener.AllowTransportFallback = true
	}
	if err := json.Unmarshal([]byte(defaultString(row.BindHostsJSON, "[]")), &listener.BindHosts); err != nil {
		listener.BindHosts = []string{listener.ListenHost}
	}
	if len(listener.BindHosts) == 0 {
		listener.BindHosts = []string{listener.ListenHost}
	}
	if err := json.Unmarshal([]byte(defaultString(row.PinSetJSON, "[]")), &listener.PinSet); err != nil {
		listener.PinSet = []RelayPin{}
	}
	listener.PinSet = normalizeRelayPins(listener.PinSet)
	listener.TrustedCACertificateIDs = parseIntArray(row.TrustedCACertificateIDs)
	if listener.PublicPort <= 0 {
		listener.PublicPort = listener.ListenPort
	}
	return listener
}

func relayListenerToRow(listener RelayListener) storage.RelayListenerRow {
	return storage.RelayListenerRow{
		ID:                      listener.ID,
		AgentID:                 listener.AgentID,
		Name:                    listener.Name,
		BindHostsJSON:           marshalJSON(listener.BindHosts, "[]"),
		ListenHost:              listener.ListenHost,
		ListenPort:              listener.ListenPort,
		PublicHost:              listener.PublicHost,
		PublicPort:              listener.PublicPort,
		Enabled:                 listener.Enabled,
		CertificateID:           listener.CertificateID,
		TLSMode:                 listener.TLSMode,
		TransportMode:           listener.TransportMode,
		AllowTransportFallback:  listener.AllowTransportFallback,
		ObfsMode:                listener.ObfsMode,
		PinSetJSON:              marshalJSON(listener.PinSet, "[]"),
		TrustedCACertificateIDs: marshalJSON(listener.TrustedCACertificateIDs, "[]"),
		AllowSelfSigned:         listener.AllowSelfSigned,
		TagsJSON:                marshalJSON(listener.Tags, "[]"),
		Revision:                listener.Revision,
	}
}
