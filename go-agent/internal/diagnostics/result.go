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

type Report struct {
	Kind    string   `json:"kind"`
	RuleID  int      `json:"rule_id"`
	Summary Summary  `json:"summary"`
	Samples []Sample `json:"samples"`
}

func BuildReport(kind string, ruleID int, samples []Sample) Report {
	report := Report{
		Kind:    kind,
		RuleID:  ruleID,
		Summary: buildSummary(samples),
		Samples: append([]Sample(nil), samples...),
	}
	return report
}

func buildSummary(samples []Sample) Summary {
	summary := Summary{
		Sent: len(samples),
	}
	if len(samples) == 0 {
		summary.Quality = "down"
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
	summary.Quality = classifyQuality(summary)
	return summary
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

func classifyQuality(summary Summary) string {
	if summary.Succeeded == 0 {
		return "down"
	}
	switch {
	case summary.LossRate <= 0.05 && summary.AvgLatencyMS <= 80:
		return "excellent"
	case summary.LossRate <= 0.10 && summary.AvgLatencyMS <= 200:
		return "good"
	case summary.LossRate <= 0.20 && summary.AvgLatencyMS <= 400:
		return "fair"
	default:
		return "poor"
	}
}

func roundMetric(value float64) float64 {
	return math.Round(value*10) / 10
}
