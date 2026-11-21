package storage

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const ListenerCertificatePlugin = "shadowsocks-server"
const ListenerStateKey = "listens"

// ListenerCertificateRef is host-owned authority for a single listener.
type ListenerCertificateRef struct {
	ID            string `json:"id"`
	AgentID       string `json:"agent_id"`
	CertificateID int    `json:"certificate_id"`
	Domain        string `json:"certificate_domain"`
}

type ListenerCertificateBinding struct {
	ListenerCertificateRef
	Revision int64 `json:"revision"`
	Retired  bool  `json:"retired,omitempty"`
}
type listenerCertificateBindings struct {
	IncarnationID string                                `json:"incarnation_id"`
	Listeners     map[string]ListenerCertificateBinding `json:"listeners"`
}

func eligibleListenerCertificate(row ManagedCertificateRow) bool {
	return row.Enabled && !internalPKIManagedCertificate(row) &&
		strings.EqualFold(defaultString(row.Usage, "https"), "https") &&
		(row.CertificateType == "" || row.CertificateType == "acme" || row.CertificateType == "uploaded")
}

// ListenerServerLeafMaterial accepts matching TLS server end-entity keys, including self-signed leaves.
func ListenerServerLeafMaterial(bundle ManagedCertificateBundle) bool {
	// tls.X509KeyPair ignores trailing PEM blocks. Only distribute a certificate
	// chain and exactly one key, so unrelated signing keys cannot ride along.
	publicPEM := bytes.TrimSpace([]byte(bundle.CertPEM))
	for len(publicPEM) > 0 {
		if !bytes.HasPrefix(publicPEM, []byte("-----BEGIN CERTIFICATE-----")) {
			return false
		}
		block, rest := pem.Decode(publicPEM)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return false
		}
		publicPEM = bytes.TrimSpace(rest)
	}
	privatePEM := bytes.TrimSpace([]byte(bundle.KeyPEM))
	if !bytes.HasPrefix(privatePEM, []byte("-----BEGIN ")) {
		return false
	}
	key, rest := pem.Decode(privatePEM)
	if key == nil || len(key.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 {
		return false
	}
	pair, err := tls.X509KeyPair([]byte(bundle.CertPEM), []byte(bundle.KeyPEM))
	if err != nil || len(pair.Certificate) == 0 {
		return false
	}
	leaf := pair.Leaf
	if leaf == nil {
		leaf, err = x509.ParseCertificate(pair.Certificate[0])
		if err != nil {
			return false
		}
	}
	domain := strings.TrimSuffix(strings.TrimSpace(bundle.Domain), ".")
	if domain == "" {
		return false
	}
	if strings.HasPrefix(domain, "*.") {
		// Every concrete share host chosen beneath a wildcard must be covered.
		// A single literal SAN such as anytls.example is insufficient.
		wildcard := false
		for _, san := range leaf.DNSNames {
			if strings.EqualFold(domain, strings.TrimSuffix(san, ".")) {
				wildcard = true
				break
			}
		}
		if !wildcard {
			return false
		}
		domain = "anytls." + strings.TrimPrefix(domain, "*.")
	}
	now := time.Now()
	if now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) || leaf.VerifyHostname(domain) != nil {
		return false
	}
	if leaf.IsCA {
		return false
	}
	if len(leaf.ExtKeyUsage) == 0 {
		return len(leaf.UnknownExtKeyUsage) == 0
	}
	for _, usage := range leaf.ExtKeyUsage {
		if usage == x509.ExtKeyUsageServerAuth || usage == x509.ExtKeyUsageAny {
			return true
		}
	}
	return false
}

