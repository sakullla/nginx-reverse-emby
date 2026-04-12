# Lego Issuance And Renewal Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `acme.sh` with Go-native lego issuance and automatic renewal for the current HTTP-01 and Cloudflare DNS-01 certificate paths.

**Architecture:** Keep certificate material handling in `go-agent/internal/certs` and add explicit issuance state + renewal scheduling instead of shelling out. Local HTTP-01 renewal runs inside the runtime process; centralized Cloudflare DNS-01 issuance and renewal runs inside `panel/backend-go` and writes the same managed-certificate state back into the shared store.

**Tech Stack:** Go 1.24, lego v4, existing `go-agent/internal/certs`, `panel/backend-go`, cert material provider.

---

## File Map

**Create**
- `go-agent/internal/certs/renewal.go`
- `go-agent/internal/certs/renewal_test.go`
- `go-agent/internal/certs/acme_state.go`
- `panel/backend-go/internal/controlplane/service/cert_renewal.go`
- `panel/backend-go/internal/controlplane/service/certs_test.go`

**Modify**
- `go-agent/internal/certs/manager.go`
- `go-agent/internal/certs/acme_lego.go`
- `panel/backend-go/internal/controlplane/service/certs.go`
- `go-agent/internal/model/certificates.go`

### Task 1: Persist ACME account and renewal metadata in Go-managed state

**Files:**
- Create: `go-agent/internal/certs/acme_state.go`
- Modify: `go-agent/internal/model/certificates.go`
- Test: `go-agent/internal/certs/manager_test.go`

- [ ] **Step 1: Write the failing state round-trip test**

```go
func TestManagedCertificateStateRoundTrip(t *testing.T) {
	state := ACMEState{
		DirectoryURL:  "https://acme-v02.api.letsencrypt.org/directory",
		AccountKeyPEM: []byte("account-key"),
		NextRenewalAt: time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC),
	}
	raw, err := MarshalACMEState(state)
	if err != nil {
		t.Fatalf("MarshalACMEState() error = %v", err)
	}
	got, err := UnmarshalACMEState(raw)
	if err != nil || got.DirectoryURL != state.DirectoryURL {
		t.Fatalf("UnmarshalACMEState() = %+v, %v", got, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd go-agent && go test ./internal/certs -run TestManagedCertificateStateRoundTrip -v`
Expected: FAIL with `undefined: ACMEState`

- [ ] **Step 3: Add the Go-managed ACME state model**

```go
type ACMEState struct {
	DirectoryURL    string    `json:"directory_url"`
	AccountKeyPEM   []byte    `json:"account_key_pem"`
	RegistrationURI string    `json:"registration_uri"`
	LastIssuedAt    time.Time `json:"last_issued_at"`
	NextRenewalAt   time.Time `json:"next_renewal_at"`
	LastError       string    `json:"last_error,omitempty"`
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd go-agent && go test ./internal/certs -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add go-agent/internal/certs/acme_state.go go-agent/internal/model/certificates.go go-agent/internal/certs/manager_test.go
git commit -m "feat(certs): persist go-managed acme state"
```

### Task 2: Add automatic local HTTP-01 renewal inside the runtime

**Files:**
- Create: `go-agent/internal/certs/renewal.go`
- Create: `go-agent/internal/certs/renewal_test.go`
- Modify: `go-agent/internal/certs/manager.go`

- [ ] **Step 1: Write the failing renewal scheduler test**

```go
func TestRenewalLoopRenewsExpiredLocalHTTP01Certificate(t *testing.T) {
	type fakeClock struct{ now time.Time }
	func (f fakeClock) Now() time.Time { return f.now }
	type fakeManager struct{ renewCalls int }
	func (f *fakeManager) Renew(context.Context, ManagedCertificateBundle) error { f.renewCalls++; return nil }

	manager := &fakeManager{}
	scheduler := NewRenewalScheduler(manager, fakeClock{now: time.Date(2026, 4, 25, 0, 0, 0, 0, time.UTC)})
	err := scheduler.RunOnce(t.Context(), []ManagedCertificateBundle{{
		Domain: "media.example.com",
		ACME:   ACMEState{NextRenewalAt: time.Date(2026, 4, 24, 0, 0, 0, 0, time.UTC)},
	}})
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if manager.renewCalls != 1 {
		t.Fatalf("renewCalls = %d", manager.renewCalls)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd go-agent && go test ./internal/certs -run TestRenewalLoopRenewsExpiredLocalHTTP01Certificate -v`
