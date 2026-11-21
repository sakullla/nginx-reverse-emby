package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/authz"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/revision"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

const listenerSecretVersionPrefix = "listener-secret-version/"
const listenerCertificateReadyPrefix = "listener-certificate-ready/"

type listenerCertificateTargetStore interface {
	ListenerCertificateAgentIDs(context.Context, int, string) ([]string, error)
}

type listenerCertificateStore interface {
	ListenerCertificateCatalog(context.Context, string, string, int) ([]storage.PublicCertificateProjection, error)
	ListenerCertificateBinding(context.Context, string, string) (storage.ListenerCertificateBinding, bool, error)
	ListenerCertificateReady(context.Context, string, string) (bool, bool, error)
}

type listenerGrant struct {
	instance, generation string
	ref                  storage.ListenerCertificateRef
	actor                authz.Actor
	retired              bool
	ctx                  context.Context
}

func (m *PluginCapabilityManager) listenerAuthority(ctx context.Context, actor authz.Actor, instance, generation, agent string, write bool, certID int) error {
	if actor.ID == "" || m.store == nil || m.runtime == nil || m.resourceAuthorizer == nil {
		return authz.ErrForbidden
	}
	if active, ok := m.runtime.ActiveGeneration(instance); !ok || active != generation {
		return storage.ErrPluginConflict
	}
	installed, found, err := m.store.GetInstalledPlugin(ctx, storage.ListenerCertificatePlugin)
	if err != nil {
		return err
	}
	if !found || installed.DesiredLifecycle != "enabled" {
		return storage.ErrPluginConflict
	}
	grants, err := m.store.ListPluginGrants(ctx, storage.ListenerCertificatePlugin)
	if err != nil {
		return err
	}
	for _, permission := range []string{pluginsdk.PermissionPublicCertificateRead, pluginsdk.PermissionPublicCertificateTerminate} {
		if !hasExactCapabilityGrant(grants, installed.PluginID, installed.ActivePackageDigest, installed.ActivePackageIdentity, pluginsdk.HostCapability(permission), agent) {
			return authz.ErrForbidden
		}
	}
	permission := authz.PermissionResourceRead
	if write {
		permission = authz.PermissionResourceWrite
	}
	if actor.Bootstrap {
		return nil
	}
	for _, r := range [][2]string{{"plugin_instance", instance}, {"agent", agent}} {
		if err := m.resourceAuthorizer.AuthorizeResource(ctx, actor, permission, r[0], r[1]); err != nil {
			return err
		}
	}
	if certID > 0 {
		return m.resourceAuthorizer.AuthorizeResource(ctx, actor, permission, "certificate", strconv.Itoa(certID))
	}
	return nil
}

func (m *PluginCapabilityManager) ListenerCertificateCatalog(ctx context.Context, actor authz.Actor, instance, generation, agent string) ([]storage.PublicCertificateProjection, error) {
	if err := m.listenerAuthority(ctx, actor, instance, generation, agent, false, 0); err != nil {
		return nil, err
	}
	store, ok := m.store.(listenerCertificateStore)
	if !ok {
		return nil, errPluginHostUnavailable
	}
	rows, err := store.ListenerCertificateCatalog(ctx, instance, agent, 0)
	if err != nil {
		return nil, err
	}
	result := make([]storage.PublicCertificateProjection, 0, len(rows))
	for _, row := range rows {
		if !actor.Bootstrap {
			err = m.resourceAuthorizer.AuthorizeResource(ctx, actor, authz.PermissionResourceRead, "certificate", strconv.Itoa(row.ID))
			if errors.Is(err, authz.ErrForbidden) {
				continue
			}
			if err != nil {
				return nil, err
			}
		}
		result = append(result, row)
	}
	return result, nil
}

