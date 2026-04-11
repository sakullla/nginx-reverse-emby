package certs

import (
	"context"
	"fmt"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type renewalCandidate struct {
	id   int
	info CertificateInfo
}

func (m *Manager) startRenewalLoop(ctx context.Context) {
	m.renewalLoopStarted.Do(func() {
		m.renewalWG.Add(1)
		go func() {
			defer m.renewalWG.Done()
			m.runRenewalLoop(ctx, m.cfg.acme.renewalLoopInterval)
		}()
	})
}

func (m *Manager) runRenewalLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 10 * time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = m.runRenewalLoopIteration(ctx)
		}
	}
}

func (m *Manager) runRenewalLoopIteration(ctx context.Context) error {
	candidates := m.renewalCandidates()
	var firstErr error
	for _, candidate := range candidates {
		attemptCtx := ctx
		cancel := func() {}
		if timeout := m.cfg.acme.renewalAttemptTimeout; timeout > 0 {
			attemptCtx, cancel = context.WithTimeout(ctx, timeout)
		}
		err := m.renewCertificate(attemptCtx, candidate)
		cancel()
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (m *Manager) renewalCandidates() []renewalCandidate {
	m.mu.RLock()
	defer m.mu.RUnlock()

	candidates := make([]renewalCandidate, 0, len(m.active.byID))
	for id, entry := range m.active.byID {
		if normalizeCertificateType(entry.info.CertificateType) != "acme" {
			continue
		}
		if entry.info.IssuerMode != "local_http01" {
			continue
		}
		if !m.needsRenewal(entry.certificate.Leaf) {
			continue
		}
		candidates = append(candidates, renewalCandidate{id: id, info: entry.info})
	}
	return candidates
}

func (m *Manager) renewCertificate(ctx context.Context, candidate renewalCandidate) error {
	attemptStartedAt := m.cfg.now()
	policy := model.ManagedCertificatePolicy{
		ID:              candidate.id,
		Domain:          candidate.info.Domain,
		Enabled:         true,
		Scope:           candidate.info.Scope,
		IssuerMode:      candidate.info.IssuerMode,
		Status:          candidate.info.Status,
		Revision:        candidate.info.Revision,
		Usage:           normalizeUsage(candidate.info.Usage),
		CertificateType: normalizeCertificateType(candidate.info.CertificateType),
		SelfSigned:      candidate.info.SelfSigned,
	}
	certPEM, keyPEM, err := m.loadOrIssueACME(ctx, policy)
	if err != nil {
		m.recordRenewalFailure(candidate.id, err, attemptStartedAt)
		return fmt.Errorf("renew certificate %d: %w", candidate.id, err)
	}

	tlsCert, parsedChain, fingerprint, err := parseTLSMaterial(certPEM, keyPEM)
	if err != nil {
		m.recordRenewalFailure(candidate.id, err, attemptStartedAt)
		return fmt.Errorf("renew certificate %d: %w", candidate.id, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	current, ok := m.active.byID[candidate.id]
	if !ok {
		return nil
	}
	if normalizeCertificateType(current.info.CertificateType) != "acme" || current.info.IssuerMode != "local_http01" {
		return nil
	}

	updated := current.info
	updated.Fingerprint = fingerprint
	m.active.byID[candidate.id] = &managedCertificate{
		info:        updated,
		certificate: tlsCert,
		parsedChain: parsedChain,
	}
	return nil
}

func (m *Manager) recordRenewalFailure(certificateID int, renewalErr error, attemptStartedAt time.Time) {
	lock := m.issuanceLock(certificateID)
	lock.Lock()
	defer lock.Unlock()

	state, _, err := m.loadManagedCertificateState(certificateID)
	if err != nil {
		return
	}
	if state.ACME == nil {
		state.ACME = &model.ManagedCertificateACMEState{}
	}
	if state.ACME.Renewal.LastAttemptStatus == "success" && state.ACME.Renewal.LastAttemptAtUnix >= attemptStartedAt.Unix() {
		return
	}
	state.ACME.Renewal.LastAttemptAtUnix = m.cfg.now().Unix()
	state.ACME.Renewal.LastAttemptStatus = "error"
	state.ACME.Renewal.LastAttemptError = renewalErr.Error()
	_ = m.saveManagedCertificateState(certificateID, state)
}
