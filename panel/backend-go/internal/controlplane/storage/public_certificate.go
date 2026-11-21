package storage

import (
	"context"
	"errors"
	"sort"
	"strings"
)

// PublicCertificateProjection is the certificate-center view exposed to one
// agent. It never includes PEM or private-key material.
type PublicCertificateProjection struct {
	ID                int    `json:"id"`
	Domain            string `json:"domain"`
	Enabled           bool   `json:"enabled"`
	MaterialAvailable bool   `json:"material_available"`
}

var errPublicCertificateProjection = errors.New("public certificate projection is unavailable")

// ListPublicCertificateProjections returns the public certificates already
// offered to the agent by the same audience filter as an agent snapshot, then
// removes internal PKI. Material availability is only a boolean.
func (s *GormStore) ListPublicCertificateProjections(ctx context.Context, agentID string) ([]PublicCertificateProjection, error) {
	if s == nil {
		return nil, errPublicCertificateProjection
	}
	rows, err := s.listManagedCertificatesForAgent(ctx, agentID, true)
	if err != nil {
		return nil, errPublicCertificateProjection
	}
	projections, err := selectPublicCertificateProjections(rows, func(row ManagedCertificateRow) (bool, error) {
		return s.publicCertificateMaterialAvailable(ctx, row)
	})
	if err != nil {
		return nil, errPublicCertificateProjection
	}
	return projections, nil
}

func (s *GormStore) listManagedCertificatesForAgent(ctx context.Context, agentID string, includeListeners bool) ([]ManagedCertificateRow, error) {
	resolvedAgentID := s.resolveAgentID(agentID)
	httpRows, err := s.ListHTTPRules(ctx, resolvedAgentID)
	if err != nil {
		return nil, err
	}
	l4Rows, err := s.ListL4Rules(ctx, resolvedAgentID)
	if err != nil {
		return nil, err
	}
	l4Rows = filterSyncL4RuleRows(l4Rows)
	relayRows, err := s.loadRelayListenersForSync(ctx, resolvedAgentID, httpRows, l4Rows)
	if err != nil {
		return nil, err
	}
	storedEgressRows, err := s.ListEgressProfiles(ctx)
	if err != nil {
		return nil, err
	}
	_, excludedEgressIDs := partitionSnapshotEgressRows(storedEgressRows)
	storedRelayRows, err := s.ListRelayListeners(ctx, "")
	if err != nil {
		return nil, err
	}
	_, excludedRelayIDs := partitionSnapshotRelayRows(storedRelayRows)
	relayFailClosed, err := s.pkiRelayFailClosed(ctx)
	if err != nil {
		return nil, err
	}
	if relayFailClosed {
		for _, row := range storedRelayRows {
			if row.ID > 0 {
				excludedRelayIDs[row.ID] = struct{}{}
			}
		}
		relayRows = nil
	}
	httpRows = filterHTTPRuleRowsForSnapshot(httpRows, excludedRelayIDs, excludedEgressIDs)
	certRows, err := s.ListManagedCertificates(ctx)
	if err != nil {
		return nil, err
	}
	rows := filterManagedCertificatesForAgent(certRows, resolvedAgentID, httpRows, relayRows)
	if !includeListeners {
		return rows, nil
	}
	added, _, err := s.listenerOnlyCertificates(ctx, certRows, rows, resolvedAgentID)
	return append(rows, added...), err
}

func (s *GormStore) publicCertificateMaterialAvailable(ctx context.Context, row ManagedCertificateRow) (bool, error) {
	domain, err := normalizeManagedCertificateGenerationDomain(row.Domain)
	if err != nil {
		return false, errPublicCertificateProjection
	}
	bundle, ok, err := s.LoadManagedCertificateMaterial(ctx, domain)
	certPEM, keyPEM := bundle.CertPEM, bundle.KeyPEM
	bundle = ManagedCertificateBundle{}
	available := ok && strings.TrimSpace(certPEM) != "" && strings.TrimSpace(keyPEM) != ""
	certPEM, keyPEM = "", ""
	if err != nil {
		return false, errPublicCertificateProjection
	}
	return available, nil
}

func selectPublicCertificateProjections(rows []ManagedCertificateRow, material func(ManagedCertificateRow) (bool, error)) ([]PublicCertificateProjection, error) {
	projections := make([]PublicCertificateProjection, 0)
	for _, row := range rows {
		if internalPKIManagedCertificate(row) {
			continue
		}
		domain, ok := publicCertificateProjectionDomain(row.Domain)
		if !ok {
			return nil, errPublicCertificateProjection
		}
		available := false
		if material != nil {
			var err error
			available, err = material(row)
			if err != nil {
				return nil, errPublicCertificateProjection
			}
		}
		projections = append(projections, PublicCertificateProjection{
			ID:                row.ID,
			Domain:            domain,
			Enabled:           row.Enabled,
			MaterialAvailable: available,
		})
	}
	sort.Slice(projections, func(i, j int) bool { return projections[i].ID < projections[j].ID })
	return projections, nil
}

func internalPKIManagedCertificate(row ManagedCertificateRow) bool {
	certificateType := strings.ToLower(strings.TrimSpace(row.CertificateType))
	usage := strings.ToLower(strings.TrimSpace(row.Usage))
	return certificateType == "internal_ca" || usage == "relay_ca" || usage == "relay_tunnel" || strings.EqualFold(strings.TrimSpace(row.Domain), "__relay-ca.internal")
}

func publicCertificateProjectionDomain(domain string) (string, bool) {
	domain = strings.TrimSpace(domain)
	if domain == "" || len(domain) > 253 || strings.ContainsAny(domain, "\r\n\x00") {
		return "", false
	}
	upper := strings.ToUpper(domain)
	if strings.Contains(upper, "PRIVATE KEY") || strings.Contains(upper, "BEGIN ") {
		return "", false
	}
	return domain, true
}
