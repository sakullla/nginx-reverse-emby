package service

import (
	"fmt"
	"strings"
	"time"
)

// PKIEndpointCertificateState is the scheduler's canonical view of the active
// public certificate generation. It intentionally contains no private key.
type PKIEndpointCertificateState struct {
	IdentityID                   string
	CertificateID                string
	CertificateFingerprintSHA256 string
	PublicKeyFingerprintSHA256   string
	Generation                   int64
	NotBefore                    time.Time
	NotAfter                     time.Time
	FailureCount                 int
	NextAttemptAt                time.Time
}

type PKIEndpointScheduleDecision struct {
	RenewalDueAt time.Time
	EffectiveAt  time.Time
	Due          bool
	Forced       bool
	FailedClosed bool
	AlertLevel   PKIAlertLevel
}

// EvaluatePKIEndpointSchedule applies the stable lifetime/3 stagger and retry
// policy. Forced rotation bypasses both the normal due time and prior backoff,
// but remains scoped to the supplied identity.
func EvaluatePKIEndpointSchedule(
	policy InternalPKIPolicy,
	certificate PKIEndpointCertificateState,
	now time.Time,
	forced bool,
) (PKIEndpointScheduleDecision, error) {
	if strings.TrimSpace(certificate.IdentityID) == "" || strings.TrimSpace(certificate.CertificateID) == "" ||
		certificate.Generation <= 0 || certificate.FailureCount < 0 || now.IsZero() {
		return PKIEndpointScheduleDecision{}, fmt.Errorf("%w: endpoint schedule fields are incomplete", ErrPKILifecycleInvalid)
	}
	dueAt, err := PKIRenewalDueTime(policy, certificate.NotBefore, certificate.NotAfter, PKIJitterSeed{
		IdentityID: certificate.IdentityID, CertificateFingerprintSHA256: certificate.CertificateFingerprintSHA256,
	})
	if err != nil {
		return PKIEndpointScheduleDecision{}, err
	}
	effectiveAt := dueAt
	if !forced && !certificate.NextAttemptAt.IsZero() && certificate.NextAttemptAt.After(effectiveAt) {
		effectiveAt = certificate.NextAttemptAt
	}
	if forced {
		effectiveAt = now
	}
	remaining := certificate.NotAfter.Sub(now)
	alert, err := PKICertificateAlertLevel(policy, certificate.FailureCount, certificate.NotAfter.Sub(certificate.NotBefore), remaining)
	if err != nil {
		return PKIEndpointScheduleDecision{}, err
	}
	return PKIEndpointScheduleDecision{
		RenewalDueAt: dueAt,
		EffectiveAt:  effectiveAt,
		Due:          !now.Before(effectiveAt),
		Forced:       forced,
		FailedClosed: !now.Before(certificate.NotAfter),
		AlertLevel:   alert,
	}, nil
}

func NextPKIEndpointRetry(
	policy InternalPKIPolicy,
	certificate PKIEndpointCertificateState,
	failedAt time.Time,
) (PKIEndpointCertificateState, error) {
	if failedAt.IsZero() || certificate.FailureCount < 0 {
		return PKIEndpointCertificateState{}, fmt.Errorf("%w: endpoint retry fields are invalid", ErrPKILifecycleInvalid)
	}
	certificate.FailureCount++
	delay, err := PKIRetryDelay(policy, certificate.FailureCount, PKIJitterSeed{
		IdentityID: certificate.IdentityID, CertificateFingerprintSHA256: certificate.CertificateFingerprintSHA256,
	})
	if err != nil {
		return PKIEndpointCertificateState{}, err
	}
	certificate.NextAttemptAt = failedAt.Add(delay)
	return certificate, nil
}
