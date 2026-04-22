package task

import (
	"context"
	"fmt"
	"strconv"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/diagnostics"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/store"
)

type DiagnosticHandler struct {
	store      store.Store
	httpProber *diagnostics.HTTPProber
	tcpProber  *diagnostics.TCPProber
}

func NewDiagnosticHandler(st store.Store, httpProber *diagnostics.HTTPProber, tcpProber *diagnostics.TCPProber) *DiagnosticHandler {
	return &DiagnosticHandler{
		store:      st,
		httpProber: httpProber,
		tcpProber:  tcpProber,
	}
}

func (h *DiagnosticHandler) HandleTask(ctx context.Context, task TaskMessage) (map[string]any, error) {
	if h == nil || h.store == nil {
		return nil, fmt.Errorf("diagnostic handler store is required")
	}
	snapshot, err := h.loadSnapshot()
	if err != nil {
		return nil, err
	}

	ruleID, err := taskRuleID(task.RawPayload)
	if err != nil {
		return nil, err
	}

	switch task.TaskType {
	case TaskTypeDiagnoseHTTPRule:
		if h.httpProber == nil {
			return nil, fmt.Errorf("http prober is required")
		}
		rule, err := findHTTPRule(snapshot.Rules, ruleID)
		if err != nil {
			return nil, err
		}
		report, err := h.httpProber.Diagnose(ctx, rule, snapshot.RelayListeners)
		if err != nil {
			return nil, err
		}
		return reportToMap(report), nil
	case TaskTypeDiagnoseL4TCPRule:
		if h.tcpProber == nil {
			return nil, fmt.Errorf("tcp prober is required")
		}
		rule, err := findL4Rule(snapshot.L4Rules, ruleID)
		if err != nil {
			return nil, err
		}
		report, err := h.tcpProber.Diagnose(ctx, rule, snapshot.RelayListeners)
		if err != nil {
			return nil, err
		}
		return reportToMap(report), nil
	default:
		return nil, fmt.Errorf("unsupported task type %q", task.TaskType)
	}
}

func (h *DiagnosticHandler) loadSnapshot() (model.Snapshot, error) {
	snapshot, err := h.store.LoadAppliedSnapshot()
	if err == nil && (len(snapshot.Rules) > 0 || len(snapshot.L4Rules) > 0) {
		return snapshot, nil
	}
	return h.store.LoadDesiredSnapshot()
}

func taskRuleID(payload map[string]any) (int, error) {
	value, ok := payload["rule_id"]
	if !ok {
		return 0, fmt.Errorf("rule_id is required")
	}
	switch typed := value.(type) {
	case int:
		return typed, nil
	case float64:
		return int(typed), nil
	case string:
		id, err := strconv.Atoi(typed)
		if err != nil {
			return 0, fmt.Errorf("invalid rule_id %q", typed)
		}
		return id, nil
	default:
		return 0, fmt.Errorf("invalid rule_id type %T", value)
	}
}

func findHTTPRule(rules []model.HTTPRule, ruleID int) (model.HTTPRule, error) {
	for _, rule := range rules {
		if rule.ID == ruleID {
			return rule, nil
		}
	}
	return model.HTTPRule{}, fmt.Errorf("http rule %d not found", ruleID)
}

func findL4Rule(rules []model.L4Rule, ruleID int) (model.L4Rule, error) {
	for _, rule := range rules {
		if rule.ID == ruleID {
			return rule, nil
		}
	}
	return model.L4Rule{}, fmt.Errorf("l4 rule %d not found", ruleID)
}

func reportToMap(report diagnostics.Report) map[string]any {
	backends := make([]map[string]any, 0, len(report.Backends))
	for _, backend := range report.Backends {
		backends = append(backends, backendReportToMap(report.Kind, backend))
	}
	return map[string]any{
		"kind":    report.Kind,
		"rule_id": report.RuleID,
		"summary": map[string]any{
			"sent":           report.Summary.Sent,
			"succeeded":      report.Summary.Succeeded,
			"failed":         report.Summary.Failed,
			"loss_rate":      report.Summary.LossRate,
			"avg_latency_ms": report.Summary.AvgLatencyMS,
			"min_latency_ms": report.Summary.MinLatencyMS,
			"max_latency_ms": report.Summary.MaxLatencyMS,
			"quality":        report.Summary.Quality,
		},
		"backends": backends,
		"samples":  report.Samples,
	}
}

func backendReportToMap(kind string, backend diagnostics.BackendReport) map[string]any {
	payload := map[string]any{
		"backend": backend.Backend,
		"address": backend.Address,
		"summary": map[string]any{
			"sent":           backend.Summary.Sent,
			"succeeded":      backend.Summary.Succeeded,
			"failed":         backend.Summary.Failed,
			"loss_rate":      backend.Summary.LossRate,
			"avg_latency_ms": backend.Summary.AvgLatencyMS,
			"min_latency_ms": backend.Summary.MinLatencyMS,
			"max_latency_ms": backend.Summary.MaxLatencyMS,
			"quality":        backend.Summary.Quality,
		},
	}
	if backend.Adaptive != nil {
		adaptivePayload := map[string]any{
			"preferred":          backend.Adaptive.Preferred,
			"stability":          backend.Adaptive.Stability,
			"recent_succeeded":   backend.Adaptive.RecentSucceeded,
			"recent_failed":      backend.Adaptive.RecentFailed,
			"latency_ms":         backend.Adaptive.LatencyMS,
			"state":              backend.Adaptive.State,
			"sample_confidence":  backend.Adaptive.SampleConfidence,
			"slow_start_active":  backend.Adaptive.SlowStartActive,
			"traffic_share_hint": backend.Adaptive.TrafficShareHint,
		}
		if kind == "http" {
			adaptivePayload["reason"] = backend.Adaptive.Reason
			adaptivePayload["performance_score"] = backend.Adaptive.PerformanceScore
			adaptivePayload["outlier"] = backend.Adaptive.Outlier
		}
		if kind == "http" && backend.Adaptive.SustainedThroughputBps > 0 {
			adaptivePayload["sustained_throughput_bps"] = backend.Adaptive.SustainedThroughputBps
		}
		payload["adaptive"] = adaptivePayload
	}
	if len(backend.Children) > 0 {
		children := make([]map[string]any, 0, len(backend.Children))
		for _, child := range backend.Children {
			children = append(children, backendReportToMap(kind, child))
		}
		payload["children"] = children
	}
	return payload
}
