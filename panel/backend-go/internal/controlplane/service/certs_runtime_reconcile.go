package service

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type LocalRuntimeManagedCertificateStore interface {
	storage.ManagedCertificateUpdateStore
	ListHTTPRules(context.Context, string) ([]storage.HTTPRuleRow, error)
}

func ReconcileManagedCertificatesFromLocalRuntimeState(ctx context.Context, store LocalRuntimeManagedCertificateStore, agentID string, state storage.RuntimeState, now time.Time) error {
	resolvedAgentID := strings.TrimSpace(agentID)
	if resolvedAgentID == "" {
		return nil
	}

	rules, err := store.ListHTTPRules(ctx, resolvedAgentID)
	if err != nil {
		return err
	}

	outcome := storage.NormalizeLocalApplyOutcome(state)
	reports := managedCertificateHeartbeatReportsFromRuntimeState(state.ManagedCertificateReports)
	err = store.UpdateManagedCertificates(ctx, func(rows []storage.ManagedCertificateRow) ([]storage.ManagedCertificateRow, bool, error) {
		nextRows, reportedCertIDs, changed := applyManagedCertificateHeartbeatReports(rows, resolvedAgentID, reports, now)
		nextRows, reconciled := reconcileLocalHTTP01CertificatesForAgent(
			nextRows,
			resolvedAgentID,
			defaultLocalCapabilities,
			rules,
			boundedRevisionInt(outcome.Revision),
			outcome.Status,
			outcome.Message,
			reportedCertIDs,
			now,
		)
		return nextRows, changed || reconciled, nil
	})
	if err != nil {
		return err
	}
	fullStore, ok := store.(storage.Store)
	if !ok {
		return nil
	}
	if _, ok := store.(storage.ManagedCertificateGenerationStore); !ok {
		return nil
	}
	_, err = NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     resolvedAgentID,
	}, fullStore).reconcileManagedCertificateGenerationPromotions(ctx)
	return err
}

func boundedRevisionInt(value int64) int {
	if value <= 0 {
		return 0
	}
	if value > int64(math.MaxInt) {
		return math.MaxInt
	}
	return int(value)
}

func managedCertificateHeartbeatReportsFromRuntimeState(reports []storage.ManagedCertificateReport) []ManagedCertificateHeartbeatReport {
	if len(reports) == 0 {
		return nil
	}

	converted := make([]ManagedCertificateHeartbeatReport, 0, len(reports))
	for _, report := range reports {
		converted = append(converted, ManagedCertificateHeartbeatReport{
			ID:           report.ID,
			Domain:       report.Domain,
			Status:       report.Status,
			LastIssueAt:  report.LastIssueAt,
			LastError:    report.LastError,
			MaterialHash: report.MaterialHash,
			ACMEInfo: ManagedCertificateACMEInfo{
				MainDomain: report.ACMEInfo.MainDomain,
				KeyLength:  report.ACMEInfo.KeyLength,
				SANDomains: report.ACMEInfo.SANDomains,
				Profile:    report.ACMEInfo.Profile,
				CA:         report.ACMEInfo.CA,
				Created:    report.ACMEInfo.Created,
				Renew:      report.ACMEInfo.Renew,
			},
			UpdatedAt: report.UpdatedAt,
		})
	}
	return converted
}
