package service

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	PKIAlertKindRenewalFailed   = "renewal_failed"
	PKIAlertKindNearExpiry      = "near_expiry"
	PKIAlertKindFailedClosed    = "failed_closed"
	PKIAlertKindIdentity        = "identity_anomaly"
	PKIAlertKindRotationBlocked = "rotation_blocked"
	PKIAlertKindClock           = "clock_anomaly"
)

type PKIAlertFact struct {
	Kind       string
	ObjectType string
	ObjectID   string
	Level      PKIAlertLevel
	FirstSeen  time.Time
	LastSeen   time.Time
	Reason     string
}

type PKIDerivedAlert struct {
	ID         string
	Kind       string
	ObjectType string
	ObjectID   string
	Level      PKIAlertLevel
	FirstSeen  time.Time
	LastSeen   time.Time
	Reason     string
}

// DerivePKIAlerts is intentionally projection-only: canonical certificate,
// job, and event facts remain the single source of truth.
func DerivePKIAlerts(facts []PKIAlertFact) ([]PKIDerivedAlert, error) {
	alerts := make([]PKIDerivedAlert, 0, len(facts))
	seen := make(map[string]struct{}, len(facts))
	for _, fact := range facts {
		fact.Kind = strings.TrimSpace(fact.Kind)
		fact.ObjectType = strings.TrimSpace(fact.ObjectType)
		fact.ObjectID = strings.TrimSpace(fact.ObjectID)
		if fact.Kind == "" || fact.ObjectType == "" || fact.ObjectID == "" || fact.FirstSeen.IsZero() ||
			fact.LastSeen.Before(fact.FirstSeen) || !validDerivedPKIAlertLevel(fact.Level) {
			return nil, fmt.Errorf("%w: alert fact is invalid", ErrPKILifecycleInvalid)
		}
		id := stablePKIAlertID(fact.Kind, fact.ObjectType, fact.ObjectID)
		if _, duplicate := seen[id]; duplicate {
			return nil, fmt.Errorf("%w: duplicate alert fact %s", ErrPKILifecycleInvalid, id)
		}
		seen[id] = struct{}{}
		alerts = append(alerts, PKIDerivedAlert{
			ID: id, Kind: fact.Kind, ObjectType: fact.ObjectType, ObjectID: fact.ObjectID,
			Level: fact.Level, FirstSeen: fact.FirstSeen, LastSeen: fact.LastSeen, Reason: strings.TrimSpace(fact.Reason),
		})
	}
	sort.Slice(alerts, func(left, right int) bool {
		if alerts[left].Level != alerts[right].Level {
			return pkiAlertRank(alerts[left].Level) > pkiAlertRank(alerts[right].Level)
		}
		return alerts[left].ID < alerts[right].ID
	})
	return alerts, nil
}

func validDerivedPKIAlertLevel(level PKIAlertLevel) bool {
	return level == PKIAlertWarning || level == PKIAlertCritical || level == PKIAlertFailedClosed
}

func PKIEndpointAlertFact(
	certificate PKIEndpointCertificateState,
	decision PKIEndpointScheduleDecision,
	now time.Time,
	lastError string,
) (PKIAlertFact, bool) {
	if decision.AlertLevel == PKIAlertNone {
		return PKIAlertFact{}, false
	}
	kind := PKIAlertKindNearExpiry
	if decision.FailedClosed {
		kind = PKIAlertKindFailedClosed
	} else if certificate.FailureCount > 0 {
		kind = PKIAlertKindRenewalFailed
	}
	firstSeen := decision.RenewalDueAt
	if firstSeen.After(now) {
		firstSeen = now
	}
	return PKIAlertFact{
		Kind: kind, ObjectType: "pki_identity", ObjectID: certificate.IdentityID,
		Level: decision.AlertLevel, FirstSeen: firstSeen, LastSeen: now, Reason: strings.TrimSpace(lastError),
	}, true
}

func stablePKIAlertID(kind, objectType, objectID string) string {
	digest := sha256.Sum256([]byte(kind + "\x00" + objectType + "\x00" + objectID))
	return "pki-alert-" + hex.EncodeToString(digest[:16])
}

func pkiAlertRank(level PKIAlertLevel) int {
	switch level {
	case PKIAlertFailedClosed:
		return 3
	case PKIAlertCritical:
		return 2
	case PKIAlertWarning:
		return 1
	default:
		return 0
	}
}