// ListenerCertificateCatalog checks both runtime ownership and certificate resource
// group membership. Reading this projection never writes a binding.
func (s *GormStore) ListenerCertificateCatalog(ctx context.Context, instanceID, agentID string, certID int) ([]PublicCertificateProjection, error) {
	instance, found, err := s.GetPluginInstance(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	if !found || instance.PluginID != ListenerCertificatePlugin || !instance.DesiredEnabled {
		return nil, ErrPluginConflict
	}
	agentGroup, err := s.ResolveResourceGroupID(ctx, "agent", agentID)
	if err != nil {
		return nil, err
	}
	if agentGroup != instance.ResourceGroupID || !containsString(parseStringSlice(instance.TargetJSON), agentID) {
		return nil, ErrPluginConflict
	}
	var exists int64
	if err := s.db.WithContext(ctx).Model(&AgentRow{}).Where("id = ?", agentID).Count(&exists).Error; err != nil {
		return nil, err
	}
	if exists == 0 {
		return nil, ErrPluginConflict
	}
	rows, err := s.ListManagedCertificates(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]PublicCertificateProjection, 0)
	for _, row := range rows {
		if (certID > 0 && row.ID != certID) || !eligibleListenerCertificate(row) {
			continue
		}
		group, err := s.ResolveResourceGroupID(ctx, "certificate", strconv.Itoa(row.ID))
		if err != nil {
			return nil, err
		}
		if group != agentGroup {
			continue
		}
		bundle, ok, err := s.LoadManagedCertificateMaterial(ctx, row.Domain)
		if err != nil {
			return nil, err
		}
		if ok && ListenerServerLeafMaterial(bundle) {
			result = append(result, PublicCertificateProjection{ID: row.ID, Domain: row.Domain, Enabled: true, MaterialAvailable: true})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func decodeListenerBindings(raw, incarnation string) (listenerCertificateBindings, error) {
	var set listenerCertificateBindings
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &set); err != nil {
			return set, err
		}
	}
	if set.IncarnationID != incarnation || set.Listeners == nil {
		set = listenerCertificateBindings{IncarnationID: incarnation, Listeners: map[string]ListenerCertificateBinding{}}
	}
	return set, nil
}

func (s *GormStore) ListenerCertificateBinding(ctx context.Context, instanceID, listenerID string) (ListenerCertificateBinding, bool, error) {
	var r struct{ CertificateBindings, IncarnationID string }
	q := s.db.WithContext(ctx).Table("plugin_runtime_state AS p").Select("p.certificate_bindings, i.incarnation_id").
		Joins("JOIN plugin_instances AS i ON i.id = p.instance_id AND i.plugin_id = p.plugin_id AND i.resource_group_id = p.resource_group_id").
		Where("p.instance_id = ? AND p.plugin_id = ? AND p.key = ?", instanceID, ListenerCertificatePlugin, ListenerStateKey).Limit(1).Find(&r)
	if q.Error != nil {
		return ListenerCertificateBinding{}, false, q.Error
	}
	set, err := decodeListenerBindings(r.CertificateBindings, r.IncarnationID)
	ref, ok := set.Listeners[listenerID]
	return ref, ok, err
}

// StorePluginListeners changes the directory and the host-owned bindings together.
// An ordinary guest state write can preserve a binding, but it cannot change
// its certificate audience. A request-authorized grant permits exactly one ref.
func (s *GormStore) StorePluginListeners(ctx context.Context, row PluginRuntimeStateRow, permit *ListenerCertificateBinding, revision int64) error {
	if row.PluginID != ListenerCertificatePlugin || row.Key != ListenerStateKey || row.InstanceID == "" || row.ResourceGroupID == "" || len(row.Value) > 1<<20 {
		return ErrPluginConflict
	}
	var listens []struct {
		ListenerCertificateRef
		Protocol string `json:"protocol"`
	}
	if err := json.Unmarshal(row.Value, &listens); err != nil {
		return err
	}
	byID := make(map[string]ListenerCertificateRef)
	for _, l := range listens {
		if l.Protocol == "anytls" {
			if _, dup := byID[l.ID]; dup || l.ID == "" || l.AgentID == "" || l.CertificateID <= 0 || l.Domain == "" {
				return ErrPluginConflict
			}
			byID[l.ID] = l.ListenerCertificateRef
		}
	}
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var instance PluginInstanceRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND plugin_id = ? AND resource_group_id = ?", row.InstanceID, row.PluginID, row.ResourceGroupID).Take(&instance).Error; err != nil {
			return err
		}
		var current PluginRuntimeStateRow
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("instance_id = ? AND key = ?", row.InstanceID, row.Key).Take(&current).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		set, err := decodeListenerBindings(current.CertificateBindings, instance.IncarnationID)
		if err != nil {
			return err
		}
		for id, bound := range set.Listeners {
			if next, ok := byID[id]; ok && next == bound.ListenerCertificateRef {
				continue
			}
			if permit == nil || permit.ListenerCertificateRef != bound.ListenerCertificateRef {
				return ErrPluginConflict
			}
			delete(set.Listeners, id)
		}
		if permit != nil {
			if next, ok := byID[permit.ID]; ok {
				if next != permit.ListenerCertificateRef || revision <= 0 || !instance.DesiredEnabled || !containsString(parseStringSlice(instance.TargetJSON), next.AgentID) {
					return ErrPluginConflict
				}
				if permit.Retired {
					set.Listeners[next.ID] = *permit
				} else if _, exists := set.Listeners[next.ID]; !exists {
					var cert ManagedCertificateRow
					if err := tx.Where("id = ? AND domain = ?", next.CertificateID, next.Domain).Take(&cert).Error; err != nil {
						return err
					}
					if !eligibleListenerCertificate(cert) {
						return ErrPluginConflict
					}
					allowed, err := listenerAudienceAllowed(tx, row.ResourceGroupID, next)
					if err != nil {
						return err
					}
					if !allowed {
						return ErrPluginConflict
					}
					bundles, err := s.transactionView(tx).snapshotCertificateBundles(ctx, []ManagedCertificateRow{cert})
					if err != nil {
						return err
					}
					if len(bundles) != 1 || !ListenerServerLeafMaterial(bundles[0]) {
						return ErrPluginConflict
					}
					set.Listeners[next.ID] = ListenerCertificateBinding{ListenerCertificateRef: next, Revision: revision}
				}
			}
		}
		if len(set.Listeners) > 0 {
			encoded, err := json.Marshal(set)
			if err != nil {
				return err
			}
			row.CertificateBindings = string(encoded)
		} else {
			row.CertificateBindings = ""
		}
		row.UpdatedAt = time.Now().UTC()
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "instance_id"}, {Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{"plugin_id", "resource_group_id", "value", "certificate_bindings", "updated_at"})}).Create(&row).Error
	})
}

