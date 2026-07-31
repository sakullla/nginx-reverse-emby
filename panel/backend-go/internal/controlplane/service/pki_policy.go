package service

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"time"
)

const (
	PKIKeyAlgorithm       = "ECDSA"
	PKIKeyCurve           = "P-256"
	PKISignatureAlgorithm = "ECDSA-SHA256"
)

const pkiDay = 24 * time.Hour

var ErrInvalidPKIPolicy = errors.New("invalid internal PKI policy")

// InternalPKIPolicy is the single validator input for internal certificate
// crypto, scheduling, alert, and retention invariants.
type InternalPKIPolicy struct {
	KeyAlgorithm             string
	KeyCurve                 string
	SignatureAlgorithm       string
	MinimumTLSVersion        uint16
	CALifetime               time.Duration
	EndpointLifetime         time.Duration
	NotBeforeSkew            time.Duration
	RenewalLifetimeDivisor   int
	RenewalStaggerCap        time.Duration
	RenewalStaggerDivisor    int
	InitialBackoff           time.Duration
	MaximumBackoff           time.Duration
	BackoffJitterPercent     int
	CriticalFailureThreshold int
	CriticalExpiryCap        time.Duration
	CriticalExpiryDivisor    int
	AuditRetention           time.Duration
}

func DefaultInternalPKIPolicy() InternalPKIPolicy {
	return InternalPKIPolicy{
		KeyAlgorithm:             PKIKeyAlgorithm,
		KeyCurve:                 PKIKeyCurve,
		SignatureAlgorithm:       PKISignatureAlgorithm,
		MinimumTLSVersion:        tls.VersionTLS13,
		CALifetime:               10 * 365 * pkiDay,
		EndpointLifetime:         90 * pkiDay,
		NotBeforeSkew:            5 * time.Minute,
		RenewalLifetimeDivisor:   3,
		RenewalStaggerCap:        pkiDay,
		RenewalStaggerDivisor:    30,
		InitialBackoff:           time.Minute,
		MaximumBackoff:           6 * time.Hour,
		BackoffJitterPercent:     20,
		CriticalFailureThreshold: 3,
		CriticalExpiryCap:        pkiDay,
		CriticalExpiryDivisor:    20,
		AuditRetention:           365 * pkiDay,
	}
}

func ValidateInternalPKIPolicy(policy InternalPKIPolicy) error {
	invalid := func(field, expected string) error {
		return fmt.Errorf("%w: %s must be %s", ErrInvalidPKIPolicy, field, expected)
	}
	if policy.KeyAlgorithm != PKIKeyAlgorithm {
		return invalid("key algorithm", PKIKeyAlgorithm)
	}
	if policy.KeyCurve != PKIKeyCurve {
		return invalid("key curve", PKIKeyCurve)
	}
	if policy.SignatureAlgorithm != PKISignatureAlgorithm {
		return invalid("signature algorithm", PKISignatureAlgorithm)
	}
	if policy.MinimumTLSVersion != tls.VersionTLS13 {
		return invalid("minimum TLS version", "TLS 1.3")
	}
	if policy.CALifetime < 365*pkiDay || policy.CALifetime > 20*365*pkiDay {
		return invalid("CA lifetime", "between 1 and 20 years")
	}
	if policy.EndpointLifetime < pkiDay || policy.EndpointLifetime > 397*pkiDay {
		return invalid("endpoint lifetime", "between 24 hours and 397 days")
	}
	if policy.NotBeforeSkew != 5*time.Minute {
		return invalid("not-before skew", "5 minutes")
	}
	if policy.RenewalLifetimeDivisor != 3 || policy.RenewalStaggerCap != pkiDay || policy.RenewalStaggerDivisor != 30 {
		return invalid("renewal profile", "lifetime/3 with a min(24h, lifetime/30) stagger")
	}
	if policy.InitialBackoff != time.Minute || policy.MaximumBackoff != 6*time.Hour || policy.BackoffJitterPercent != 20 {
		return invalid("retry profile", "1 minute exponential backoff capped at 6 hours with 20% jitter")
	}
	if policy.CriticalFailureThreshold != 3 || policy.CriticalExpiryCap != pkiDay || policy.CriticalExpiryDivisor != 20 {
		return invalid("critical alert profile", "3 failures or min(24h, lifetime/20) remaining")
	}
	if policy.AuditRetention < 90*pkiDay || policy.AuditRetention > 3650*pkiDay || policy.AuditRetention%pkiDay != 0 {
		return invalid("audit retention", "a whole-day value between 90 and 3650 days")
	}
	return nil
}

