package runtime

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestApplyFailureKeepsPreviousSnapshot(t *testing.T) {
	ctx := context.Background()

	failingActivator := func(_ context.Context, previous, next model.Snapshot) error {
		if next.Revision == 2 {
			return errors.New("activation failed")
		}
		return nil
	}

	r := newRuntimeWithActivator(failingActivator)
	initial := model.Snapshot{
		DesiredVersion: "v1",
		Revision:       1,
		Certificates: []model.ManagedCertificateBundle{{
			ID:       1,
			Domain:   "sync.example.com",
			Revision: 1,
			CertPEM:  "CERT",
			KeyPEM:   "KEY",
		}},
	}
	if err := r.Apply(ctx, model.Snapshot{}, initial); err != nil {
		t.Fatalf("priming apply failed: %v", err)
	}

	next := model.Snapshot{DesiredVersion: "v2", Revision: 2}
	if err := r.Apply(ctx, initial, next); err == nil {
		t.Fatalf("expected apply to fail")
	}

	got := r.ActiveSnapshot()
	if !snapshotEqual(got, initial) {
		t.Fatalf("active snapshot mutated on failure: got %+v want %+v", got, initial)
	}

	state := r.State()
	if state.CurrentRevision != initial.Revision {
		t.Fatalf("current revision advanced on failure: got %d want %d", state.CurrentRevision, initial.Revision)
	}

	if state.Status != "error" {
		t.Fatalf("runtime state not error after failure: got %q", state.Status)
	}
}

func TestApplySuccessSwapsSnapshot(t *testing.T) {
	r := New()
	ctx := context.Background()
	first := model.Snapshot{
		DesiredVersion: "stable",
		Revision:       1,
		CertificatePolicies: []model.ManagedCertificatePolicy{{
			ID:              7,
			Domain:          "stable.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			Revision:        1,
			Usage:           "https",
			CertificateType: "uploaded",
		}},
	}
	if err := r.Apply(ctx, model.Snapshot{}, first); err != nil {
		t.Fatalf("priming apply failed: %v", err)
	}

	next := model.Snapshot{
		DesiredVersion: "stable-next",
		Revision:       2,
		CertificatePolicies: []model.ManagedCertificatePolicy{{
			ID:              8,
			Domain:          "next.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			Revision:        2,
			Usage:           "relay_ca",
			CertificateType: "internal_ca",
			SelfSigned:      true,
		}},
	}
	if err := r.Apply(ctx, first, next); err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	if got := r.ActiveSnapshot(); !snapshotEqual(got, next) {
		t.Fatalf("active snapshot not updated on success: got %+v want %+v", got, next)
	}

	state := r.State()
	if state.CurrentRevision != next.Revision {
		t.Fatalf("current revision not advanced: got %d want %d", state.CurrentRevision, next.Revision)
	}

	if state.Status != "active" {
		t.Fatalf("runtime state not active after success: got %q", state.Status)
	}

	if value, ok := state.Metadata["current_revision"]; !ok || value != strconv.FormatInt(next.Revision, 10) {
		t.Fatalf("expected metadata current_revision to match revision, got %q", value)
	}
}

func TestApplyPreviousMismatchReportsError(t *testing.T) {
	r := New()
	ctx := context.Background()
	base := model.Snapshot{
		DesiredVersion: "base",
		Revision:       1,
		Certificates: []model.ManagedCertificateBundle{{
			ID:       9,
			Domain:   "base.example.com",
			Revision: 1,
			CertPEM:  "CERT",
			KeyPEM:   "KEY",
		}},
	}
	if err := r.Apply(ctx, model.Snapshot{}, base); err != nil {
		t.Fatalf("priming apply failed: %v", err)
	}

	prev := model.Snapshot{DesiredVersion: "mismatch", Revision: 999}
	if err := r.Apply(ctx, prev, model.Snapshot{DesiredVersion: "next", Revision: 2}); err == nil {
		t.Fatalf("expected previous mismatch to return error")
	}

	state := r.State()
	if value, ok := state.Metadata["current_revision"]; !ok || value != strconv.FormatInt(base.Revision, 10) {
		t.Fatalf("current revision metadata changed after mismatch: %v", state.Metadata["current_revision"])
	}

	if state.Status != "error" {
		t.Fatalf("runtime state not error after mismatch: got %q", state.Status)
	}
}