// Check the active package grants again at snapshot time. Replacing grants or moving
// either resource out of this group ends that package's certificate audience.
func listenerAudienceAllowed(tx *gorm.DB, group string, ref ListenerCertificateRef) (bool, error) {
	var permissions int64
	err := tx.Table("plugin_grants AS g").
		Joins("JOIN installed_plugins AS p ON p.plugin_id = g.plugin_id AND p.active_package_identity = g.package_identity AND p.active_package_digest = g.package_digest").
		Where("p.plugin_id = ? AND p.desired_lifecycle = ? AND g.permission IN ? AND (g.resource_selector = '' OR g.resource_selector = ?)", ListenerCertificatePlugin, "enabled", []string{pluginsdk.PermissionPublicCertificateRead, pluginsdk.PermissionPublicCertificateTerminate}, ref.AgentID).
		Distinct("g.permission").Count(&permissions).Error
	if err != nil || permissions != 2 {
		return false, err
	}
	var owners []ResourceBindingRow
	err = tx.Select("resource_group_id").Where("(resource_kind = ? AND resource_id = ?) OR (resource_kind = ? AND resource_id = ?)", "agent", ref.AgentID, "certificate", strconv.Itoa(ref.CertificateID)).Find(&owners).Error
	if err != nil {
		return false, err
	}
	for _, owner := range owners {
		if owner.ResourceGroupID != group {
			return false, nil
		}
	}
	return len(owners) == 2 || group == defaultResourceGroupID, nil
}