// BeginListenerCertificateGrant scopes a single listener binding to one authenticated UI request.
func (m *PluginCapabilityManager) BeginListenerCertificateGrant(ctx context.Context, actor authz.Actor, instance, generation, agent, deleteID string, certID int) (string, storage.ListenerCertificateRef, func(), error) {
	var ref storage.ListenerCertificateRef
	store, ok := m.store.(listenerCertificateStore)
	if !ok {
		return "", ref, nil, errPluginHostUnavailable
	}
	retired := false
	if deleteID != "" {
		bound, found, err := store.ListenerCertificateBinding(ctx, instance, deleteID)
		if err != nil || !found {
			return "", ref, nil, err
		}
		ref = bound.ListenerCertificateRef
		retired = bound.Retired
		if agent != "" && ref.AgentID != agent {
			return "", ref, nil, authz.ErrForbidden
		}
	} else {
		ref.AgentID = agent
		ref.CertificateID = certID
	}
	if err := m.listenerAuthority(ctx, actor, instance, generation, ref.AgentID, true, ref.CertificateID); err != nil {
		return "", ref, nil, err
	}
	if deleteID == "" {
		rows, err := store.ListenerCertificateCatalog(ctx, instance, agent, certID)
		if err != nil {
			return "", ref, nil, err
		}
		if certID <= 0 || len(rows) != 1 || rows[0].ID != certID {
			return "", ref, nil, authz.ErrForbidden
		}
		var id [12]byte
		rand.Read(id[:])
		ref.ID = "listen-" + hex.EncodeToString(id[:])
		ref.Domain = rows[0].Domain
	}
	lifetime, cancel := context.WithCancel(ctx)
	token := rand.Text()
	m.listenerGrants.Store(token, listenerGrant{instance: instance, generation: generation, ref: ref, actor: actor, ctx: lifetime, retired: retired})
	return token, ref, func() { cancel(); m.listenerGrants.Delete(token) }, nil
}

