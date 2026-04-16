package diagnostics

import (
	"math"
	"time"
)

type Sample struct {
	Attempt    int     `json:"attempt"`
	Backend    string  `json:"backend,omitempty"`
	Success    bool    `json:"success"`
	LatencyMS  float64 `json:"latency_ms,omitempty"`
	StatusCode int     `json:"status_code,omitempty"`
	Error      string  `json:"error,omitempty"`
}

type Summary struct {
	Sent         int     `json:"sent"`
	Succeeded    int     `json:"succeeded"`
	Failed       int     `json:"failed"`
	LossRate     float64 `json:"loss_rate"`
	AvgLatencyMS float64 `json:"avg_latency_ms,omitempty"`
	MinLatencyMS float64 `json:"min_latency_ms,omitempty"`
	MaxLatencyMS float64 `json:"max_latency_ms,omitempty"`
	Quality      string  `json:"quality"`
}

type BackendReport struct {
	Backend  string           `json:"backend"`
	Summary  Summary          `json:"summary"`
	Adaptive *AdaptiveSummary `json:"adaptive,omitempty"`
	Children []BackendReport  `json:"children,omitempty"`
}

type Report struct {
	Kind     string          `json:"kind"`
	RuleID   int             `json:"rule_id"`
	Summary  Summary         `json:"summary"`
	Backends []BackendReport `json:"backends,omitempty"`
	Samples  []Sample        `json:"samples"`
}

type AdaptiveSummary struct {
	Preferred             bool    `json:"preferred,omitempty"`
	Reason                string  `json:"reason,omitempty"`
	Stability             float64 `json:"stability,omitempty"`
	RecentSucceeded       int     `json:"recent_succeeded,omitempty"`
	RecentFailed          int     `json:"recent_failed,omitempty"`
	LatencyMS             float64 `json:"latency_ms,omitempty"`
	EstimatedBandwidthBps float64 `json:"estimated_bandwidth_bps,omitempty"`
	PerformanceScore      float64 `json:"performance_score,omitempty"`
	State                 string  `json:"state,omitempty"`
	SampleConfidence      float64 `json:"sample_confidence,omitempty"`
	SlowStartActive       bool    `json:"slow_start_active,omitempty"`
	Outlier               bool    `json:"outlier,omitempty"`
	TrafficShareHint      string  `json:"traffic_share_hint,omitempty"`
}

func BuildReport(kind string, ruleID int, samples []Sample) Report {
	report := Report{
		Kind:     kind,
		RuleID:   ruleID,
		Summary:  buildSummary(kind, samples),
		Backends: buildBackendReports(kind, samples),
		Samples:  append([]Sample(nil), samples...),
	}
	return report
}

func buildSummary(kind string, samples []Sample) Summary {
	summary := Summary{
		Sent: len(samples),
	}
	if len(samples) == 0 {
		summary.Quality = "不可用"
		return summary
	}

	var (
		totalLatency float64
		minLatency   float64
		maxLatency   float64
	)

	for _, sample := range samples {
		if !sample.Success {
			summary.Failed++
			continue
		}
		summary.Succeeded++
		totalLatency += sample.LatencyMS
		if summary.Succeeded == 1 || sample.LatencyMS < minLatency {
			minLatency = sample.LatencyMS
		}
		if sample.LatencyMS > maxLatency {
			maxLatency = sample.LatencyMS
		}
	}

	summary.Failed = summary.Sent - summary.Succeeded
	summary.LossRate = roundMetric(float64(summary.Failed) / float64(summary.Sent))
	if summary.Succeeded > 0 {
		summary.AvgLatencyMS = roundMetric(totalLatency / float64(summary.Succeeded))
		summary.MinLatencyMS = roundMetric(minLatency)
		summary.MaxLatencyMS = roundMetric(maxLatency)
	}
	summary.Quality = classifyQuality(kind, summary)
	return summary
}

func buildBackendReports(kind string, samples []Sample) []BackendReport {
	grouped := make(map[string][]Sample)
	order := make([]string, 0)
	for _, sample := range samples {
		backend := sample.Backend
		if backend == "" {
			continue
		}
		if _, ok := grouped[backend]; !ok {
			order = append(order, backend)
		}
		grouped[backend] = append(grouped[backend], sample)
	}
	reports := make([]BackendReport, 0, len(order))
	for _, backend := range order {
		reports = append(reports, BackendReport{
			Backend: backend,
			Summary: buildSummary(kind, grouped[backend]),
		})
	}
	return reports
}

func LatencySample(attempt int, backend string, latency time.Duration, statusCode int) Sample {
	return Sample{
		Attempt:    attempt,
		Backend:    backend,
		Success:    true,
		LatencyMS:  roundMetric(float64(latency) / float64(time.Millisecond)),
		StatusCode: statusCode,
	}
}

func FailureSample(attempt int, backend string, err error) Sample {
	sample := Sample{
		Attempt: attempt,
		Backend: backend,
		Success: false,
	}
	if err != nil {
		sample.Error = err.Error()
	}
	return sample
}

func classifyQuality(kind string, summary Summary) string {
	if summary.Succeeded == 0 {
		return "不可用"
	}
	if kind == "http" {
		switch {
		case summary.LossRate <= 0.05 && summary.AvgLatencyMS <= 150:
			return "极佳"
		case summary.LossRate <= 0.10 && summary.AvgLatencyMS <= 400:
			return "良好"
		case summary.LossRate <= 0.20 && summary.AvgLatencyMS <= 800:
			return "一般"
		default:
			return "较差"
		}
	}
	switch {
	case summary.LossRate <= 0.05 && summary.AvgLatencyMS <= 50:
		return "极佳"
	case summary.LossRate <= 0.10 && summary.AvgLatencyMS <= 120:
		return "良好"
	case summary.LossRate <= 0.20 && summary.AvgLatencyMS <= 250:
		return "一般"
	default:
		return "较差"
	}
}

func roundMetric(value float64) float64 {
	return math.Round(value*10) / 10
}