// Active bindings are scoped to the exact instance incarnation, agent target,
// and the resource group which currently owns both the agent and certificate.
func (s *GormStore) listenerCertificateRefs(ctx context.Context, agentID string, certID int) ([]ListenerCertificateRef, error) {
	var state []struct{ CertificateBindings, IncarnationID, TargetJSON, ResourceGroupID string }
	q := s.db.WithContext(ctx).Table("plugin_runtime_state AS p").
		Select("p.certificate_bindings, i.incarnation_id, i.target_json, i.resource_group_id").
		Joins("JOIN plugin_instances AS i ON i.id = p.instance_id AND i.plugin_id = p.plugin_id AND i.resource_group_id = p.resource_group_id").
		Joins("JOIN installed_plugins AS a ON a.plugin_id = p.plugin_id AND a.desired_lifecycle = ?", "enabled").
		Where("p.plugin_id = ? AND p.key = ? AND p.certificate_bindings <> '' AND i.desired_enabled = ?", ListenerCertificatePlugin, ListenerStateKey, true)
	if err := q.Find(&state).Error; err != nil {
		return nil, err
	}
	var refs []ListenerCertificateRef
	for _, r := range state {
		set, err := decodeListenerBindings(r.CertificateBindings, r.IncarnationID)
		if err != nil {
			return nil, err
		}
		targets := parseStringSlice(r.TargetJSON)
		for _, b := range set.Listeners {
			if b.Retired || (agentID != "" && agentID != b.AgentID) || (certID > 0 && b.CertificateID != certID) || !containsString(targets, b.AgentID) {
				continue
			}
			allowed, err := listenerAudienceAllowed(s.db.WithContext(ctx), r.ResourceGroupID, b.ListenerCertificateRef)
			if err != nil {
				return nil, err
			}
			if allowed {
				refs = append(refs, b.ListenerCertificateRef)
			}
		}
	}
	return refs, nil
}

func (s *GormStore) ListenerCertificateAgentIDs(ctx context.Context, certID int, domain string) ([]string, error) {
	refs, err := s.listenerCertificateRefs(ctx, "", certID)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, ref := range refs {
		if ref.Domain == domain {
			ids = append(ids, ref.AgentID)
		}
	}
	return uniqueNonEmptyStrings(ids), nil
}

