package certs

import (
	"context"
	"fmt"
	"strconv"
	"strings"
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

	now := m.cfg.now()
	candidates := make([]renewalCandidate, 0, len(m.active.byID))
	for id, entry := range m.active.byID {
		if normalizeCertificateType(entry.info.CertificateType) != "acme" {
			continue
		}
		if entry.info.IssuerMode != "local_http01" {
			continue
		}
		if m.isInRenewalBackoffLocked(id, now) {
			continue
		}
		if !m.needsRenewalForScope(entry.certificate.Leaf, entry.info.Scope) {
			continue
		}
		candidates = append(candidates, renewalCandidate{id: id, info: entry.info})
	}
	return candidates
}

// isInRenewalBackoffLocked reports whether a certificate is currently in a
// failed-retry backoff window (next-retry-at strictly in the future). Only
// applies to failed-retry state; it never affects the expiry-based renewal
// window (needsRenewalForScope). Zero/legacy state (no backoff class, or
// next-retry-at <= 0) means "retry immediately". The lock is held by the
// caller; state is loaded read-only without mutating persisted state.
func (m *Manager) isInRenewalBackoffLocked(certificateID int, now time.Time) bool {
	state, ok, err := m.loadManagedCertificateState(certificateID)
	if err != nil || !ok || state.ACME == nil {
		return false
	}
	renewal := state.ACME.Renewal
	if strings.TrimSpace(renewal.BackoffClass) == "" {
		return false
	}
	if renewal.LastAttemptStatus != "error" {
		return false
	}
	next := renewal.BackoffRetryNext
	if next <= 0 {
		return false
	}
	return now.Unix() < next
}

func (m *Manager) renewCertificate(ctx context.Context, candidate renewalCandidate) error {
	defer m.issuanceLock(candidate.id)()

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
	certPEM, keyPEM, err := m.loadOrIssueACMEUnlocked(ctx, policy)
	if err != nil {
		// loadOrIssueACMEUnlocked already recorded the failure (and backoff) into
		// persisted state; surface the error to the loop without double-counting.
		return fmt.Errorf("renew certificate %d: %w", candidate.id, err)
	}

	tlsCert, parsedChain, fingerprint, err := parseTLSMaterial(certPEM, keyPEM)
	if err != nil {
		m.recordRenewalFailureLocked(candidate.id, err)
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
		info:         updated,
		certificate:  tlsCert,
		parsedChain:  parsedChain,
		materialHash: hashManagedCertificateMaterial(certPEM, keyPEM),
	}
	return nil
}

func (m *Manager) recordRenewalFailure(certificateID int, renewalErr error) {
	defer m.issuanceLock(certificateID)()
	m.recordRenewalFailureLocked(certificateID, renewalErr)
}

func (m *Manager) recordRenewalFailureLocked(certificateID int, renewalErr error) {
	state, _, err := m.loadManagedCertificateState(certificateID)
	if err != nil {
		return
	}
	if state.ACME == nil {
		state.ACME = &model.ManagedCertificateACMEState{}
	}

	class := classifyRenewalError(renewalErr)
	retryAfter := extractRenewalRetryAfter(renewalErr)

	// Only counts failed retries. A successful first issuance (LastAttemptStatus
	// == "success") leaves BackoffRetryNum at zero; subsequent failures start at 1.
	retryNum := state.ACME.Renewal.BackoffRetryNum
	if state.ACME.Renewal.LastAttemptStatus == "error" && retryNum > 0 {
		retryNum = retryNum + 1
	} else {
		retryNum = 1
	}
	delay := renewalBackoffDelay(class, retryAfter, retryNum)
	now := m.cfg.now()

	state.ACME.Renewal.LastAttemptAtUnix = now.Unix()
	state.ACME.Renewal.LastAttemptStatus = "error"
	state.ACME.Renewal.LastAttemptError = renewalErr.Error()
	state.ACME.Renewal.BackoffClass = class
	state.ACME.Renewal.BackoffRetryNext = now.Add(delay).Unix()
	state.ACME.Renewal.BackoffRetryNum = retryNum
	_ = m.saveManagedCertificateState(certificateID, state)
}

