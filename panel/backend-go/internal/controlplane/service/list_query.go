package service

import (
	"context"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

const (
	DefaultListPageSize = 20
	MaxListPageSize     = 100
)

// ListQuery is the shared list filter/pagination contract for resource list APIs.
// Empty AgentID means all agents; non-empty filters to that agent.
// Enabled nil means no enabled filter; non-nil filters to that value.
// Empty Status means no status filter; non-empty matches status case-insensitively.
// Tags filters resources containing any of the given tags (exact element match, OR
// within the dimension, AND with the other dimensions).
// CertificateID filters http rules by certificate domain approximation and relay
// listeners by their explicit certificate_id. EgressProfileID filters rules by egress
// profile. RelayListenerID filters rules whose relay_layers contain the listener id.
// Sync filters by approximate apply state ("applied"/"pending"); unrecognized values
// are normalized away (no filter). Referenced nil means no referenced filter;
// non-nil filters certificates by whether any rule/listener references them.
type ListQuery struct {
	AgentID  string
	Page     int
	PageSize int
	Q        string
	Enabled  *bool
	Status   string

	Tags            []string
	CertificateID   *int
	EgressProfileID *int
	RelayListenerID *int
	Sync            string
	Referenced      *bool
}

const (
	// ListSyncApplied marks resources whose revision is covered by the owning
	// agent's last_apply_revision (approximate, agent-level semantics).
	ListSyncApplied = "applied"
	// ListSyncPending marks resources whose revision is ahead of the owning
	// agent's last_apply_revision, or whose agent never reported one.
	ListSyncPending = "pending"
)

// PageMeta is returned with every paginated list response.
type PageMeta struct {
	Total    int `json:"total"`
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
}

// NormalizeListQuery clamps page/page_size and trims filter fields.
// Invalid or missing page defaults to 1; page_size defaults to 20 and is capped at 100.
func NormalizeListQuery(query ListQuery) ListQuery {
	query.AgentID = strings.TrimSpace(query.AgentID)
	query.Q = strings.TrimSpace(query.Q)
	query.Status = strings.TrimSpace(query.Status)
	tags := make([]string, 0, len(query.Tags))
	for _, tag := range query.Tags {
		if trimmed := strings.TrimSpace(tag); trimmed != "" {
			tags = append(tags, trimmed)
		}
	}
	query.Tags = tags
	// Only the two documented sync values are meaningful; anything else is
	// silently ignored (no filter) per the list-filter contract.
	switch strings.ToLower(strings.TrimSpace(query.Sync)) {
	case ListSyncApplied:
		query.Sync = ListSyncApplied
	case ListSyncPending:
		query.Sync = ListSyncPending
	default:
		query.Sync = ""
	}
	if query.Page < 1 {
		query.Page = 1
	}
	if query.PageSize < 1 {
		query.PageSize = DefaultListPageSize
	}
	if query.PageSize > MaxListPageSize {
		query.PageSize = MaxListPageSize
	}
	return query
}

// ApplyPage slices items for the given normalized page and returns the page slice plus meta.
// total is the pre-pagination filtered count.
func ApplyPage[T any](items []T, query ListQuery) ([]T, PageMeta) {
	query = NormalizeListQuery(query)
	total := len(items)
	meta := PageMeta{
		Total:    total,
		Page:     query.Page,
		PageSize: query.PageSize,
	}
	if total == 0 {
		return []T{}, meta
	}
	// Prove the page is in range before multiplying. A hostile MaxInt page
	// would otherwise overflow start to a negative value and panic while slicing.
	pageIndex := query.Page - 1
	if pageIndex > (total-1)/query.PageSize {
		return []T{}, meta
	}
	start := pageIndex * query.PageSize
	count := query.PageSize
	if remaining := total - start; count > remaining {
		count = remaining
	}
	end := start + count
	page := make([]T, end-start)
	copy(page, items[start:end])
	return page, meta
}

func matchesListQuery(q string, parts ...string) bool {
	q = strings.TrimSpace(q)
	if q == "" {
		return true
	}
	needle := strings.ToLower(q)
	for _, part := range parts {
		if strings.Contains(strings.ToLower(part), needle) {
			return true
		}
	}
	return false
}

// matchesEnabledFilter returns true when Enabled is nil, or when value equals *Enabled.
func matchesEnabledFilter(enabled *bool, value bool) bool {
	if enabled == nil {
		return true
	}
	return value == *enabled
}

// matchesStatusFilter returns true when status filter is empty, or when value matches case-insensitively.
func matchesStatusFilter(status string, value string) bool {
	status = strings.TrimSpace(status)
	if status == "" {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(value), status)
}

// matchesTagsFilter returns true when no tags are requested, or when values contains
// any requested tag. Tag matching is exact element equality (not substring) and the
// requested tags combine with OR semantics.
func matchesTagsFilter(requested []string, values []string) bool {
	if len(requested) == 0 {
		return true
	}
	for _, want := range requested {
		for _, value := range values {
			if value == want {
				return true
			}
		}
	}
	return false
}

// matchesOptionalIntFilter returns true when filter is nil, or when value is non-nil
// and equals *filter.
func matchesOptionalIntFilter(filter *int, value *int) bool {
	if filter == nil {
		return true
	}
	return value != nil && *value == *filter
}

// matchesSyncFilter returns true when sync is not a recognized value (no filter).
// A resource counts as applied only when its owning agent is known, has reported a
// last_apply_revision, and that revision covers the resource revision; everything
// else (unknown agent, agent never reported, revision ahead) counts as pending.
func matchesSyncFilter(sync string, revision int, lastApplyRevision int, agentKnown bool) bool {
	applied := agentKnown && lastApplyRevision > 0 && revision <= lastApplyRevision
	switch strings.ToLower(strings.TrimSpace(sync)) {
	case ListSyncApplied:
		return applied
	case ListSyncPending:
		return !applied
	default:
		return true
	}
}

// matchesReferencedFilter returns true when the referenced filter is nil, or when the
// computed referenced state equals *filter.
func matchesReferencedFilter(filter *bool, referenced bool) bool {
	if filter == nil {
		return true
	}
	return referenced == *filter
}

// agentRevisionStore is the store surface needed to resolve per-agent apply revisions.
type agentRevisionStore interface {
	ListAgents(context.Context) ([]storage.AgentRow, error)
	LoadLocalAgentState(context.Context) (storage.LocalAgentStateRow, error)
}

// agentLastApplyRevisionMap collects each agent's last applied revision from agent
// rows, overlaying the embedded local agent state when the local agent is enabled.
func agentLastApplyRevisionMap(ctx context.Context, cfg config.Config, store agentRevisionStore) (map[string]int, error) {
	revisions := map[string]int{}
	rows, err := store.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if id := strings.TrimSpace(row.ID); id != "" {
			revisions[id] = row.LastApplyRevision
		}
	}
	if cfg.EnableLocalAgent && strings.TrimSpace(cfg.LocalAgentID) != "" {
		state, err := store.LoadLocalAgentState(ctx)
		if err != nil {
			return nil, err
		}
		revisions[cfg.LocalAgentID] = state.LastApplyRevision
	}
	return revisions, nil
}

