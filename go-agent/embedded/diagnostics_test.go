package embedded

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestDiagnoseSnapshotReturnsSampleArrayForHTTP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	result, err := DiagnoseSnapshot(context.Background(), "", Snapshot{
		Rules: []HTTPRule{{
			ID:          7,
			FrontendURL: "https://media.example.com",
			BackendURL:  server.URL,
			Backends:    []HTTPBackend{{URL: server.URL}},
		}},
	}, DiagnosticRequest{TaskType: "diagnose_http_rule", RuleID: 7})
	if err != nil {
		t.Fatalf("DiagnoseSnapshot() error = %v", err)
	}

	samples := reflect.ValueOf(result["samples"])
	if samples.Kind() != reflect.Slice {
		t.Fatalf("samples kind = %s, want slice", samples.Kind())
	}
	if samples.Len() != 5 {
		t.Fatalf("samples len = %d, want 5", samples.Len())
	}
}