Expected: FAIL with `undefined: NewRenewalScheduler`

- [ ] **Step 3: Implement the renewal scheduler**

```go
type RenewalScheduler struct {
	manager Manager
	clock   Clock
}

func (s *RenewalScheduler) RunOnce(ctx context.Context, certs []ManagedCertificateBundle) error {
	now := s.clock.Now()
	for _, cert := range certs {
		if cert.ACME.NextRenewalAt.IsZero() || cert.ACME.NextRenewalAt.After(now) {
			continue
		}
		if err := s.manager.Renew(ctx, cert); err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd go-agent && go test ./internal/certs -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add go-agent/internal/certs/renewal.go go-agent/internal/certs/renewal_test.go go-agent/internal/certs/manager.go
git commit -m "feat(certs): add automatic local http-01 renewal"
```

### Task 3: Add centralized Cloudflare DNS-01 issuance and renewal in the control-plane

**Files:**
- Create: `panel/backend-go/internal/controlplane/service/cert_renewal.go`
- Modify: `panel/backend-go/internal/controlplane/service/certs.go`
- Test: `panel/backend-go/internal/controlplane/service/certs_test.go`

- [ ] **Step 1: Write the failing control-plane renewal test**

```go
func TestCertificateServiceRenewsCloudflareManagedCert(t *testing.T) {
	type fakeCertStore struct{ certs []ManagedCertificate }
	func (f *fakeCertStore) ListManagedCertificates(context.Context) ([]ManagedCertificate, error) { return f.certs, nil }
	func (f *fakeCertStore) SaveIssuedCertificate(context.Context, ManagedCertificate) error { return nil }
	func (f *fakeCertStore) MarkCertificateError(context.Context, int64, string) error { return nil }
	type fakeIssuer struct{ issueCalls int }
	func (f *fakeIssuer) Issue(context.Context, ManagedCertificate) (ManagedCertificate, error) { f.issueCalls++; return ManagedCertificate{}, nil }

	store := &fakeCertStore{certs: []ManagedCertificate{{ID: 10, Domain: "media.example.com", IssuerMode: "master_cf_dns", Enabled: true}}}
	issuer := &fakeIssuer{}
	svc := NewCertificateService(store, issuer)
	err := svc.RunRenewalPass(t.Context())
	if err != nil {
		t.Fatalf("RunRenewalPass() error = %v", err)
	}
	if issuer.issueCalls != 1 {
		t.Fatalf("issueCalls = %d", issuer.issueCalls)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd panel/backend-go && go test ./internal/controlplane/service -run TestCertificateServiceRenewsCloudflareManagedCert -v`
Expected: FAIL with `RunRenewalPass undefined`

- [ ] **Step 3: Implement renewal pass and state write-back**

```go
func (s *CertificateService) RunRenewalPass(ctx context.Context) error {
	certs, err := s.store.ListManagedCertificates(ctx)
	if err != nil {
		return err
	}
	for _, cert := range certs {
		if cert.IssuerMode != "master_cf_dns" || !cert.Enabled || !needsRenewal(cert) {
			continue
		}
		updated, err := s.issuer.Issue(ctx, cert)
		if err != nil {
			_ = s.store.MarkCertificateError(ctx, cert.ID, err.Error())
			return err
		}
		if err := s.store.SaveIssuedCertificate(ctx, updated); err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd panel/backend-go && go test ./internal/controlplane/service -v`
Expected: PASS

Run: `cd ..\\..\\go-agent && go test ./internal/certs -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add panel/backend-go/internal/controlplane/service/cert_renewal.go panel/backend-go/internal/controlplane/service/certs.go panel/backend-go/internal/controlplane/service/certs_test.go
git commit -m "feat(control-plane): add centralized dns-01 issuance and renewal"
```
