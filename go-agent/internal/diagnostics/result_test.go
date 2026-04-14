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
	if report.Summary.Quality != "poor" {
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
	if report.Summary.Quality != "down" {
		t.Fatalf("Quality = %q", report.Summary.Quality)
	}
	if report.Summary.AvgLatencyMS != 0 {
		t.Fatalf("AvgLatencyMS = %v", report.Summary.AvgLatencyMS)
	}
}