func TestStateReturnsMetadataCopy(t *testing.T) {
	r := New()
	ctx := context.Background()
	initial := model.Snapshot{
		DesiredVersion: "copy",
		Revision:       1,
		Rules: []model.HTTPRule{{
			FrontendURL: "https://frontend.example.com",
			BackendURL:  "http://127.0.0.1:8096",
			CustomHeaders: []model.HTTPHeader{{
				Name:  "X-Test",
				Value: "one",
			}},
			Revision: 1,
		}},
		L4Rules: []model.L4Rule{{
			Protocol:     "tcp",
			ListenHost:   "127.0.0.1",
			ListenPort:   9000,
			UpstreamHost: "127.0.0.1",
			UpstreamPort: 9001,
			RelayChain:   []int{1, 2},
			Revision:     1,
		}},
		RelayListeners: []model.RelayListener{{
			ID:         10,
			AgentID:    "agent-a",
			Name:       "relay-a",
			ListenHost: "127.0.0.1",
			ListenPort: 9443,
			Enabled:    true,
			TLSMode:    "pin_only",
			PinSet: []model.RelayPin{{
				Type:  "sha256",
				Value: "pin-one",
			}},
			TrustedCACertificateIDs: []int{7},
			Tags:                    []string{"tag-one"},
			Revision:                1,
		}},
		Certificates: []model.ManagedCertificateBundle{{
			ID:       3,
			Domain:   "copy.example.com",
			Revision: 1,
			CertPEM:  "CERT",
			KeyPEM:   "KEY",
		}},
	}
	if err := r.Apply(ctx, model.Snapshot{}, initial); err != nil {
		t.Fatalf("priming apply failed: %v", err)
	}

	state := r.State()
	state.Metadata["leak"] = "mutated"

	second := r.State()
	if _, ok := second.Metadata["leak"]; ok {
		t.Fatalf("metadata copy leaked: %v", second.Metadata)
	}
}