func PKIRenewalDueTime(policy InternalPKIPolicy, notBefore, notAfter time.Time, stableIdentity string) (time.Time, error) {
	if err := ValidateInternalPKIPolicy(policy); err != nil {
		return time.Time{}, err
	}
	if notBefore.IsZero() || !notAfter.After(notBefore) {
		return time.Time{}, fmt.Errorf("%w: certificate validity interval is invalid", ErrInvalidPKIPolicy)
	}
	lifetime := notAfter.Sub(notBefore)
	due := notAfter.Add(-lifetime / time.Duration(policy.RenewalLifetimeDivisor))
	staggerLimit := minDuration(policy.RenewalStaggerCap, lifetime/time.Duration(policy.RenewalStaggerDivisor))
	if staggerLimit <= 0 {
		return due, nil
	}
	return due.Add(stableDurationFraction(stableIdentity, staggerLimit)), nil
}

func PKIRetryDelay(policy InternalPKIPolicy, failureCount int, stableIdentity string) (time.Duration, error) {
	if err := ValidateInternalPKIPolicy(policy); err != nil {
		return 0, err
	}
	if failureCount <= 0 {
		return 0, fmt.Errorf("%w: failure count must be positive", ErrInvalidPKIPolicy)
	}
	base := policy.InitialBackoff
	for attempt := 1; attempt < failureCount && base < policy.MaximumBackoff; attempt++ {
		if base > policy.MaximumBackoff/2 {
			base = policy.MaximumBackoff
			break
		}
		base *= 2
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d", stableIdentity, failureCount)))
	unit := float64(binary.BigEndian.Uint64(digest[:8])) / float64(math.MaxUint64)
	jitter := (unit*2 - 1) * float64(policy.BackoffJitterPercent) / 100
	delay := time.Duration(float64(base) * (1 + jitter))
	if delay > policy.MaximumBackoff {
		return policy.MaximumBackoff, nil
	}
	return delay, nil
}

type PKIAlertLevel string

const (
	PKIAlertNone         PKIAlertLevel = "none"
	PKIAlertWarning      PKIAlertLevel = "warning"
	PKIAlertCritical     PKIAlertLevel = "critical"
	PKIAlertFailedClosed PKIAlertLevel = "failed_closed"
)

func PKICertificateAlertLevel(policy InternalPKIPolicy, failureCount int, lifetime, remaining time.Duration) (PKIAlertLevel, error) {
	if err := ValidateInternalPKIPolicy(policy); err != nil {
		return "", err
	}
	if lifetime <= 0 || failureCount < 0 {
		return "", fmt.Errorf("%w: alert inputs are invalid", ErrInvalidPKIPolicy)
	}
	if remaining <= 0 {
		return PKIAlertFailedClosed, nil
	}
	criticalWindow := minDuration(policy.CriticalExpiryCap, lifetime/time.Duration(policy.CriticalExpiryDivisor))
	if failureCount >= policy.CriticalFailureThreshold || remaining <= criticalWindow {
		return PKIAlertCritical, nil
	}
	if failureCount > 0 {
		return PKIAlertWarning, nil
	}
	return PKIAlertNone, nil
}

func minDuration(left, right time.Duration) time.Duration {
	if left < right {
		return left
	}
	return right
}

func stableDurationFraction(seed string, maximum time.Duration) time.Duration {
	digest := sha256.Sum256([]byte(seed))
	unit := float64(binary.BigEndian.Uint64(digest[:8])) / float64(math.MaxUint64)
	return time.Duration(unit * float64(maximum))
}