// Only append newly authorized bundles after checking their actual key pair.
func (s *GormStore) listenerOnlyCertificates(ctx context.Context, rows, existing []ManagedCertificateRow, agentID string) ([]ManagedCertificateRow, []ManagedCertificateBundle, error) {
	if len(rows) == 0 {
		return nil, nil, nil
	}
	refs, err := s.listenerCertificateRefs(ctx, agentID, 0)
	if err != nil {
		return nil, nil, err
	}
	if len(refs) == 0 {
		return nil, nil, nil
	}
	ids := make(map[int]string, len(refs))
	for _, ref := range refs {
		ids[ref.CertificateID] = ref.Domain
	}
	for _, row := range existing {
		delete(ids, row.ID)
	}
	var added []ManagedCertificateRow
	var materials []ManagedCertificateBundle
	for _, row := range rows {
		if domain, ok := ids[row.ID]; !ok || domain != row.Domain || !eligibleListenerCertificate(row) {
			continue
		}
		bundles, err := s.snapshotCertificateBundles(ctx, []ManagedCertificateRow{row})
		if err != nil {
			return nil, nil, err
		}
		if len(bundles) == 1 && ListenerServerLeafMaterial(bundles[0]) {
			// This node installs the selected material. Issuance stays with the original certificate owner.
			row.IssuerMode = "master_cf_dns"
			bundles[0].ListenerBound = true
			added = append(added, row)
			materials = append(materials, bundles[0])
		}
	}
	return added, materials, nil
}
func (s *GormStore) ListenerCertificateReady(ctx context.Context, instance, id string) (bool, bool, error) {
	ref, found, err := s.ListenerCertificateBinding(ctx, instance, id)
	if err != nil || !found || ref.Retired {
		return false, found, err
	}
	refs, err := s.listenerCertificateRefs(ctx, ref.AgentID, ref.CertificateID)
	if err != nil {
		return false, true, err
	}
	current := false
	for _, active := range refs {
		if active == ref.ListenerCertificateRef {
			current = true
			break
		}
	}
	if !current {
		return false, true, nil
	}
	pointer, ok, err := s.GetAgentRevisionPointer(ctx, ref.AgentID)
	if err != nil || !ok || ref.Revision <= 0 || pointer.AppliedRevision < ref.Revision {
		return false, true, err
	}
	var cert ManagedCertificateRow
	q := s.db.WithContext(ctx).Where("id = ? AND domain = ?", ref.CertificateID, ref.Domain).Limit(1).Find(&cert)
	if q.Error != nil || q.RowsAffected == 0 || !eligibleListenerCertificate(cert) {
		return false, true, q.Error
	}
	bundle, ok, err := s.LoadManagedCertificateMaterial(ctx, cert.Domain)
	if err != nil {
		return false, true, err
	}
	if isMasterIssuedCertificateMode(cert.IssuerMode) {
		pending, found, err := s.LoadPendingManagedCertificateGeneration(ctx, cert.Domain)
		if err != nil {
			return false, true, err
		}
		if found && ListenerServerLeafMaterial(pending.Material) {
			bundle = pending.Material
			ok = true
		}
	}
	if !ok || !ListenerServerLeafMaterial(bundle) {
		return false, true, nil
	}
	// A higher applied revision can be a rollback. Check the acknowledged immutable snapshot itself.
	var artifact struct{ Payload []byte }
	q = s.db.WithContext(ctx).Table("agent_revisions AS r").Select("a.payload").
		Joins("JOIN generation_artifacts AS a ON a.id = r.snapshot_artifact_id AND a.sha256 = r.snapshot_digest").
		Where("r.agent_id = ? AND r.revision = ? AND r.state = ? AND a.kind = ?", ref.AgentID, pointer.AppliedRevision, AgentRevisionStateApplied, "agent_snapshot").Limit(1).Find(&artifact)
	if q.Error != nil || q.RowsAffected == 0 {
		return false, true, q.Error
	}
	var snap struct {
		Certificates []ManagedCertificateBundle `json:"certificates"`
	}
	if err := json.Unmarshal(artifact.Payload, &snap); err != nil {
		return false, true, err
	}
	for _, material := range snap.Certificates {
		if material.ID == ref.CertificateID && material.Domain == ref.Domain && material.CertPEM == bundle.CertPEM && material.KeyPEM == bundle.KeyPEM {
			return true, true, nil
		}
	}
	return false, true, nil
}

// Forget disabled or untargeted bindings too when a certificate identity is
// retired. A future certificate reusing its ID and domain has a new audience.
func retireListenerCertificates(tx *gorm.DB, previous, next []ManagedCertificateRow, removedAgent string) error {
	retired := make(map[int]string, len(previous))
	for _, cert := range previous {
		retired[cert.ID] = cert.Domain
	}
	for _, cert := range next {
		if retired[cert.ID] == cert.Domain {
			delete(retired, cert.ID)
		}
	}
	if len(retired) == 0 && removedAgent == "" {
		return nil
	}
	var states []PluginRuntimeStateRow
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("instance_id", "certificate_bindings").Where("plugin_id = ? AND key = ? AND certificate_bindings <> ''", ListenerCertificatePlugin, ListenerStateKey).Find(&states).Error; err != nil {
		return err
	}
	for _, state := range states {
		var set listenerCertificateBindings
		if err := json.Unmarshal([]byte(state.CertificateBindings), &set); err != nil {
			return err
		}
		changed := false
		for id, ref := range set.Listeners {
			if domain, ok := retired[ref.CertificateID]; (ok && domain == ref.Domain) || (removedAgent != "" && ref.AgentID == removedAgent) {
				ref.Retired = true
				set.Listeners[id] = ref
				changed = true
			}
		}
		if !changed {
			continue
		}
		updated := ""
		if len(set.Listeners) > 0 {
			encoded, err := json.Marshal(set)
			if err != nil {
				return err
			}
			updated = string(encoded)
		}
		if err := tx.Model(&PluginRuntimeStateRow{}).Where("instance_id = ? AND key = ?", state.InstanceID, ListenerStateKey).Update("certificate_bindings", updated).Error; err != nil {
			return err
		}
	}
	return nil
}