// Failure backoff classes. These mirror the control-plane retry curve
// (panel/backend-go/internal/controlplane/service/certs.go) so the local agent
// renewal path and the master control-plane issuance path share the same delays
// per R4. Duplicated here because the go-agent cannot import the control-plane.
const (
	backoffClassTransient   = "transient"
	backoffClassPersistent  = "persistent"
	backoffClassRateLimited = "rate_limited"

	backoffTransientBase  = 5 * time.Second
	backoffTransientCap   = 5 * time.Minute
	backoffPersistentBase = time.Hour
	backoffPersistentCap  = 32 * time.Hour
	backoffRateLimitedMin = time.Hour
	backoffRateLimitedCap = 32 * time.Hour

	// backoffMaxShift bounds exponential growth (base<<shift) so delays stay within
	// class caps and never overflow for large retry counts.
	backoffMaxShift = 6
)

// classifyRenewalError maps an ACME/issuer failure to a backoff class. The
// heuristic is string-based (lego error types are not stable across releases);
// durable misconfiguration (auth, validation, quota) defaults to persistent so
// retries do not burn LE's 5-failed-validations/hour/hostname limit.
func classifyRenewalError(err error) string {
	if err == nil {
		return backoffClassTransient
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "rate-limit") ||
		strings.Contains(msg, "too many") ||
		strings.Contains(msg, "retry-after"):
		return backoffClassRateLimited
	case renewalTransientMessage(msg):
		return backoffClassTransient
	default:
		return backoffClassPersistent
	}
}

func renewalTransientMessage(msg string) bool {
	markers := []string{
		"timeout", "timed out", "connection reset", "connection refused",
		"connection closed", "no such host", "temporary", "i/o timeout",
		"eof", "server misbehaving", "502", "503", "504", "service unavailable",
	}
	for _, marker := range markers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// extractRenewalRetryAfter best-effort parses an ACME Retry-After value (seconds)
// from an error message. lego does not reliably surface Retry-After as a
// structured field across releases, so this scans the error text; callers fall
// back to the class curve when it returns 0.
func extractRenewalRetryAfter(err error) time.Duration {
	if err == nil {
		return 0
	}
	msg := strings.ToLower(err.Error())
	idx := strings.Index(msg, "retry-after")
	if idx < 0 {
		return 0
	}
	rest := strings.TrimLeft(msg[idx+len("retry-after"):], " :=\t")
	digits := strings.Builder{}
	for _, r := range rest {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
			continue
		}
		break
	}
	if digits.Len() == 0 {
		return 0
	}
	seconds, err := strconv.Atoi(digits.String())
	if err != nil || seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

// renewalBackoffDelay computes the delay before the next attempt for a failed
// issuance. It is a pure function of (class, retryAfter, retryCount) so it is
// fully testable: exponential growth base<<shift capped per class, plus
// deterministic jitter (spread by retryCount) so simultaneously-failed
// certificates do not retry in lockstep without introducing randomness.
func renewalBackoffDelay(class string, retryAfter time.Duration, retryCount int) time.Duration {
	base, capDelay := renewalBackoffBaseAndCap(class, retryAfter)

	shift := retryCount - 1
	if shift < 0 {
		shift = 0
	}
	if shift > backoffMaxShift {
		shift = backoffMaxShift
	}
	delay := base << uint(shift)
	if delay <= 0 || delay > capDelay {
		delay = capDelay
	}

	jitter := delay / 4 * time.Duration(renewalBackoffJitterFraction(retryCount))
	if maxJitter := capDelay / 4; jitter > maxJitter {
		jitter = maxJitter
	}
	return delay + jitter
}

// renewalBackoffJitterFraction returns 0..3 (quarters of the delay)
// deterministically from the attempt count, spreading retries without
// randomness.
func renewalBackoffJitterFraction(retryCount int) int64 {
	switch retryCount % 4 {
	case 1:
		return 1
	case 2:
		return 2
	case 3:
		return 3
	default:
		return 0
	}
}

func renewalBackoffBaseAndCap(class string, retryAfter time.Duration) (time.Duration, time.Duration) {
	switch class {
	case backoffClassTransient:
		return backoffTransientBase, backoffTransientCap
	case backoffClassRateLimited:
		base := retryAfter
		if base <= 0 || base < backoffRateLimitedMin {
			base = backoffRateLimitedMin
		}
		return base, backoffRateLimitedCap
	default:
		return backoffPersistentBase, backoffPersistentCap
	}
}
