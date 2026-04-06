package sync

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestHeartbeatSync(t *testing.T) {
	type captured struct {
		Method string
		Path   string
		Header http.Header
		Body   []byte
	}
	reqs := make(chan captured, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		reqs <- captured{
			Method: r.Method,
			Path:   r.URL.Path,
			Header: r.Header.Clone(),
			Body:   body,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(struct {
			Sync struct {
				DesiredVersion      string                           `json:"desired_version"`
				DesiredRevision     int64                            `json:"desired_revision"`
				Certificates        []model.ManagedCertificateBundle `json:"certificates"`
				CertificatePolicies []model.ManagedCertificatePolicy `json:"certificate_policies"`
			} `json:"sync"`
		}{Sync: struct {
			DesiredVersion      string                           `json:"desired_version"`
			DesiredRevision     int64                            `json:"desired_revision"`
			Certificates        []model.ManagedCertificateBundle `json:"certificates"`
			CertificatePolicies []model.ManagedCertificatePolicy `json:"certificate_policies"`
		}{
			DesiredVersion:  "1.2.3",
			DesiredRevision: 42,
			Certificates: []model.ManagedCertificateBundle{{
				ID:       21,
				Domain:   "sync.example.com",
				Revision: 3,
				CertPEM:  "CERTIFICATE",
				KeyPEM:   "PRIVATEKEY",
			}},
			CertificatePolicies: []model.ManagedCertificatePolicy{{
				ID:              21,
				Domain:          "sync.example.com",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "local_http01",
				Status:          "issued",
				Revision:        3,
				Usage:           "relay_ca",
				CertificateType: "internal_ca",
				SelfSigned:      true,
			}},
		}})
	}))
	defer server.Close()

	client := NewClient(ClientConfig{
		MasterURL:      server.URL,
		AgentToken:     "token",
		AgentID:        "node",
		AgentName:      "local",
		CurrentVersion: "0.1.0",
		Platform:       "linux-amd64",
	}, server.Client())

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	snap, err := client.Sync(ctx, SyncRequest{CurrentRevision: 42})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if snap.DesiredVersion != "1.2.3" {
		t.Fatalf("expected desired_version=1.2.3, got %q", snap.DesiredVersion)
	}
	if snap.Revision != 42 {
		t.Fatalf("expected revision=42, got %d", snap.Revision)
	}
	if !reflect.DeepEqual(snap.Certificates, []model.ManagedCertificateBundle{{
		ID:       21,
		Domain:   "sync.example.com",
		Revision: 3,
		CertPEM:  "CERTIFICATE",
		KeyPEM:   "PRIVATEKEY",
	}}) {
		t.Fatalf("unexpected certificates payload: %+v", snap.Certificates)
	}
	if !reflect.DeepEqual(snap.CertificatePolicies, []model.ManagedCertificatePolicy{{
		ID:              21,
		Domain:          "sync.example.com",
		Enabled:         true,
		Scope:           "domain",
		IssuerMode:      "local_http01",
		Status:          "issued",
		LastIssueAt:     "",
		LastError:       "",
		ACMEInfo:        model.ManagedCertificateACMEInfo{},
		Tags:            nil,
		Revision:        3,
		Usage:           "relay_ca",
		CertificateType: "internal_ca",
		SelfSigned:      true,
	}}) {
		t.Fatalf("unexpected certificate_policies payload: %+v", snap.CertificatePolicies)
	}

	select {
	case req := <-reqs:
		if req.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", req.Method)
		}
		if req.Path != "/api/agents/heartbeat" {
			t.Fatalf("expected heartbeat path, got %s", req.Path)
		}
		if req.Header.Get("x-agent-token") != "token" {
			t.Fatal("missing x-agent-token header")
		}
		if !bytes.Contains(req.Body, []byte(`"current_revision"`)) {
			t.Fatalf("expected current_revision in heartbeat payload")
		}
		var payload struct {
			Name            string `json:"name"`
			CurrentRevision int    `json:"current_revision"`
			Version         string `json:"version"`
			Platform        string `json:"platform"`
		}
		if err := json.NewDecoder(bytes.NewReader(req.Body)).Decode(&payload); err != nil {
			t.Fatalf("failed to decode payload: %v", err)
		}
		if payload.Name != "local" || payload.Version != "0.1.0" || payload.Platform != "linux-amd64" {
			t.Fatalf("unexpected payload %+v", payload)
		}
		if payload.CurrentRevision != 42 {
			t.Fatalf("expected current_revision=42, got %d", payload.CurrentRevision)
		}
	case <-ctx.Done():
		t.Fatalf("heartbeat not sent")
	}
}

func TestHeartbeatSyncPreservesOmittedCertificatePayloadAsNil(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"sync":{"desired_version":"1.2.3","desired_revision":7}}`)
	}))
	defer server.Close()

	client := NewClient(ClientConfig{
		MasterURL:      server.URL,
		AgentToken:     "token",
		AgentID:        "node",
		AgentName:      "local",
		CurrentVersion: "0.1.0",
		Platform:       "linux-amd64",
	}, server.Client())

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	snap, err := client.Sync(ctx, SyncRequest{CurrentRevision: 42})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if snap.Certificates != nil {
		t.Fatalf("expected nil certificates for omitted payload, got %+v", snap.Certificates)
	}
	if snap.CertificatePolicies != nil {
		t.Fatalf("expected nil certificate policies for omitted payload, got %+v", snap.CertificatePolicies)
	}
}

func TestHeartbeatSyncPreservesExplicitEmptyCertificatePayloads(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"sync":{"desired_version":"1.2.3","desired_revision":7,"certificates":[],"certificate_policies":[]}}`)
	}))
	defer server.Close()

	client := NewClient(ClientConfig{
		MasterURL:      server.URL,
		AgentToken:     "token",
		AgentID:        "node",
		AgentName:      "local",
		CurrentVersion: "0.1.0",
		Platform:       "linux-amd64",
	}, server.Client())

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	snap, err := client.Sync(ctx, SyncRequest{CurrentRevision: 42})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if snap.Certificates == nil || len(snap.Certificates) != 0 {
		t.Fatalf("expected explicit empty certificates slice, got %+v", snap.Certificates)
	}
	if snap.CertificatePolicies == nil || len(snap.CertificatePolicies) != 0 {
		t.Fatalf("expected explicit empty certificate policies slice, got %+v", snap.CertificatePolicies)
	}
}
