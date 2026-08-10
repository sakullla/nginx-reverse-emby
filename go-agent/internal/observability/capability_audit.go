package observability

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/hostapi"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

const maxCapabilityAuditBytes int64 = 8 << 20

var capabilityAuditIdentityPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._:-]{0,255}$`)

type CapabilityAuditJournal struct {
	mu     sync.Mutex
	file   *os.File
	closed bool
}

type capabilityAuditRecord struct {
	Timestamp       time.Time `json:"timestamp"`
	PluginID        string    `json:"plugin_id"`
	InstanceID      string    `json:"instance_id"`
	Generation      string    `json:"generation"`
	Capability      string    `json:"capability"`
	ActorID         string    `json:"actor_id"`
	ResourceGroupID string    `json:"resource_group_id"`
	TargetKind      string    `json:"target_kind"`
	TargetID        string    `json:"target_id"`
	Outcome         string    `json:"outcome"`
	Reason          string    `json:"reason,omitempty"`
}

func NewCapabilityAuditJournal(path string) (*CapabilityAuditJournal, error) {
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		return nil, errors.New("capability audit path must be absolute")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create capability audit directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open capability audit journal: %w", err)
	}
	return &CapabilityAuditJournal{file: file}, nil
}

func (journal *CapabilityAuditJournal) Audit(_ context.Context, event hostapi.AuditEvent) error {
	if journal == nil {
		return errors.New("capability audit journal is unavailable")
	}
	record := capabilityAuditRecord{
		Timestamp: time.Now().UTC(), PluginID: canonicalAuditIdentity(event.Call.PluginID),
		InstanceID: canonicalAuditIdentity(event.Call.InstanceID), Generation: canonicalAuditIdentity(event.Call.Generation),
		Capability: canonicalAuditCapability(event.Call.Capability), ActorID: canonicalAuditIdentity(event.Call.Actor.ID),
		ResourceGroupID: canonicalAuditIdentity(event.Call.Target.ResourceGroupID), TargetKind: canonicalAuditIdentity(event.Call.Target.Kind),
		TargetID: canonicalAuditIdentity(event.Call.Target.ID), Outcome: canonicalAuditOutcome(event.Outcome), Reason: canonicalAuditReason(event.Reason),
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if journal.closed || journal.file == nil {
		return errors.New("capability audit journal is closed")
	}
	info, err := journal.file.Stat()
	if err != nil {
		return err
	}
	if info.Size()+int64(len(encoded)) > maxCapabilityAuditBytes {
		return errors.New("capability audit journal capacity exhausted")
	}
	written, err := journal.file.Write(encoded)
	if err != nil {
		return err
	}
	if written != len(encoded) {
		return errors.New("capability audit journal short write")
	}
	return journal.file.Sync()
}

func (journal *CapabilityAuditJournal) Close() error {
	if journal == nil {
		return nil
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if journal.closed {
		return nil
	}
	journal.closed = true
	if journal.file == nil {
		return nil
	}
	err := journal.file.Close()
	journal.file = nil
	return err
}

func canonicalAuditIdentity(value string) string {
	if pluginsdk.ValidatePolicyIdentity(value) != nil || !capabilityAuditIdentityPattern.MatchString(value) {
		return "invalid"
	}
	return value
}

func canonicalAuditCapability(value pluginsdk.HostCapability) string {
	if value.Validate() != nil {
		return "invalid"
	}
	return string(value)
}

func canonicalAuditOutcome(value string) string {
	if value == "allowed" || value == "denied" {
		return value
	}
	return "invalid"
}

func canonicalAuditReason(value string) string {
	switch value {
	case "", "invalid_call", "owner_denied", "not_declared", "not_granted", "actor_denied", "target_denied", "quota_unavailable", "quota_denied", "audit_unavailable":
		return value
	default:
		return "invalid"
	}
}

type CapabilityAuditObserver struct {
	Observer Observer
	Auditor  hostapi.Auditor
}

func (observer CapabilityAuditObserver) Observe(ctx context.Context, event Event) {
	if observer.Observer != nil {
		observer.Observer.Observe(ctx, event)
	}
}

func (observer CapabilityAuditObserver) Audit(ctx context.Context, event hostapi.AuditEvent) error {
	if observer.Auditor == nil {
		return errors.New("capability audit acknowledgement is unavailable")
	}
	return observer.Auditor.Audit(ctx, event)
}