func TestActiveSnapshotReturnsSliceIsolation(t *testing.T) {
	r := New()
	ctx := context.Background()
	initial := model.Snapshot{
		DesiredVersion: "copy",
		Revision:       1,
		Rules: []model.HTTPRule{{
			FrontendURL: "https://frontend.example.com",
			BackendURL:  "http://127.0.0.1:8096",
			CustomHeaders: []model.HTTPHeader{{
				Name:  "X-Test",
				Value: "one",
			}},
			Revision: 1,
		}},
		L4Rules: []model.L4Rule{{
			Protocol:     "tcp",
			ListenHost:   "127.0.0.1",
			ListenPort:   9000,
			UpstreamHost: "127.0.0.1",
			UpstreamPort: 9001,
			RelayChain:   []int{1, 2},
			Revision:     1,
		}},
		RelayListeners: []model.RelayListener{{
			ID:         10,
			AgentID:    "agent-a",
			Name:       "relay-a",
			ListenHost: "127.0.0.1",
			ListenPort: 9443,
			Enabled:    true,
			TLSMode:    "pin_only",
			PinSet: []model.RelayPin{{
				Type:  "sha256",
				Value: "pin-one",
			}},
			TrustedCACertificateIDs: []int{7},
			Tags:                    []string{"tag-one"},
			Revision:                1,
		}},
		Certificates: []model.ManagedCertificateBundle{{
			ID:       3,
			Domain:   "copy.example.com",
			Revision: 1,
			CertPEM:  "CERT",
			KeyPEM:   "KEY",
		}},
		CertificatePolicies: []model.ManagedCertificatePolicy{{
			ID:              4,
			Domain:          "policy.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			Revision:        1,
			Usage:           "https",
			CertificateType: "uploaded",
			Tags:            []string{"one"},
		}},
	}
	if err := r.Apply(ctx, model.Snapshot{}, initial); err != nil {
		t.Fatalf("priming apply failed: %v", err)
	}

	snap := r.ActiveSnapshot()
	snap.Rules[0].CustomHeaders[0].Value = "mutated"
	snap.L4Rules[0].RelayChain[0] = 99
	snap.RelayListeners[0].PinSet[0].Value = "mutated"
	snap.RelayListeners[0].TrustedCACertificateIDs[0] = 99
	snap.RelayListeners[0].Tags[0] = "mutated"
	snap.Certificates[0].Domain = "mutated.example.com"
	snap.CertificatePolicies[0].Tags[0] = "mutated"

	current := r.ActiveSnapshot()
	if current.Rules[0].CustomHeaders[0].Value != "one" {
		t.Fatalf("http rule slice leaked mutation: %+v", current.Rules)
	}
	if current.L4Rules[0].RelayChain[0] != 1 {
		t.Fatalf("l4 relay_chain leaked mutation: %+v", current.L4Rules)
	}
	if current.RelayListeners[0].PinSet[0].Value != "pin-one" {
		t.Fatalf("relay pin_set leaked mutation: %+v", current.RelayListeners)
	}
	if current.RelayListeners[0].TrustedCACertificateIDs[0] != 7 {
		t.Fatalf("relay trusted ca leaked mutation: %+v", current.RelayListeners)
	}
	if current.RelayListeners[0].Tags[0] != "tag-one" {
		t.Fatalf("relay tags leaked mutation: %+v", current.RelayListeners)
	}
	if current.Certificates[0].Domain != "copy.example.com" {
		t.Fatalf("certificate slice leaked mutation: %+v", current.Certificates)
	}
	if current.CertificatePolicies[0].Tags[0] != "one" {
		t.Fatalf("policy tags leaked mutation: %+v", current.CertificatePolicies)
	}
}

func TestApplyMismatchErrorRedactsCertificateMaterial(t *testing.T) {
	r := New()
	ctx := context.Background()
	base := model.Snapshot{
		DesiredVersion: "base",
		Revision:       1,
		Certificates: []model.ManagedCertificateBundle{{
			ID:       9,
			Domain:   "base.example.com",
			Revision: 1,
			CertPEM:  "SECRET_CERT",
			KeyPEM:   "SECRET_KEY",
		}},
	}
	if err := r.Apply(ctx, model.Snapshot{}, base); err != nil {
		t.Fatalf("priming apply failed: %v", err)
	}

	err := r.Apply(ctx, model.Snapshot{DesiredVersion: "mismatch", Revision: 999}, model.Snapshot{})
	if err == nil {
		t.Fatal("expected mismatch error")
	}
	if strings.Contains(err.Error(), "SECRET_CERT") || strings.Contains(err.Error(), "SECRET_KEY") {
		t.Fatalf("mismatch error leaked certificate material: %v", err)
	}
}

func TestActiveSnapshotPreservesExplicitEmptySlices(t *testing.T) {
	r := New()
	ctx := context.Background()
	initial := model.Snapshot{
		DesiredVersion:      "empty",
		Revision:            1,
		Certificates:        []model.ManagedCertificateBundle{},
		CertificatePolicies: []model.ManagedCertificatePolicy{},
	}
	if err := r.Apply(ctx, model.Snapshot{}, initial); err != nil {
		t.Fatalf("priming apply failed: %v", err)
	}

	snap := r.ActiveSnapshot()
	if snap.Certificates == nil || len(snap.Certificates) != 0 {
		t.Fatalf("expected explicit empty certificates slice, got %+v", snap.Certificates)
	}
	if snap.CertificatePolicies == nil || len(snap.CertificatePolicies) != 0 {
		t.Fatalf("expected explicit empty certificate policies slice, got %+v", snap.CertificatePolicies)
	}
}
