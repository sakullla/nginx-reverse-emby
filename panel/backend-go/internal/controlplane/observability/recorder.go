package observability

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	RevisionQueue     = "nre_revision_queue"
	RevisionApply     = "nre_revision_apply"
	GenerationDrain   = "nre_generation_drain"
	GenerationCutover = "nre_generation_cutover"
	HotRestartUpgrade = "nre_hot_restart_upgrade"
)

var metricNames = map[string]struct{}{
	RevisionQueue: {}, RevisionApply: {}, GenerationDrain: {}, GenerationCutover: {}, HotRestartUpgrade: {},
}

type Event struct {
	Name         string
	Outcome      string
	OperationID  string
	AgentID      string
	Revision     int64
	GenerationID string
	Attempt      int
	Duration     time.Duration
}

type Observer interface {
	Observe(context.Context, Event)
}

type ObserverFunc func(context.Context, Event)

func (fn ObserverFunc) Observe(ctx context.Context, event Event) { fn(ctx, event) }

type observerContextKey struct{}

func WithObserver(ctx context.Context, observer Observer) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, observerContextKey{}, observer)
}

func FromContext(ctx context.Context) Observer {
	if ctx != nil {
		if observer, ok := ctx.Value(observerContextKey{}).(Observer); ok && observer != nil {
			return observer
		}
	}
	return Default()
}

func Observe(ctx context.Context, event Event) {
	defer func() { _ = recover() }()
	FromContext(ctx).Observe(ctx, event)
}

type metricKey struct {
	name    string
	outcome string
}

type Recorder struct {
	mu        sync.Mutex
	logger    *slog.Logger
	counts    map[metricKey]uint64
	durations map[metricKey]time.Duration
}

func NewRecorder(logger *slog.Logger) *Recorder {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Recorder{logger: logger, counts: make(map[metricKey]uint64), durations: make(map[metricKey]time.Duration)}
}

var defaultRecorder = NewRecorder(slog.Default())

func Default() *Recorder { return defaultRecorder }

func (r *Recorder) Observe(ctx context.Context, event Event) {
	if r == nil {
		return
	}
	name := normalizedMetricName(event.Name)
	outcome := normalizedOutcome(event.Outcome)
	key := metricKey{name: name, outcome: outcome}
	r.mu.Lock()
	r.counts[key]++
	r.durations[key] += event.Duration
	r.mu.Unlock()
	if outcome == "noop" {
		return
	}
	r.logger.InfoContext(ctx, "control-plane lifecycle event",
		"event", name, "outcome", outcome,
		"operation_id", cleanCorrelation(event.OperationID),
		"agent_id", cleanCorrelation(event.AgentID),
		"revision", event.Revision,
		"generation_id", cleanCorrelation(event.GenerationID),
		"attempt", event.Attempt,
		"duration_ms", event.Duration.Milliseconds(),
	)
}

func (r *Recorder) WritePrometheus(w io.Writer) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	keys := make([]metricKey, 0, len(r.counts))
	counts := make(map[metricKey]uint64, len(r.counts))
	durations := make(map[metricKey]time.Duration, len(r.durations))
	for key, count := range r.counts {
		keys = append(keys, key)
		counts[key] = count
		durations[key] = r.durations[key]
	}
	r.mu.Unlock()
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].name == keys[j].name {
			return keys[i].outcome < keys[j].outcome
		}
		return keys[i].name < keys[j].name
	})
	for _, key := range keys {
		if _, err := fmt.Fprintf(w, "%s_total{outcome=%q} %d\n", key.name, key.outcome, counts[key]); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "%s_duration_seconds_sum{outcome=%q} %.6f\n", key.name, key.outcome, durations[key].Seconds()); err != nil {
			return err
		}
	}
	return nil
}

func SafeAttrs(fields map[string]any) []any {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	attrs := make([]any, 0, len(keys)*2)
	for _, key := range keys {
		if sensitiveKey(key) {
			continue
		}
		attrs = append(attrs, key, fields[key])
	}
	return attrs
}

func normalizedMetricName(name string) string {
	name = strings.TrimSpace(name)
	if _, ok := metricNames[name]; ok {
		return name
	}
	return "nre_lifecycle_unknown"
}

func normalizedOutcome(outcome string) string {
	switch strings.ToLower(strings.TrimSpace(outcome)) {
	case "queued", "started", "applied", "drained", "forced", "succeeded", "failed", "rejected", "noop":
		return strings.ToLower(strings.TrimSpace(outcome))
	default:
		return "unknown"
	}
}

func sensitiveKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	for _, fragment := range []string{"token", "secret", "material", "private", "password", "lease_id", "certificate", "payload"} {
		if strings.Contains(key, fragment) {
			return true
		}
	}
	return false
}

func cleanCorrelation(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 160 {
		return value[:160]
	}
	return value
}
