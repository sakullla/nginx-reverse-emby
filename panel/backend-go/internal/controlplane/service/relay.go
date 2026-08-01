package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/revision"
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
	ID            int      `json:"id"`
	AgentID       string   `json:"agent_id"`
	AgentName     string   `json:"agent_name,omitempty"`
	Name          string   `json:"name"`
	BindHosts     []string `json:"bind_hosts"`
	ListenHost    string   `json:"listen_host"`
	ListenPort    int      `json:"listen_port"`
	PublicHost    string   `json:"public_host"`
	PublicPort    int      `json:"public_port"`
	Enabled       bool     `json:"enabled"`
	CertificateID *int     `json:"certificate_id"`
	TLSMode       string   `json:"tls_mode"`
	TransportMode string   `json:"transport_mode"`

	AllowTransportFallback  bool       `json:"allow_transport_fallback"`
	ObfsMode                string     `json:"obfs_mode"`
	PinSet                  []RelayPin `json:"pin_set"`
	TrustedCACertificateIDs []int      `json:"trusted_ca_certificate_ids"`
	AllowSelfSigned         bool       `json:"allow_self_signed"`
	Tags                    []string   `json:"tags"`
	Revision                int        `json:"revision"`
}

type RelayListenerInput struct {
	ID            *int      `json:"id,omitempty"`
	Name          *string   `json:"name,omitempty"`
	BindHosts     *[]string `json:"bind_hosts,omitempty"`
	ListenHost    *string   `json:"listen_host,omitempty"`
	ListenPort    *int      `json:"listen_port,omitempty"`
	PublicHost    *string   `json:"public_host,omitempty"`
	PublicPort    *int      `json:"public_port,omitempty"`
	Enabled       *bool     `json:"enabled,omitempty"`
	CertificateID *int      `json:"certificate_id,omitempty"`
	TLSMode       *string   `json:"tls_mode,omitempty"`
	TransportMode *string   `json:"transport_mode,omitempty"`

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
	cfg                   config.Config
	store                 storage.Store
	materialRecoveryStore storage.Store
	localApplyTrigger     func(context.Context) error
	mutationExecutor      *revision.Executor
	revisionMutation      bool
	revisionNumbers       map[string]int64
	postCommitActions     *[]func()
	rollbackActions       *[]func()
	materialRollbacks     *[]func() error
	pkiListenerRevoker    func(context.Context, string, int) error
}

func NewRelayListenerService(cfg config.Config, store storage.Store) *relayService {
	return &relayService{
		cfg:                   cfg,
		store:                 store,
		materialRecoveryStore: store,
		mutationExecutor:      newConfigMutationExecutor(cfg, store),
	}
}

func (s *relayService) SetLocalApplyTrigger(trigger func(context.Context) error) {
	s.localApplyTrigger = wrapLocalApplyTrigger(trigger)
}

func (s *relayService) SetPKIListenerRevoker(revoker func(context.Context, string, int) error) {
	s.pkiListenerRevoker = revoker
}

func (s *relayService) triggerLocalApply(ctx context.Context, agentID string) error {
	if s.revisionMutation {
		return nil
	}
	if !s.cfg.EnableLocalAgent || agentID != s.cfg.LocalAgentID || s.localApplyTrigger == nil {
		return nil
	}
	return s.localApplyTrigger(ctx)
}

func (s *relayService) canonicalPKIState(ctx context.Context) (storage.PKICanonicalState, bool, error) {
	if schema, ok := s.store.(interface {
		HasPKICanonicalSchema(context.Context) (bool, error)
	}); ok {
		present, err := schema.HasPKICanonicalSchema(ctx)
		if err != nil || !present {
			return storage.PKICanonicalState{}, false, err
		}
	}
	source, ok := s.store.(interface {
		LoadPKICanonicalState(context.Context) (storage.PKICanonicalState, error)
	})
	if !ok {
		return storage.PKICanonicalState{}, false, nil
	}
	state, err := source.LoadPKICanonicalState(ctx)
	if err != nil {
		return storage.PKICanonicalState{}, false, err
	}
	return state, true, nil
}

func (s *relayService) canonicalPKIPresent(ctx context.Context) (bool, error) {
	state, available, err := s.canonicalPKIState(ctx)
	return available && state.Settings != nil, err
}

func (s *relayService) canonicalPKIEnabled(ctx context.Context) (bool, error) {
	state, available, err := s.canonicalPKIState(ctx)
	return available && state.Settings != nil && state.Settings.UpgradeState == PKIUpgradeStateTunnelMTLSOnly, err
}

func relayListenerPKIMode(listener RelayListener) RelayListener {
	listener.CertificateID = nil
	listener.TLSMode = "pki_mtls"
	listener.PinSet = nil
	listener.TrustedCACertificateIDs = nil
	listener.AllowSelfSigned = false
	return listener
}

