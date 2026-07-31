package service

import (
	"crypto/tls"
	"errors"
	"testing"
	"time"
)

func TestPolicyInternalPKIBoundariesAndDerivedSchedule(t *testing.T) {
	policy := DefaultInternalPKIPolicy()
	if err := ValidateInternalPKIPolicy(policy); err != nil {
		t.Fatalf("ValidateInternalPKIPolicy(default) error = %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*InternalPKIPolicy)
	}{
		{"weak curve", func(p *InternalPKIPolicy) { p.KeyCurve = "P-224" }},
		{"TLS downgrade", func(p *InternalPKIPolicy) { p.MinimumTLSVersion = tls.VersionTLS12 }},
		{"short CA", func(p *InternalPKIPolicy) { p.CALifetime = 365*24*time.Hour - time.Second }},
		{"long endpoint", func(p *InternalPKIPolicy) { p.EndpointLifetime = 397*24*time.Hour + time.Second }},
		{"late renewal", func(p *InternalPKIPolicy) { p.RenewalLifetimeDivisor = 4 }},
		{"unbounded retry", func(p *InternalPKIPolicy) { p.MaximumBackoff = 7 * time.Hour }},
		{"weak alert", func(p *InternalPKIPolicy) { p.CriticalFailureThreshold = 4 }},
		{"short audit", func(p *InternalPKIPolicy) { p.AuditRetention = 89 * 24 * time.Hour }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := policy
			test.mutate(&candidate)
			if err := ValidateInternalPKIPolicy(candidate); !errors.Is(err, ErrInvalidPKIPolicy) {
				t.Fatalf("ValidateInternalPKIPolicy() error = %v, want ErrInvalidPKIPolicy", err)
			}
		})
	}

	notBefore := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	notAfter := notBefore.Add(90 * 24 * time.Hour)
	due, err := PKIRenewalDueTime(policy, notBefore, notAfter, "agent-1/cert-fingerprint")
	if err != nil {
		t.Fatalf("PKIRenewalDueTime() error = %v", err)
	}
	base := notAfter.Add(-30 * 24 * time.Hour)
	if due.Before(base) || due.After(base.Add(24*time.Hour)) {
		t.Fatalf("renewal due = %v, want [%v, %v]", due, base, base.Add(24*time.Hour))
	}
	again, _ := PKIRenewalDueTime(policy, notBefore, notAfter, "agent-1/cert-fingerprint")
	if !again.Equal(due) {
		t.Fatalf("renewal stagger is not stable: %v != %v", again, due)
	}
	delay, err := PKIRetryDelay(policy, 20, "agent-1/cert-fingerprint")
	if err != nil || delay <= 0 || delay > 6*time.Hour {
		t.Fatalf("PKIRetryDelay() = %v, %v", delay, err)
	}
	level, err := PKICertificateAlertLevel(policy, 0, 24*time.Hour, 72*time.Minute)
	if err != nil || level != PKIAlertCritical {
		t.Fatalf("24h certificate final 72m alert = %q, %v", level, err)
	}
}