// managedCertificateLister is the store surface needed to resolve certificate domains.
type managedCertificateLister interface {
	ListManagedCertificates(context.Context) ([]storage.ManagedCertificateRow, error)
}

// certificateDomainByID resolves a certificate id to its normalized domain for the
// http-rule certificate domain-approximation filter. found is false when no
// certificate with the id exists (in which case no rule can match the filter).
func certificateDomainByID(ctx context.Context, store managedCertificateLister, id int) (domain string, found bool, err error) {
	rows, err := store.ListManagedCertificates(ctx)
	if err != nil {
		return "", false, err
	}
	for _, row := range rows {
		if row.ID == id {
			return strings.ToLower(normalizeCertificateHost(row.Domain)), true, nil
		}
	}
	return "", false, nil
}

// certificateReferenceStore is the store surface needed to compute certificate
// referenced state (http rule hostnames + relay listener certificate references).
type certificateReferenceStore interface {
	ListAgents(context.Context) ([]storage.AgentRow, error)
	ListHTTPRules(context.Context, string) ([]storage.HTTPRuleRow, error)
	ListRelayListeners(context.Context, string) ([]storage.RelayListenerRow, error)
}

// certificateReferenceSets gathers the hostnames used by http rules across all known
// agents (domain approximation) and the certificate ids explicitly referenced by
// relay listeners (certificate_id and trusted_ca_certificate_ids).
func certificateReferenceSets(ctx context.Context, cfg config.Config, store certificateReferenceStore) (map[string]struct{}, map[int]struct{}, error) {
	hostnames := map[string]struct{}{}
	certIDs := map[int]struct{}{}
	agentIDs, err := allKnownAgentIDs(ctx, cfg, store)
	if err != nil {
		return nil, nil, err
	}
	for _, agentID := range agentIDs {
		rows, err := store.ListHTTPRules(ctx, agentID)
		if err != nil {
			return nil, nil, err
		}
		for _, row := range rows {
			if _, host, ok := parseRuleFrontendTarget(row.FrontendURL); ok {
				hostnames[host] = struct{}{}
			}
		}
	}
	listeners, err := store.ListRelayListeners(ctx, "")
	if err != nil {
		return nil, nil, err
	}
	for _, row := range listeners {
		if row.CertificateID != nil {
			certIDs[*row.CertificateID] = struct{}{}
		}
		for _, id := range parseIntArray(row.TrustedCACertificateIDs) {
			certIDs[id] = struct{}{}
		}
	}
	return hostnames, certIDs, nil
}

// certificateReferenced reports whether a certificate is referenced under the domain
// approximation (rule frontend hostnames) or explicit relay listener references.
func certificateReferenced(domain string, id int, hostnames map[string]struct{}, certIDs map[int]struct{}) bool {
	if _, ok := hostnames[strings.ToLower(normalizeCertificateHost(domain))]; ok {
		return true
	}
	_, ok := certIDs[id]
	return ok
}

func agentDisplayNameMap(ctx context.Context, cfg config.Config, store agentLister) (map[string]string, error) {
	names := map[string]string{}
	if cfg.EnableLocalAgent && strings.TrimSpace(cfg.LocalAgentID) != "" {
		name := strings.TrimSpace(cfg.LocalAgentName)
		if name == "" {
			name = cfg.LocalAgentID
		}
		names[cfg.LocalAgentID] = name
	}
	rows, err := store.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		id := strings.TrimSpace(row.ID)
		if id == "" {
			continue
		}
		name := strings.TrimSpace(row.Name)
		if name == "" {
			name = id
		}
		names[id] = name
	}
	return names, nil
}

func resolveAgentDisplayName(names map[string]string, agentID string) string {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return ""
	}
	if name := strings.TrimSpace(names[agentID]); name != "" {
		return name
	}
	return agentID
}

func ensureKnownAgentID(ctx context.Context, cfg config.Config, store agentLister, agentID string) (string, error) {
	resolvedID := strings.TrimSpace(agentID)
	if resolvedID == "" {
		return "", nil
	}
	if cfg.EnableLocalAgent && resolvedID == cfg.LocalAgentID {
		return resolvedID, nil
	}
	rows, err := store.ListAgents(ctx)
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

// compile-time guard that storage.AgentRow stays available to this helper package surface.
var _ = storage.AgentRow{}