func (s *relayService) ensurePKIListenerIdentity(ctx context.Context, listener RelayListener) error {
	present, err := s.canonicalPKIPresent(ctx)
	if err != nil || !present {
		return err
	}
	store, ok := s.store.(PKITransactionStore)
	if !ok {
		return nil
	}
	return store.WithPKITransaction(ctx, func(tx *storage.PKITransaction) error {
		settings, found, err := tx.GetPKISettings(ctx)
		if err != nil || !found {
			return err
		}
		listenerID := strconv.Itoa(listener.ID)
		if identity, found, err := tx.FindPKIIdentityForUpdate(ctx, settings.PKIDomainID, storage.PKIIdentityKindListener, listener.AgentID, listenerID); err != nil {
			return err
		} else if found {
			if identity.State == storage.PKIIdentityStateRevoked {
				return fmt.Errorf("%w: relay listener ID %s was retired after PKI revocation and cannot be reused", ErrInvalidArgument, listenerID)
			}
			return nil
		}
		identityID, err := randomPKIIdentifier(rand.Reader)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		if err := tx.CreatePKIIdentity(ctx, storage.PKIIdentityRow{
			ID: identityID, PKIDomainID: settings.PKIDomainID, Kind: storage.PKIIdentityKindListener,
			AgentID: listener.AgentID, ListenerID: listenerID, State: storage.PKIIdentityStateEnrollmentRequired,
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		eventID, err := randomPKIIdentifier(rand.Reader)
		if err != nil {
			return err
		}
		return tx.AppendPKIEvent(ctx, storage.PKIEventRow{
			ID: eventID, PKIDomainID: settings.PKIDomainID, Type: "pki.listener.enrollment_required",
			OccurredAt: now, Source: "control_plane", ObjectType: "identity", ObjectID: identityID,
			Result: "success", SecurityRevision: settings.SecurityRevision,
			DetailsJSON: fmt.Sprintf(`{"agent_id":%q,"listener_id":%q}`, listener.AgentID, listenerID),
		})
	})
}

func (s *relayService) Bootstrap(ctx context.Context) error {
	pkiEnabled, err := s.canonicalPKIPresent(ctx)
	if err != nil {
		return err
	}
	if pkiEnabled {
		return nil
	}
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
		if _, err := s.persistManagedCertificateMaterialBundles(ctx, bundles, rows, nextRows); err != nil {
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

// FinalizeTunnelMTLSUpgrade removes only legacy relay authentication material.
// Agent rows, control tokens, rules, listener IDs/names/tags and associations
// remain untouched.
func (s *relayService) FinalizeTunnelMTLSUpgrade(
	ctx context.Context,
	commit func(context.Context, *storage.GormStore) error,
) error {
	mutationStore, ok := s.store.(interface {
		WithRevisionMutation(context.Context, storage.RevisionMutationFunc) error
	})
	if !ok {
		return fmt.Errorf("%w: atomic relay activation store is required", ErrPKILifecycleInvalid)
	}
	var originalCertificates []storage.ManagedCertificateRow
	var publicCertificates []storage.ManagedCertificateRow
	err := mutationStore.WithRevisionMutation(ctx, func(txStore *storage.GormStore) (storage.RevisionMutationDecision, error) {
		txService := &relayService{
			cfg: s.cfg, store: txStore, materialRecoveryStore: s.durableMaterialStore(), revisionMutation: true,
		}
		var finalizeErr error
		originalCertificates, publicCertificates, finalizeErr = txService.finalizeTunnelMTLSUpgradeRows(ctx)
		if finalizeErr != nil {
			return storage.RevisionMutationDecision{}, finalizeErr
		}
		if commit != nil {
			if err := commit(ctx, txStore); err != nil {
				return storage.RevisionMutationDecision{}, err
			}
		}
		return storage.RevisionMutationDecision{}, nil
	})
	if err != nil {
		return err
	}
	cleanupManagedCertificateMaterialBestEffort(ctx, s.durableMaterialStore(), originalCertificates, publicCertificates)
	return nil
}

func (s *relayService) finalizeTunnelMTLSUpgradeRows(ctx context.Context) ([]storage.ManagedCertificateRow, []storage.ManagedCertificateRow, error) {
	originalCertificates, err := s.store.ListManagedCertificates(ctx)
	if err != nil {
		return nil, nil, err
	}
	publicCertificates := make([]storage.ManagedCertificateRow, 0, len(originalCertificates))
	for _, row := range originalCertificates {
		certificate := managedCertificateFromRow(row)
		if certificate.Usage == "relay_ca" || certificate.Usage == "relay_tunnel" ||
			strings.EqualFold(strings.TrimSpace(certificate.Domain), relayCADomainIdentity) {
			continue
		}
		publicCertificates = append(publicCertificates, row)
	}
	if !managedCertificateRowsEqual(originalCertificates, publicCertificates) {
		if err := s.store.SaveManagedCertificates(ctx, publicCertificates); err != nil {
			return nil, nil, err
		}
	}

	listeners, err := s.store.ListRelayListeners(ctx, "")
	if err != nil {
		return nil, nil, err
	}
	byAgent := make(map[string][]storage.RelayListenerRow)
	for _, listener := range listeners {
		listener.CertificateID = nil
		listener.TLSMode = "pki_mtls"
		listener.PinSetJSON = "[]"
		listener.TrustedCACertificateIDs = "[]"
		listener.AllowSelfSigned = false
		byAgent[listener.AgentID] = append(byAgent[listener.AgentID], listener)
	}
	for agentID, rows := range byAgent {
		if err := s.store.SaveRelayListeners(ctx, agentID, rows); err != nil {
			return nil, nil, err
		}
	}
	return originalCertificates, publicCertificates, nil
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
		if !relayListenerRowSupported(row) {
			continue
		}
		listener := relayListenerFromRow(row)
		if pkiEnabled, pkiErr := s.canonicalPKIEnabled(ctx); pkiErr != nil {
			return nil, pkiErr
		} else if pkiEnabled {
			listener = relayListenerPKIMode(listener)
		}
		listeners = append(listeners, listener)
	}
	return listeners, nil
}

func (s *relayService) ListPage(ctx context.Context, query ListQuery) ([]RelayListener, PageMeta, error) {
	query = NormalizeListQuery(query)
	names, err := agentDisplayNameMap(ctx, s.cfg, s.store)
	if err != nil {
		return nil, PageMeta{}, err
	}

	var rows []storage.RelayListenerRow
	if query.AgentID != "" {
		resolvedID, err := s.ensureAgentExists(ctx, query.AgentID)
		if err != nil {
			return nil, PageMeta{}, err
		}
		rows, err = s.store.ListRelayListeners(ctx, resolvedID)
		if err != nil {
			return nil, PageMeta{}, err
		}
	} else {
		rows, err = s.store.ListRelayListeners(ctx, "")
		if err != nil {
			return nil, PageMeta{}, err
		}
	}

	syncRevisions := map[string]int{}
	if query.Sync != "" {
		revisions, syncErr := agentLastApplyRevisionMap(ctx, s.cfg, s.store)
		if syncErr != nil {
			return nil, PageMeta{}, syncErr
		}
		syncRevisions = revisions
	}

	filtered := make([]RelayListener, 0, len(rows))
	for _, row := range rows {
		if !relayListenerRowSupported(row) {
			continue
		}
		listener := relayListenerFromRow(row)
		if pkiEnabled, pkiErr := s.canonicalPKIEnabled(ctx); pkiErr != nil {
			return nil, PageMeta{}, pkiErr
		} else if pkiEnabled {
			listener = relayListenerPKIMode(listener)
		}
		if strings.TrimSpace(listener.AgentID) == "" {
			listener.AgentID = row.AgentID
		}
		listener.AgentName = resolveAgentDisplayName(names, listener.AgentID)
		searchFields := []string{listener.Name, listener.PublicHost, listener.ListenHost, strconv.Itoa(listener.ListenPort), listener.AgentID, listener.AgentName, strings.Join(listener.Tags, " ")}
		if listener.PublicPort > 0 {
			publicPort := strconv.Itoa(listener.PublicPort)
			searchFields = append(searchFields, publicPort)
			if strings.TrimSpace(listener.PublicHost) != "" {
				searchFields = append(searchFields, net.JoinHostPort(listener.PublicHost, publicPort))
			}
		}
		if !matchesListQuery(query.Q, searchFields...) {
			continue
		}
		if !matchesEnabledFilter(query.Enabled, listener.Enabled) {
			continue
		}
		if !matchesTagsFilter(query.Tags, listener.Tags) {
			continue
		}
		if !matchesOptionalIntFilter(query.CertificateID, listener.CertificateID) {
			continue
		}
		if query.Sync != "" {
			lastApplyRevision, agentKnown := syncRevisions[listener.AgentID]
			if !matchesSyncFilter(query.Sync, listener.Revision, lastApplyRevision, agentKnown) {
				continue
			}
		}
		filtered = append(filtered, listener)
	}
	page, meta := ApplyPage(filtered, query)
	return page, meta, nil
}

func (s *relayService) Create(ctx context.Context, agentID string, input RelayListenerInput) (RelayListener, error) {
	if err := requireConfigMutationStore(s.store, s.mutationExecutor, s.revisionMutation); err != nil {
		return RelayListener{}, err
	}
	if s.mutationExecutor == nil || s.revisionMutation {
		return s.createLegacy(ctx, agentID, input)
	}
	resolvedID, err := s.ensureAgentExists(ctx, agentID)
	if err != nil {
		return RelayListener{}, err
	}
	postCommitActions := make([]func(), 0)
	rollbackActions := make([]func(), 0)
	materialRollbacks := make([]func() error, 0)
	var created RelayListener
	_, err = s.mutationExecutor.Execute(ctx, revision.MutationRequest{
		Kind:                "relay_listener.create",
		DependencyAction:    revision.DependencyActionApply,
		Request:             input,
		Targets:             configMutationTargets(s.cfg, []string{resolvedID}, nil),
		ResourceState:       relayListenerMutationResourceState,
		ReplayResourceField: "listener",
		ReplayResource:      func() any { return created },
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			txService := &relayService{
				cfg: s.cfg, store: tx, materialRecoveryStore: s.durableMaterialStore(), revisionMutation: true, revisionNumbers: revisions,
				postCommitActions: &postCommitActions, rollbackActions: &rollbackActions,
				materialRollbacks: &materialRollbacks,
			}
			var mutateErr error
			created, mutateErr = txService.createLegacy(ctx, resolvedID, input)
			return mutateErr
		},
	})
	if err != nil {
		err = relayMaterialRollbackError(err, materialRollbacks)
		runConfigPostCommitActions(rollbackActions)
		return RelayListener{}, err
	}
	runConfigPostCommitActions(postCommitActions)
	return created, nil
}

func (s *relayService) createLegacy(ctx context.Context, agentID string, input RelayListenerInput) (RelayListener, error) {
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

	existing := make([]RelayListener, 0, len(rows))
	maxRevision := 0
	for _, row := range allRows {
		if row.Revision > maxRevision {
			maxRevision = row.Revision
		}
	}
	for _, row := range rows {
		if !relayListenerRowSupported(row) {
			continue
		}
		existing = append(existing, relayListenerFromRow(row))
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
	listener.Revision = configMutationRevision(s.revisionNumbers, resolvedID, allocator.AllocateRevisionForAgent(resolvedID, maxRevision))
	if err := ensureUniqueRelayListen(existing, listener, 0); err != nil {
		return RelayListener{}, err
	}

	var materialRollbacks relayMaterialRollbackActions
	if prepared.PersistCertificates {
		if err := s.store.SaveManagedCertificates(ctx, prepared.NextCertRows); err != nil {
			return RelayListener{}, err
		}
		materialRollbacks, err = s.persistManagedCertificateMaterialBundles(ctx, prepared.MaterialBundles, prepared.OriginalCertRows, prepared.NextCertRows)
		if err != nil {
			if rollbackErr := s.store.SaveManagedCertificates(ctx, prepared.OriginalCertRows); rollbackErr != nil {
				return RelayListener{}, fmt.Errorf("%v (rollback failed: %v)", err, rollbackErr)
			}
			return RelayListener{}, err
		}
		s.runAfterRevisionMaterialRollback(materialRollbacks.recovery)
		s.runAfterRevisionRollback(func() {
			cleanupManagedCertificateMaterialBestEffort(ctx, s.durableMaterialStore(), prepared.NextCertRows, prepared.OriginalCertRows)
		})
	}
	rows = append(rows, relayListenerToRow(listener))
	if err := s.store.SaveRelayListeners(ctx, resolvedID, rows); err != nil {
		if prepared.PersistCertificates {
			err = relayMaterialRollbackError(err, materialRollbacks.immediate)
			if rollbackErr := s.store.SaveManagedCertificates(ctx, prepared.OriginalCertRows); rollbackErr != nil {
				return RelayListener{}, fmt.Errorf("%v (rollback failed: %v)", err, rollbackErr)
			}
			cleanupManagedCertificateMaterialBestEffort(ctx, s.store, prepared.NextCertRows, prepared.OriginalCertRows)
		}
		return RelayListener{}, err
	}
	if err := s.ensurePKIListenerIdentity(ctx, listener); err != nil {
		return RelayListener{}, err
	}
	if err := s.bumpRemoteDesiredRevision(ctx, resolvedID, listener.Revision); err != nil {
		return RelayListener{}, err
	}
	if prepared.PersistCertificates {
		s.runAfterRevisionCommit(func() {
			cleanupManagedCertificateMaterialBestEffort(ctx, s.durableMaterialStore(), prepared.OriginalCertRows, prepared.NextCertRows)
		})
	}
	if err := s.triggerLocalApply(ctx, resolvedID); err != nil {
		return RelayListener{}, err
	}
	return listener, nil
}

func (s *relayService) Update(ctx context.Context, agentID string, id int, input RelayListenerInput) (RelayListener, error) {
	if err := requireConfigMutationStore(s.store, s.mutationExecutor, s.revisionMutation); err != nil {
		return RelayListener{}, err
	}
	if s.mutationExecutor == nil || s.revisionMutation {
		return s.updateLegacy(ctx, agentID, id, input)
	}
	resolvedID, err := s.ensureAgentExists(ctx, agentID)
	if err != nil {
		return RelayListener{}, err
	}
	if _, err := s.listenerByID(ctx, resolvedID, id); err != nil {
		return RelayListener{}, err
	}
	targetAgentIDs, err := s.relayMutationAgentIDs(ctx, resolvedID, id)
	if err != nil {
		return RelayListener{}, err
	}
	postCommitActions := make([]func(), 0)
	rollbackActions := make([]func(), 0)
	materialRollbacks := make([]func() error, 0)
	var updated RelayListener
	result, err := s.mutationExecutor.Execute(ctx, revision.MutationRequest{
		Kind:             "relay_listener.update",
		DependencyAction: revision.DependencyActionApply,
		Request: struct {
			ID    int                `json:"id"`
			Input RelayListenerInput `json:"input"`
		}{ID: id, Input: input},
		Targets:             configMutationTargets(s.cfg, targetAgentIDs, nil),
		ResourceState:       relayListenerMutationResourceState,
		ReplayResourceField: "listener",
		ReplayResource:      func() any { return updated },
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			txService := &relayService{
				cfg: s.cfg, store: tx, materialRecoveryStore: s.durableMaterialStore(), revisionMutation: true, revisionNumbers: revisions,
				postCommitActions: &postCommitActions, rollbackActions: &rollbackActions,
				materialRollbacks: &materialRollbacks,
			}
			var mutateErr error
			updated, mutateErr = txService.updateLegacy(ctx, resolvedID, id, input)
			return mutateErr
		},
	})
	if err != nil {
		err = relayMaterialRollbackError(err, materialRollbacks)
		runConfigPostCommitActions(rollbackActions)
		return RelayListener{}, err
	}
	if result.NoOp {
		if rollbackErr := runRelayMaterialRollbacks(materialRollbacks); rollbackErr != nil {
			return RelayListener{}, fmt.Errorf("relay certificate material restore failed: %w", rollbackErr)
		}
		runConfigPostCommitActions(rollbackActions)
		return s.listenerByID(ctx, resolvedID, id)
	}
	runConfigPostCommitActions(postCommitActions)
	return updated, nil
}

func (s *relayService) updateLegacy(ctx context.Context, agentID string, id int, input RelayListenerInput) (RelayListener, error) {
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

	existing := make([]RelayListener, 0, len(rows))
	maxRevision := 0
	targetIndex := -1
	var current RelayListener
	for i, row := range rows {
		if row.Revision > maxRevision {
			maxRevision = row.Revision
		}
		if !relayListenerRowSupported(row) {
			continue
		}
		listener := relayListenerFromRow(row)
		existing = append(existing, listener)
		if row.ID == id {
			targetIndex = i
			current = listener
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
	if err := validateRelayLiveBindingTransition(current, listener); err != nil {
		return RelayListener{}, err
	}
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
	listener.Revision = configMutationRevision(s.revisionNumbers, resolvedID, allocator.AllocateRevisionForAgent(resolvedID, maxRevision))
	if err := ensureUniqueRelayListen(existing, listener, id); err != nil {
		return RelayListener{}, err
	}

	var materialRollbacks relayMaterialRollbackActions
	if prepared.PersistCertificates {
		if err := s.store.SaveManagedCertificates(ctx, prepared.NextCertRows); err != nil {
			return RelayListener{}, err
		}
		materialRollbacks, err = s.persistManagedCertificateMaterialBundles(ctx, prepared.MaterialBundles, prepared.OriginalCertRows, prepared.NextCertRows)
		if err != nil {
			if rollbackErr := s.store.SaveManagedCertificates(ctx, prepared.OriginalCertRows); rollbackErr != nil {
				return RelayListener{}, fmt.Errorf("%v (rollback failed: %v)", err, rollbackErr)
			}
			return RelayListener{}, err
		}
		s.runAfterRevisionMaterialRollback(materialRollbacks.recovery)
		s.runAfterRevisionRollback(func() {
			cleanupManagedCertificateMaterialBestEffort(ctx, s.durableMaterialStore(), prepared.NextCertRows, prepared.OriginalCertRows)
		})
	}
	rows[targetIndex] = relayListenerToRow(listener)
	if err := s.store.SaveRelayListeners(ctx, resolvedID, rows); err != nil {
		if prepared.PersistCertificates {
			err = relayMaterialRollbackError(err, materialRollbacks.immediate)
			if rollbackErr := s.store.SaveManagedCertificates(ctx, prepared.OriginalCertRows); rollbackErr != nil {
				return RelayListener{}, fmt.Errorf("%v (rollback failed: %v)", err, rollbackErr)
			}
			cleanupManagedCertificateMaterialBestEffort(ctx, s.store, prepared.NextCertRows, prepared.OriginalCertRows)
		}
		return RelayListener{}, err
	}
	if err := s.ensurePKIListenerIdentity(ctx, listener); err != nil {
		return RelayListener{}, err
	}
	if err := s.bumpRemoteDesiredRevision(ctx, resolvedID, listener.Revision); err != nil {
		return RelayListener{}, err
	}
	if prepared.PersistCertificates {
		s.runAfterRevisionCommit(func() {
			cleanupManagedCertificateMaterialBestEffort(ctx, s.durableMaterialStore(), prepared.OriginalCertRows, prepared.NextCertRows)
		})
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
	if err := requireConfigMutationStore(s.store, s.mutationExecutor, s.revisionMutation); err != nil {
		return RelayListener{}, err
	}
	if s.pkiListenerRevoker != nil && !s.revisionMutation {
		resolvedID, err := s.ensureAgentExists(ctx, agentID)
		if err != nil {
			return RelayListener{}, err
		}
		listener, err := s.listenerByID(ctx, resolvedID, id)
		if err != nil {
			return RelayListener{}, err
		}
		reference, err := s.findRelayListenerReference(ctx, listener.ID)
		if err != nil {
			return RelayListener{}, err
		}
		if reference != nil {
			return RelayListener{}, fmt.Errorf(
				"%w: relay listener %d is referenced by %s rule #%d on agent %s",
				ErrInvalidArgument, listener.ID, reference.RuleType, reference.RuleID, reference.AgentID,
			)
		}
		if err := s.pkiListenerRevoker(ctx, resolvedID, id); err != nil {
			return RelayListener{}, err
		}
	}
	if s.mutationExecutor == nil || s.revisionMutation {
		return s.deleteLegacy(ctx, agentID, id)
	}
	resolvedID, err := s.ensureAgentExists(ctx, agentID)
	if err != nil {
		return RelayListener{}, err
	}
	if _, err := s.listenerByID(ctx, resolvedID, id); err != nil {
		return RelayListener{}, err
	}
	targetAgentIDs, err := s.relayMutationAgentIDs(ctx, resolvedID, id)
	if err != nil {
		return RelayListener{}, err
	}
	postCommitActions := make([]func(), 0)
	var deleted RelayListener
	_, err = s.mutationExecutor.Execute(ctx, revision.MutationRequest{
		Kind:                "relay_listener.delete",
		DependencyAction:    revision.DependencyActionDelete,
		Request:             map[string]int{"id": id},
		Targets:             configMutationTargets(s.cfg, targetAgentIDs, nil),
		ResourceState:       relayListenerMutationResourceState,
		ReplayResourceField: "listener",
		ReplayResource:      func() any { return deleted },
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			txService := &relayService{
				cfg: s.cfg, store: tx, materialRecoveryStore: s.durableMaterialStore(), revisionMutation: true, revisionNumbers: revisions,
				postCommitActions: &postCommitActions,
			}
			var mutateErr error
			deleted, mutateErr = txService.deleteLegacy(ctx, resolvedID, id)
			return mutateErr
		},
	})
	if err != nil {
		return RelayListener{}, err
	}
	runConfigPostCommitActions(postCommitActions)
	return deleted, nil
}

func (s *relayService) deleteLegacy(ctx context.Context, agentID string, id int) (RelayListener, error) {
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
		if !relayListenerRowSupported(row) {
			continue
		}
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
	_ = deleteTrafficByScopeIfSupported(ctx, s.store, resolvedID, "relay_listener", deleted.ID)
	return deleted, nil
}

func (s *relayService) listenerByID(ctx context.Context, agentID string, id int) (RelayListener, error) {
	rows, err := s.store.ListRelayListeners(ctx, agentID)
	if err != nil {
		return RelayListener{}, err
	}
	for _, row := range rows {
		if row.ID == id && relayListenerRowSupported(row) {
			return relayListenerFromRow(row), nil
		}
	}
	return RelayListener{}, ErrRelayListenerNotFound
}

func (s *relayService) relayMutationAgentIDs(ctx context.Context, ownerAgentID string, listenerID int) ([]string, error) {
	agentIDs := []string{ownerAgentID}
	knownAgentIDs, err := s.allKnownAgentIDs(ctx)
	if err != nil {
		return nil, err
	}
	listeners, err := s.store.ListRelayListeners(ctx, "")
	if err != nil {
		return nil, err
	}
	listenersByID := make(map[int]storage.RelayListenerRow, len(listeners))
	for _, listener := range listeners {
		if listener.ID > 0 {
			listenersByID[listener.ID] = listener
		}
	}
	addRuleGraph := func(ruleAgentID string, layers [][]int) {
		agentIDs = append(agentIDs, ruleAgentID)
		for _, referencedID := range flattenRelayLayers(layers) {
			if listener, ok := listenersByID[referencedID]; ok {
				agentIDs = append(agentIDs, listener.AgentID)
			}
		}
	}
	for _, agentID := range knownAgentIDs {
		httpRules, err := s.store.ListHTTPRules(ctx, agentID)
		if err != nil {
			return nil, err
		}
		for _, row := range httpRules {
			if !row.Enabled {
				continue
			}
			layers := parseIntLayers(row.RelayLayersJSON)
			if containsInt(flattenRelayLayers(layers), listenerID) {
				addRuleGraph(row.AgentID, layers)
			}
		}
		l4Rules, err := s.store.ListL4Rules(ctx, agentID)
		if err != nil {
			return nil, err
		}
		for _, row := range l4Rules {
			if !row.Enabled {
				continue
			}
			layers := parseIntLayers(row.RelayLayersJSON)
			if containsInt(flattenRelayLayers(layers), listenerID) {
				addRuleGraph(row.AgentID, layers)
			}
		}
	}
	return expandConfigDependencyAgentIDs(ctx, s.store, agentIDs)
}

func relayListenerMutationResourceState(ctx context.Context, tx *storage.GormStore, _ revision.Target) (any, error) {
	rows, err := tx.ListRelayListeners(ctx, "")
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].Revision = 0
	}
	return rows, nil
}

func (s *relayService) runAfterRevisionCommit(action func()) {
	if action == nil {
		return
	}
	if s.revisionMutation && s.postCommitActions != nil {
		*s.postCommitActions = append(*s.postCommitActions, action)
		return
	}
	action()
}

func (s *relayService) runAfterRevisionRollback(action func()) {
	if action == nil || !s.revisionMutation || s.rollbackActions == nil {
		return
	}
	*s.rollbackActions = append(*s.rollbackActions, action)
}

func (s *relayService) runAfterRevisionMaterialRollback(actions []func() error) {
	if !s.revisionMutation || s.materialRollbacks == nil {
		return
	}
	*s.materialRollbacks = append(*s.materialRollbacks, actions...)
}

func (s *relayService) durableMaterialStore() storage.Store {
	if s.materialRecoveryStore != nil {
		return s.materialRecoveryStore
	}
	return s.store
}

func runRelayMaterialRollbacks(actions []func() error) error {
	var firstErr error
	for index := len(actions) - 1; index >= 0; index-- {
		if actions[index] == nil {
			continue
		}
		if err := actions[index](); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func relayMaterialRollbackError(mutationErr error, actions []func() error) error {
	if rollbackErr := runRelayMaterialRollbacks(actions); rollbackErr != nil {
		return fmt.Errorf("%w (relay certificate material restore failed: %v)", mutationErr, rollbackErr)
	}
	return mutationErr
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
	if s.revisionMutation {
		return nil
	}
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

	pkiEnabled, err := s.canonicalPKIEnabled(ctx)
	if err != nil {
		return relayPreparation{}, err
	}
	certRows, err := s.store.ListManagedCertificates(ctx)
	if err != nil {
		return relayPreparation{}, err
	}
	originalCertRows := append([]storage.ManagedCertificateRow(nil), certRows...)

	workingInput := input
	if pkiEnabled {
		pkiTLSMode := "pki_mtls"
		emptyPins := []RelayPin{}
		emptyCAIDs := []int{}
		allowSelfSigned := false
		workingInput.CertificateID = nil
		workingInput.HasCertificateID = true
		workingInput.TLSMode = &pkiTLSMode
		workingInput.PinSet = &emptyPins
		workingInput.TrustedCACertificateIDs = &emptyCAIDs
		workingInput.AllowSelfSigned = &allowSelfSigned
	}
	draft, err := normalizeRelayListenerInput(workingInput, fallback, suggestedID, relayNormalizeOptions{
		AllowMissingCertificate: true,
		SkipTrustValidation:     true,
	})
	if err != nil {
		return relayPreparation{}, err
	}
	if pkiEnabled {
		listener, err := normalizeRelayListenerInput(workingInput, fallback, suggestedID, relayNormalizeOptions{
			AllowMissingCertificate: true,
			SkipTrustValidation:     true,
		})
		if err != nil {
			return relayPreparation{}, err
		}
		return relayPreparation{Listener: listener, OriginalCertRows: originalCertRows, NextCertRows: certRows}, nil
	}
	previousUsesAutoCert := relayListenerUsesAutoCertificate(certRows, fallback)
	shouldRotateAutoCert := shouldRotateAutoRelayListenerCertificate(certificateSource, input, fallback, draft, previousUsesAutoCert)
	shouldIssueCert := shouldAutoIssueRelayListenerCertificate(certificateSource, draft, previousUsesAutoCert, shouldRotateAutoCert)
	shouldDeriveTrust := shouldAutoDeriveRelayTrust(trustModeSource, certificateSource, input, draft, fallback, previousUsesAutoCert)

	persistCertificates := false
	materialBundles := make([]storage.ManagedCertificateBundle, 0)
	if shouldIssueCert {
		if previousUsesAutoCert && fallback.CertificateID != nil && !shouldRotateAutoCert {
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

func shouldAutoIssueRelayListenerCertificate(certificateSource string, draft RelayListener, previousUsesAutoCert bool, shouldRotateAutoCert bool) bool {
	if !draft.Enabled {
		return false
	}
	if shouldRotateAutoCert {
		return true
	}
	if certificateSource != "" {
		return certificateSource == "auto_relay_ca" && draft.CertificateID == nil
	}
	return previousUsesAutoCert && draft.CertificateID == nil
}

func shouldRotateAutoRelayListenerCertificate(certificateSource string, input RelayListenerInput, fallback RelayListener, draft RelayListener, previousUsesAutoCert bool) bool {
	if !previousUsesAutoCert || input.hasCertificateIDField() {
		return false
	}
	if certificateSource == "existing_certificate" {
		return false
	}
	return strings.TrimSpace(fallback.PublicHost) != strings.TrimSpace(draft.PublicHost)
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
	if left, right, ok := relayBindHostOverlapWithin(bindHosts); ok {
		return RelayListener{}, newConflictError(
			"bind_hosts %s and %s overlap on the same relay listener",
			left,
			right,
		)
	}

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
	case "pin_only", "ca_only", "pin_or_ca", "pin_and_ca", "pki_mtls":
	default:
		return RelayListener{}, fmt.Errorf("%w: tls_mode must be pin_only, ca_only, pin_or_ca, pin_and_ca, or pki_mtls", ErrInvalidArgument)
	}

	transportMode := strings.ToLower(strings.TrimSpace(pointerString(input.TransportMode)))
	if transportMode == "" {
		transportMode = strings.ToLower(strings.TrimSpace(fallback.TransportMode))
	}
	switch transportMode {
	case "", "tls_tcp":
		transportMode = "tls_tcp"
	case "quic":
	default:
		return RelayListener{}, fmt.Errorf("%w: transport_mode must be tls_tcp or quic", ErrInvalidArgument)
	}
	listenHost = bindHosts[0]

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

type relayMaterialRollbackActions struct {
	immediate []func() error
	recovery  []func() error
}

func (s *relayService) persistManagedCertificateMaterialBundles(ctx context.Context, bundles []storage.ManagedCertificateBundle, originalRows []storage.ManagedCertificateRow, nextRows []storage.ManagedCertificateRow) (relayMaterialRollbackActions, error) {
	rollbacks := relayMaterialRollbackActions{
		immediate: make([]func() error, 0, len(bundles)),
		recovery:  make([]func() error, 0, len(bundles)),
	}
	for _, bundle := range bundles {
		if strings.TrimSpace(bundle.Domain) == "" {
			continue
		}
		writeRestore, recoveryRestore, err := saveManagedCertificateMaterialWithRollbackStores(
			ctx,
			s.store,
			s.durableMaterialStore(),
			bundle.Domain,
			bundle,
		)
		if err != nil {
			cleanupManagedCertificateMaterialBestEffort(ctx, s.store, nextRows, originalRows)
			return relayMaterialRollbackActions{}, relayMaterialRollbackError(err, rollbacks.immediate)
		}
		rollbacks.immediate = append(rollbacks.immediate, writeRestore)
		rollbacks.recovery = append(rollbacks.recovery, recoveryRestore)
	}
	return rollbacks, nil
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
	cert := canonicalizeSystemRelayCACertificate(existing)
	cert.ID = certID
	cert.TargetAgentIDs = targetAgentIDs
	cert.Revision = existing.Revision
	return cert
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
			if relayLayersReferenceListener(row.RelayLayersJSON, listenerID) {
				return &relayRuleReference{AgentID: agentID, RuleID: row.ID, RuleType: "HTTP"}, nil
			}
		}

		l4Rules, err := s.store.ListL4Rules(ctx, agentID)
		if err != nil {
			return nil, err
		}
		for _, row := range l4Rules {
			if relayLayersReferenceListener(row.RelayLayersJSON, listenerID) {
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
	s.runAfterRevisionCommit(func() {
		cleanupManagedCertificateMaterialBestEffort(ctx, s.durableMaterialStore(), certRows, nextRows)
	})
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

func copyOptionalInt(value *int) *int {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func normalizeOptionalPositiveInt(value *int) *int {
	if value == nil || *value <= 0 {
		return nil
	}
	copied := *value
	return &copied
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

func relayBindHostOverlapWithin(hosts []string) (string, string, bool) {
	for index, host := range hosts {
		for _, candidate := range hosts[index+1:] {
			if conflictHost, ok := relayBindHostConflictsWithExisting([]string{host}, []string{candidate}); ok {
				return host, conflictHost, true
			}
		}
	}
	return "", "", false
}

func validateRelayLiveBindingTransition(current, next RelayListener) error {
	if !current.Enabled || !next.Enabled ||
		relayListenStackIdentity(current) != relayListenStackIdentity(next) ||
		current.ListenPort != next.ListenPort {
		return nil
	}
	for _, rawCurrentHost := range current.BindHosts {
		currentHost := strings.TrimSpace(rawCurrentHost)
		for _, rawNextHost := range next.BindHosts {
			nextHost := strings.TrimSpace(rawNextHost)
			if currentHost == "" || nextHost == "" || currentHost == nextHost {
				continue
			}
			if _, ok := relayBindHostConflictsWithExisting([]string{currentHost}, []string{nextHost}); ok {
				if relayLiveBindingTransitionReusable(current, currentHost, nextHost) {
					continue
				}
				return newConflictError(
					"disable relay listener #%d before changing overlapping bind_hosts from %s to %s, then wait for apply to finish",
					current.ID,
					currentHost,
					nextHost,
				)
			}
		}
	}
	return nil
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

func ensureUniqueRelayListen(listeners []RelayListener, next RelayListener, excludeID int) error {
	nextTransport := relayListenStackIdentity(next)
	for _, listener := range listeners {
		if listener.ID == excludeID || !listener.Enabled {
			continue
		}
		if relayListenStackIdentity(listener) != nextTransport || listener.ListenPort != next.ListenPort {
			continue
		}
		if conflictHost, ok := relayBindHostConflictsWithExisting(listener.BindHosts, next.BindHosts); ok {
			return newConflictError(
				"relay listen %s:%d on host %s conflicts with relay listener #%d",
				nextTransport,
				next.ListenPort,
				conflictHost,
				listener.ID,
			)
		}
	}
	return nil
}

func relayLiveBindingTransitionReusable(listener RelayListener, currentHost, nextHost string) bool {
	if relayListenStackIdentity(listener) != "tls_tcp" {
		return false
	}
	switch relayBindHostFamily(currentHost) {
	case relayBindHostIPv4Wildcard:
		return relayBindHostFamily(nextHost) == relayBindHostIPv4
	case relayBindHostIPv6Wildcard:
		return relayBindHostFamily(nextHost) == relayBindHostIPv6
	default:
		return false
	}
}

func relayListenStackIdentity(listener RelayListener) string {
	return normalizeRelayTransportModeIdentity(listener.TransportMode)
}

// Empty transport_mode defaults to "tls_tcp" (the system default when omitted).
func normalizeRelayTransportModeIdentity(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case "", "tls_tcp":
		return "tls_tcp"
	case "quic":
		return "quic"
	default:
		return normalized
	}
}

// relayBindHostConflictsWithExisting checks whether any host in candidate
// overlaps with an existing listener's bind hosts (exact match or same-family
// wildcard overlap).
func relayBindHostConflictsWithExisting(existing []string, candidate []string) (string, bool) {
	for _, existingHost := range existing {
		for _, rawCandidateHost := range candidate {
			candidateHost := strings.TrimSpace(rawCandidateHost)
			if relayBindHostsOverlap(existingHost, candidateHost) {
				return candidateHost, true
			}
		}
	}
	return "", false
}

func relayBindHostsOverlap(left, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" || right == "" {
		return false
	}
	leftNormalized := normalizeRelayBindHost(left)
	rightNormalized := normalizeRelayBindHost(right)
	if leftNormalized == rightNormalized {
		return true
	}
	if (leftNormalized == "localhost" && relayBindHostIsLoopback(rightNormalized)) ||
		(rightNormalized == "localhost" && relayBindHostIsLoopback(leftNormalized)) {
		return true
	}
	leftFamily := relayBindHostFamily(leftNormalized)
	rightFamily := relayBindHostFamily(rightNormalized)
	leftWildcard := leftFamily == relayBindHostIPv4Wildcard || leftFamily == relayBindHostIPv6Wildcard
	rightWildcard := rightFamily == relayBindHostIPv4Wildcard || rightFamily == relayBindHostIPv6Wildcard
	if !leftWildcard && !rightWildcard {
		return false
	}
	if leftFamily == relayBindHostOther || rightFamily == relayBindHostOther {
		return true
	}
	return relayBindHostIsIPv4Family(leftFamily) == relayBindHostIsIPv4Family(rightFamily)
}

func normalizeRelayBindHost(host string) string {
	host = strings.TrimSpace(host)
	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}
	return strings.ToLower(host)
}

func relayBindHostIsLoopback(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func relayBindHostIsIPv4Family(family int) bool {
	return family == relayBindHostIPv4 || family == relayBindHostIPv4Wildcard
}

const (
	relayBindHostOther = iota
	relayBindHostIPv4
	relayBindHostIPv4Wildcard
	relayBindHostIPv6
	relayBindHostIPv6Wildcard
)

func relayBindHostFamily(host string) int {
	ip := net.ParseIP(strings.TrimSpace(host))
	if ip == nil {
		return relayBindHostOther
	}
	if ip4 := ip.To4(); ip4 != nil {
		if ip4.Equal(net.IPv4zero) {
			return relayBindHostIPv4Wildcard
		}
		return relayBindHostIPv4
	}
	if ip.Equal(net.IPv6zero) {
		return relayBindHostIPv6Wildcard
	}
	return relayBindHostIPv6
}

func relayListenerFromRow(row storage.RelayListenerRow) RelayListener {
	listener := RelayListener{
		ID:            row.ID,
		AgentID:       row.AgentID,
		Name:          row.Name,
		ListenHost:    defaultString(row.ListenHost, "0.0.0.0"),
		ListenPort:    row.ListenPort,
		PublicHost:    defaultString(row.PublicHost, row.ListenHost),
		PublicPort:    row.PublicPort,
		Enabled:       row.Enabled,
		CertificateID: row.CertificateID,
		TLSMode:       defaultString(row.TLSMode, "pin_or_ca"),
		TransportMode: defaultString(row.TransportMode, "tls_tcp"),

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

func relayListenerRowSupported(row storage.RelayListenerRow) bool {
	switch normalizeRelayTransportModeIdentity(row.TransportMode) {
	case "tls_tcp", "quic":
		return true
	default:
		return false
	}
}

func relayListenerToRow(listener RelayListener) storage.RelayListenerRow {
	return storage.RelayListenerRow{
		ID:            listener.ID,
		AgentID:       listener.AgentID,
		Name:          listener.Name,
		BindHostsJSON: marshalJSON(listener.BindHosts, "[]"),
		ListenHost:    listener.ListenHost,
		ListenPort:    listener.ListenPort,
		PublicHost:    listener.PublicHost,
		PublicPort:    listener.PublicPort,
		Enabled:       listener.Enabled,
		CertificateID: listener.CertificateID,
		TLSMode:       listener.TLSMode,
		TransportMode: listener.TransportMode,

		AllowTransportFallback:  listener.AllowTransportFallback,
		ObfsMode:                listener.ObfsMode,
		PinSetJSON:              marshalJSON(listener.PinSet, "[]"),
		TrustedCACertificateIDs: marshalJSON(listener.TrustedCACertificateIDs, "[]"),
		AllowSelfSigned:         listener.AllowSelfSigned,
		TagsJSON:                marshalJSON(listener.Tags, "[]"),
		Revision:                listener.Revision,
	}
}