func (m *PluginCapabilityManager) storeGrantedListeners(ctx context.Context, candidate pluginhost.Candidate, row storage.PluginRuntimeStateRow, token string) error {
	raw, ok := m.listenerGrants.Load(token)
	if !ok || row.Key != storage.ListenerStateKey || row.PluginID != storage.ListenerCertificatePlugin {
		return errPluginHostDenied
	}
	grant := raw.(listenerGrant)
	if grant.ctx.Err() != nil || candidate.InstanceID != grant.instance || candidate.Identity.Generation != grant.generation {
		return errPluginHostDenied
	}
	if err := m.listenerAuthority(ctx, grant.actor, grant.instance, grant.generation, grant.ref.AgentID, true, grant.ref.CertificateID); err != nil {
		return errPluginHostDenied
	}
	if m.plugins == nil || m.plugins.mutationExecutor == nil {
		return errPluginHostUnavailable
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(grant.ctx, cancel)
	defer stop()
	if actor, ok := storage.QuotaActorFromContext(grant.ctx); ok {
		ctx = storage.WithQuotaActor(ctx, actor)
	}
	ctx = WithResourceAuthorizer(ctx, func(check context.Context, kind, id string) error {
		if grant.actor.Bootstrap {
			return nil
		}
		return m.resourceAuthorizer.AuthorizeResource(check, grant.actor, authz.PermissionResourceWrite, kind, id)
	})
	digest := sha256.Sum256(row.Value)
	_, err := m.plugins.mutationExecutor.Execute(ctx, revision.MutationRequest{
		Kind: "plugin.listener.certificate",
		Request: struct {
			Ref    storage.ListenerCertificateRef
			Digest string
		}{grant.ref, hex.EncodeToString(digest[:])},
		Targets: configMutationTargets(m.plugins.cfg, []string{grant.ref.AgentID}, nil),
		ResourceState: func(ctx context.Context, tx *storage.GormStore, _ revision.Target) (any, error) {
			binding, found, err := tx.ListenerCertificateBinding(ctx, grant.instance, grant.ref.ID)
			if !found {
				return nil, err
			}
			return binding.ListenerCertificateRef, err
		},
		Mutate: func(ctx context.Context, tx *storage.GormStore, revs map[string]int64) error {
			return tx.StorePluginListeners(ctx, row, &storage.ListenerCertificateBinding{ListenerCertificateRef: grant.ref, Retired: grant.retired}, revs[grant.ref.AgentID])
		},
		BeforeCommit: func(ctx context.Context, _ *storage.GormStore) error {
			active, ok := m.runtime.ActiveGeneration(grant.instance)
			if !ok || active != grant.generation || grant.ctx.Err() != nil {
				return errPluginHostDenied
			}
			return ctx.Err()
		},
	})
	return err
}

// A failed create response may follow a successful vault commit. This lookup
// identifies that exact scoped reference without resolving or delivering bytes.
func (m *PluginCapabilityManager) listenerScopedSecretVersion(ctx context.Context, candidate pluginhost.Candidate, id string) (map[string]any, error) {
	const scope = "shadowsocks-inbound"
	if candidate.Identity.PluginID != storage.ListenerCertificatePlugin || candidate.ResourceGroupID == "" || pluginsdk.ValidatePolicyIdentity(id) != nil || !pluginCandidateHasGrant(candidate, pluginsdk.PermissionScopedSecretRead) || !containsPluginString(pluginScopedSecretScopes(candidate, pluginsdk.PermissionScopedSecretRead), scope) {
		return nil, errPluginHostDenied
	}
	if m.runtime == nil {
		return nil, errPluginHostDenied
	}
	if active, ok := m.runtime.ActiveGeneration(candidate.InstanceID); !ok || active != candidate.Identity.Generation {
		return nil, errPluginHostDenied
	}
	transactions, ok := m.store.(interface {
		SecurityTransaction(context.Context, func(*storage.GormStore) error) error
	})
	if !ok {
		return nil, errPluginHostUnavailable
	}
	name := pluginScopedSecretName(candidate, pluginsdk.ScopedSecretReference{InstanceID: candidate.InstanceID, ID: id, Scope: scope})
	var version string
	err := transactions.SecurityTransaction(ctx, func(tx *storage.GormStore) error {
		instance, found, err := tx.GetPluginInstance(ctx, candidate.InstanceID)
		if err != nil {
			return err
		}
		if !found || !instance.DesiredEnabled || candidate.Identity.PluginID != instance.PluginID || candidate.ResourceGroupID != instance.ResourceGroupID || instance.IncarnationID != candidate.IncarnationID {
			return errPluginHostDenied
		}
		installed, found, err := tx.GetInstalledPlugin(ctx, instance.PluginID)
		if err != nil {
			return err
		}
		if !found || installed.DesiredLifecycle != "enabled" {
			return errPluginHostDenied
		}
		grants, err := tx.ListPluginGrants(ctx, instance.PluginID)
		if err != nil {
			return err
		}
		authorized := false
		for _, g := range grants {
			if g.PluginID == instance.PluginID && g.PackageDigest == installed.ActivePackageDigest && g.PackageIdentity == installed.ActivePackageIdentity && g.Permission == pluginsdk.PermissionScopedSecretRead && (g.ResourceSelector == scope || g.ResourceSelector == "secret-scope:"+scope) {
				authorized = true
				break
			}
		}
		if !authorized {
			return errPluginHostDenied
		}
		blocked, err := tx.ScopedSecretReadBlocked(ctx, name, "", candidate.InstanceID, candidate.Identity.Generation)
		if err != nil {
			return err
		}
		if blocked {
			return errPluginHostDenied
		}
		row, found, err := tx.GetSecretByName(ctx, name)
		if err != nil || !found {
			return err
		}
		if row.Purpose != pluginScopedSecretPurpose || row.ResourceGroupID != candidate.ResourceGroupID {
			return errPluginHostDenied
		}
		if row.RetiredAt == nil {
			version = pluginScopedSecretVersion(row)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"found": true, "value": version}, nil
}
