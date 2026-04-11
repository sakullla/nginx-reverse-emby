package service

import (
	"context"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type LocalRuntimeManagedCertificateStore interface {
	ListManagedCertificates(context.Context) ([]storage.ManagedCertificateRow, error)
	ListHTTPRules(context.Context, string) ([]storage.HTTPRuleRow, error)
	SaveManagedCertificates(context.Context, []storage.ManagedCertificateRow) error
}

func ReconcileManagedCertificatesFromLocalRuntimeState(ctx context.Context, store LocalRuntimeManagedCertificateStore, agentID string, state storage.RuntimeState, now time.Time) error {
	resolvedAgentID := strings.TrimSpace(agentID)
	if resolvedAgentID == "" {
		return nil
	}

	rows, err := store.ListManagedCertificates(ctx)
	if err != nil {
		return err
	}
	rules, err := store.ListHTTPRules(ctx, resolvedAgentID)
	if err != nil {
		return err
	}

	reports := managedCertificateHeartbeatReportsFromRuntimeState(state.ManagedCertificateReports)
	nextRows, reportedCertIDs, changed := applyManagedCertificateHeartbeatReports(rows, resolvedAgentID, reports, now)
	nextRows, reconciled := reconcileLocalHTTP01CertificatesForAgent(
		nextRows,
		resolvedAgentID,
		rules,
		effectiveLocalApplyRevision(state),
		effectiveLocalApplyStatus(state),
		effectiveLocalApplyMessage(state),
		reportedCertIDs,
		now,
	)
	if !changed && !reconciled {
		return nil
	}
	return store.SaveManagedCertificates(ctx, nextRows)
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

func effectiveLocalApplyRevision(state storage.RuntimeState) int {
	if state.LastApplyRevision > 0 {
		return int(state.LastApplyRevision)
	}
	if state.CurrentRevision > 0 {
		return int(state.CurrentRevision)
	}
	return 0
}

func effectiveLocalApplyStatus(state storage.RuntimeState) string {
	return strings.TrimSpace(state.LastApplyStatus)
}

func effectiveLocalApplyMessage(state storage.RuntimeState) string {
	return state.LastApplyMessage
}
