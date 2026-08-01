package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

type PKIAuditEvent struct {
	ID               string         `json:"id"`
	Type             string         `json:"type"`
	OccurredAt       time.Time      `json:"occurred_at"`
	Source           string         `json:"source"`
	OperatorID       string         `json:"operator_id,omitempty"`
	ObjectType       string         `json:"object_type"`
	ObjectID         string         `json:"object_id"`
	CertificateID    string         `json:"certificate_id,omitempty"`
	CAGeneration     int64          `json:"ca_generation,omitempty"`
	Result           string         `json:"result"`
	Reason           string         `json:"reason,omitempty"`
	SecurityRevision int64          `json:"security_revision"`
	Details          map[string]any `json:"details"`
}

func NewPKIAuditEvent(eventType, source, objectID, result, reason string, occurredAt time.Time) PKIAuditEvent {
	event := PKIAuditEvent{
		Type: strings.TrimSpace(eventType), OccurredAt: occurredAt.UTC(), Source: strings.TrimSpace(source),
		ObjectType: "pki_identity", ObjectID: strings.TrimSpace(objectID), Result: strings.TrimSpace(result),
		Reason: strings.TrimSpace(reason), Details: map[string]any{},
	}
	event.ID = stablePKIAuditEventID(event)
	return event
}

func ValidatePKIAuditEvent(event PKIAuditEvent) error {
	if strings.TrimSpace(event.ID) == "" || strings.TrimSpace(event.Type) == "" || event.OccurredAt.IsZero() ||
		strings.TrimSpace(event.Source) == "" || strings.TrimSpace(event.ObjectType) == "" ||
		strings.TrimSpace(event.ObjectID) == "" || strings.TrimSpace(event.Result) == "" || event.SecurityRevision < 0 ||
		event.CAGeneration < 0 {
		return fmt.Errorf("%w: audit event fields are incomplete", ErrPKILifecycleInvalid)
	}
	if event.ID != stablePKIAuditEventID(event) {
		return fmt.Errorf("%w: audit event ID does not match immutable fields", ErrPKILifecycleInvalid)
	}
	for key := range event.Details {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("%w: audit detail keys must be non-empty", ErrPKILifecycleInvalid)
		}
	}
	return nil
}

type PKIAuditFilter struct {
	Types          []string
	ObjectType     string
	ObjectID       string
	CertificateID  string
	CAGeneration   int64
	OperatorID     string
	Source         string
	Result         string
	OccurredAfter  time.Time
	OccurredBefore time.Time
	Limit          int
}

type PKIAuditRepository interface {
	AppendPKIAuditEvent(context.Context, PKIAuditEvent) error
	QueryPKIAuditEvents(context.Context, PKIAuditFilter) ([]PKIAuditEvent, error)
	DeleteExpiredPKIAuditEvents(context.Context, time.Time, []string) (int64, error)
}

type PKIAuditService struct {
	repository PKIAuditRepository
	retention  time.Duration
	clock      func() time.Time
}

func NewPKIAuditService(repository PKIAuditRepository, retention time.Duration, clock func() time.Time) (*PKIAuditService, error) {
	if repository == nil || retention < 90*pkiDay || retention > 3650*pkiDay || retention%pkiDay != 0 {
		return nil, fmt.Errorf("%w: audit repository and retention are invalid", ErrPKILifecycleInvalid)
	}
	if clock == nil {
		clock = time.Now
	}
	return &PKIAuditService{repository: repository, retention: retention, clock: clock}, nil
}

func (s *PKIAuditService) Append(ctx context.Context, event PKIAuditEvent) error {
	if event.ID == "" {
		event.ID = stablePKIAuditEventID(event)
	}
	if err := ValidatePKIAuditEvent(event); err != nil {
		return err
	}
	return s.repository.AppendPKIAuditEvent(ctx, event)
}

func (s *PKIAuditService) Query(ctx context.Context, filter PKIAuditFilter) ([]PKIAuditEvent, error) {
	if filter.Limit < 0 || filter.CAGeneration < 0 ||
		(!filter.OccurredAfter.IsZero() && !filter.OccurredBefore.IsZero() && filter.OccurredBefore.Before(filter.OccurredAfter)) {
		return nil, fmt.Errorf("%w: audit filter is invalid", ErrPKILifecycleInvalid)
	}
	events, err := s.repository.QueryPKIAuditEvents(ctx, filter)
	if err != nil {
		return nil, err
	}
	for _, event := range events {
		if err := ValidatePKIAuditEvent(event); err != nil {
			return nil, err
		}
	}
	sort.SliceStable(events, func(left, right int) bool {
		if events[left].OccurredAt.Equal(events[right].OccurredAt) {
			return events[left].ID < events[right].ID
		}
		return events[left].OccurredAt.After(events[right].OccurredAt)
	})
	return events, nil
}

func (s *PKIAuditService) EnforceRetention(ctx context.Context, protectedEventIDs []string) (int64, error) {
	now := s.clock().UTC()
	if now.IsZero() {
		return 0, fmt.Errorf("%w: clock returned zero", ErrPKILifecycleInvalid)
	}
	protected := append([]string(nil), protectedEventIDs...)
	for _, eventID := range protected {
		if strings.TrimSpace(eventID) == "" {
			return 0, fmt.Errorf("%w: protected audit event ID is empty", ErrPKILifecycleInvalid)
		}
	}
	sort.Strings(protected)
	protected = slicesCompactStrings(protected)
	return s.repository.DeleteExpiredPKIAuditEvents(ctx, now.Add(-s.retention), protected)
}

func stablePKIAuditEventID(event PKIAuditEvent) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf(
		"%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%d\x00%s\x00%s\x00%d",
		event.Type, event.Source, event.OperatorID, event.ObjectType, event.ObjectID,
		event.OccurredAt.UTC().Format(time.RFC3339Nano), event.CertificateID,
		event.CAGeneration, event.Result, event.Reason, event.SecurityRevision,
	)))
	return "pki-event-" + hex.EncodeToString(digest[:16])
}

func slicesCompactStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	result := values[:1]
	for _, value := range values[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}
