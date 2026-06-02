package diagnostics

import "testing"

func TestBuildReportSummarizesLatencyAndLoss(t *testing.T) {
	report := BuildReport("http", 7, []Sample{
		{Attempt: 1, Success: true, LatencyMS: 10},
		{Attempt: 2, Success: true, LatencyMS: 30},
		{Attempt: 3, Success: false, Error: "timeout"},
	})

	if report.Summary.Sent != 3 {
		t.Fatalf("Sent = %d", report.Summary.Sent)
	}
	if report.Summary.Succeeded != 2 {
		t.Fatalf("Succeeded = %d", report.Summary.Succeeded)
	}
	if report.Summary.Failed != 1 {
		t.Fatalf("Failed = %d", report.Summary.Failed)
	}
	if report.Summary.LossRate != 0.3 {
		t.Fatalf("LossRate = %v", report.Summary.LossRate)
	}
	if report.Summary.AvgLatencyMS != 20 {
		t.Fatalf("AvgLatencyMS = %v", report.Summary.AvgLatencyMS)
	}
	if report.Summary.MinLatencyMS != 10 {
		t.Fatalf("MinLatencyMS = %v", report.Summary.MinLatencyMS)
	}
	if report.Summary.MaxLatencyMS != 30 {
		t.Fatalf("MaxLatencyMS = %v", report.Summary.MaxLatencyMS)
	}
	if report.Summary.Quality != "较差" {
		t.Fatalf("Quality = %q", report.Summary.Quality)
	}
}

func TestBuildReportMarksDownWhenAllProbesFail(t *testing.T) {
	report := BuildReport("l4_tcp", 9, []Sample{
		{Attempt: 1, Success: false, Error: "dial tcp timeout"},
		{Attempt: 2, Success: false, Error: "dial tcp timeout"},
	})

	if report.Summary.LossRate != 1 {
		t.Fatalf("LossRate = %v", report.Summary.LossRate)
	}
	if report.Summary.Quality != "不可用" {
		t.Fatalf("Quality = %q", report.Summary.Quality)
	}
	if report.Summary.AvgLatencyMS != 0 {
		t.Fatalf("AvgLatencyMS = %v", report.Summary.AvgLatencyMS)
	}
}

func TestBuildReportIncludesPerBackendSummaries(t *testing.T) {
	report := BuildReport("http", 7, []Sample{
		{Attempt: 1, Backend: "http://backend-a/healthz", Success: true, LatencyMS: 10},
		{Attempt: 2, Backend: "http://backend-a/healthz", Success: false, Error: "timeout"},
		{Attempt: 3, Backend: "http://backend-b/healthz", Success: true, LatencyMS: 30},
		{Attempt: 4, Backend: "http://backend-b/healthz", Success: true, LatencyMS: 50},
	})

	if len(report.Backends) != 2 {
		t.Fatalf("Backends = %+v", report.Backends)
	}
	if report.Backends[0].Backend != "http://backend-a/healthz" {
		t.Fatalf("first backend = %+v", report.Backends[0])
	}
	if report.Backends[0].Summary.Sent != 2 || report.Backends[0].Summary.Succeeded != 1 || report.Backends[0].Summary.Failed != 1 {
		t.Fatalf("first backend summary = %+v", report.Backends[0].Summary)
	}
	if report.Backends[1].Backend != "http://backend-b/healthz" {
		t.Fatalf("second backend = %+v", report.Backends[1])
	}
	if report.Backends[1].Summary.Sent != 2 || report.Backends[1].Summary.Succeeded != 2 || report.Backends[1].Summary.AvgLatencyMS != 40 {
		t.Fatalf("second backend summary = %+v", report.Backends[1].Summary)
	}
}

func TestMergeBackendSummariesWeightsLatencyBySuccessfulSamples(t *testing.T) {
	summary := mergeBackendSummaries("http", []Summary{
		{Sent: 3, Succeeded: 2, Failed: 1, AvgLatencyMS: 20, MinLatencyMS: 10, MaxLatencyMS: 30},
		{Sent: 2, Succeeded: 1, Failed: 1, AvgLatencyMS: 100, MinLatencyMS: 100, MaxLatencyMS: 100},
		{Sent: 1, Failed: 1},
	})

	if summary.Sent != 6 || summary.Succeeded != 3 || summary.Failed != 3 {
		t.Fatalf("counts = %+v", summary)
	}
	if summary.LossRate != 0.5 {
		t.Fatalf("LossRate = %v", summary.LossRate)
	}
	if summary.AvgLatencyMS != 46.7 {
		t.Fatalf("AvgLatencyMS = %v", summary.AvgLatencyMS)
	}
	if summary.MinLatencyMS != 10 || summary.MaxLatencyMS != 100 {
		t.Fatalf("latency range = %+v", summary)
	}
	if summary.Quality != "较差" {
		t.Fatalf("Quality = %q", summary.Quality)
	}
}

func TestMergeBackendSummariesUsesKindQualityThresholds(t *testing.T) {
	summaries := []Summary{
		{Sent: 1, Succeeded: 1, AvgLatencyMS: 100, MinLatencyMS: 100, MaxLatencyMS: 100},
	}

	httpSummary := mergeBackendSummaries("http", summaries)
	if httpSummary.Quality != "极佳" {
		t.Fatalf("http quality = %q", httpSummary.Quality)
	}

	tcpSummary := mergeBackendSummaries("l4_tcp", summaries)
	if tcpSummary.Quality != "良好" {
		t.Fatalf("tcp quality = %q", tcpSummary.Quality)
	}
}