// CertificateMaterialAudience resolves durable consumers. The revision coordinator's
// notification or fallback targets carry no authority to receive pending key material.
func (s *GormStore) CertificateMaterialAudience(ctx context.Context, agent string, id int, domain string) (ordinary bool, listener bool, err error) {
	rows, err := s.listManagedCertificatesForAgent(ctx, agent, false)
	if err != nil {
		return false, false, err
	}
	for _, row := range rows {
		if row.ID == id && row.Domain == domain {
			return true, false, nil
		}
	}
	refs, err := s.listenerCertificateRefs(ctx, agent, id)
	if err != nil {
		return false, false, err
	}
	for _, ref := range refs {
		if ref.Domain == domain {
			return false, true, nil
		}
	}
	return false, false, nil
}

var ErrListenerCertificateOwnership = errors.New("remove the AnyTLS listener before moving its certificate or agent to another resource group")

func rejectListenerOwnershipChange(tx *gorm.DB, targets map[string]ResourceBindingRow) error {
	var states []struct{ CertificateBindings, IncarnationID, ResourceGroupID string }
	q := tx.Table("plugin_runtime_state AS p").Select("p.certificate_bindings, i.incarnation_id, i.resource_group_id").
		Joins("JOIN plugin_instances AS i ON i.id = p.instance_id AND i.plugin_id = p.plugin_id AND i.resource_group_id = p.resource_group_id").
		Where("p.plugin_id = ? AND p.key = ? AND p.certificate_bindings <> ''", ListenerCertificatePlugin, ListenerStateKey).Find(&states)
	if q.Error != nil {
		return q.Error
	}
	for _, state := range states {
		set, err := decodeListenerBindings(state.CertificateBindings, state.IncarnationID)
		if err != nil {
			return err
		}
		for _, bound := range set.Listeners {
			if bound.Retired {
				continue
			}
			for _, key := range []string{"agent\x00" + bound.AgentID, "certificate\x00" + strconv.Itoa(bound.CertificateID)} {
				if target, ok := targets[key]; ok && target.ResourceGroupID != state.ResourceGroupID {
					return ErrListenerCertificateOwnership
				}
			}
		}
	}
	return nil
}

// An immutable rollback snapshot cannot restore a retired listener's key or a
// superseded leaf just because the node once acknowledged that snapshot.
func (s *GormStore) validateRollbackListenerCertificates(ctx context.Context, agent string, certificates []ManagedCertificateBundle) error {
	for _, historical := range certificates {
		if !historical.ListenerBound {
			continue
		}
		ordinary, listener, err := s.CertificateMaterialAudience(ctx, agent, historical.ID, historical.Domain)
		if err != nil {
			return err
		}
		if !ordinary && !listener {
			return coordinatorStateConflict("rollback listener certificate audience has changed")
		}
		var row ManagedCertificateRow
		q := s.db.WithContext(ctx).Where("id = ? AND domain = ?", historical.ID, historical.Domain).Limit(1).Find(&row)
		if q.Error != nil {
			return q.Error
		}
		if q.RowsAffected == 0 || !eligibleListenerCertificate(row) || !ListenerServerLeafMaterial(historical) {
			return coordinatorStateConflict("rollback listener certificate is unavailable")
		}
		bundles, err := s.snapshotCertificateBundles(ctx, []ManagedCertificateRow{row})
		if err != nil {
			return err
		}
		if len(bundles) == 1 && bundles[0].CertPEM == historical.CertPEM && bundles[0].KeyPEM == historical.KeyPEM {
			continue
		}
		pending, found, err := s.LoadPendingManagedCertificateGeneration(ctx, historical.Domain)
		if err != nil {
			return err
		}
		if !found || historical.CertPEM != pending.Material.CertPEM || historical.KeyPEM != pending.Material.KeyPEM {
			return coordinatorStateConflict("rollback listener certificate material has changed")
		}
	}
	return nil
}
