package pki

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

var errEnrollmentContractInjected = errors.New("injected enrollment contract persistence failure")

func TestPrepareEnrollmentReplaysAfterImmediateProcessReopen(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	dataRoot := t.TempDir()
	failed := false
	store, err := NewStore(dataRoot, WithClock(func() time.Time { return now }), withPersistenceCheckpoint(func(point string) error {
		if point == "enrollment.after_publish" && !failed {
			failed = true
			return errEnrollmentContractInjected
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	spec := EnrollmentSpec{StorageIdentity: "agent", Kind: model.PKIIdentityKindAgent, Purpose: model.PKICertificatePurposeClient}
	if _, err := store.PrepareEnrollment(context.Background(), spec); !errors.Is(err, errEnrollmentContractInjected) {
		t.Fatalf("PrepareEnrollment() error = %v, want injected failure", err)
	}
	keyPath := filepath.Join(store.Root(), identitiesDirName, "agent", pendingDirName, pendingKeyName)
	keyBefore, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}

	reopened, err := NewStore(dataRoot, WithClock(func() time.Time { return now.Add(time.Minute) }))
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := reopened.PrepareEnrollment(context.Background(), spec)
	if err != nil {
		t.Fatalf("reopened PrepareEnrollment() error = %v", err)
	}
	keyAfter, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(keyBefore, keyAfter) || replayed.Request.RequestID == "" || replayed.Request.CSRPEM == "" {
		t.Fatal("process reopen did not preserve the published enrollment key and request")
	}
}

func TestDNSNormalizationIsIdempotentAcrossProcessReopen(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	dataRoot := t.TempDir()
	store, err := NewStore(dataRoot, WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	spec := EnrollmentSpec{
		StorageIdentity: "listener_1", DomainID: "domain-1", AgentID: "agent-1",
		Kind: model.PKIIdentityKindListener, ListenerID: "listener-1", Purpose: model.PKICertificatePurposeServer,
		DNSNames: []string{"Relay.Example.."},
	}
	prepared, err := store.PrepareEnrollment(context.Background(), spec)
	if err != nil {
		t.Fatalf("PrepareEnrollment(double trailing dot) error = %v", err)
	}
	if !slices.Equal(prepared.Request.DNSNames, []string{"relay.example"}) {
		t.Fatalf("normalized DNS names = %v", prepared.Request.DNSNames)
	}

	reopened, err := NewStore(dataRoot, WithClock(func() time.Time { return now.Add(time.Minute) }))
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := reopened.LoadPending("listener_1")
	if err != nil || loaded.Request.RequestID != prepared.Request.RequestID || !slices.Equal(loaded.Request.DNSNames, []string{"relay.example"}) {
		t.Fatalf("LoadPending() after reopen = %+v, error = %v", loaded, err)
	}
	replayed, err := reopened.PrepareEnrollment(context.Background(), spec)
	if err != nil || replayed.Request.RequestID != prepared.Request.RequestID || replayed.Request.CSRPEM != prepared.Request.CSRPEM {
		t.Fatalf("replayed double-dot enrollment = %+v, error = %v", replayed, err)
	}
	for _, invalid := range []string{".", "..", "relay..example"} {
		if _, err := normalizeDNSNames([]string{invalid}); !errors.Is(err, ErrInvalidIdentity) {
			t.Fatalf("normalizeDNSNames(%q) error = %v, want ErrInvalidIdentity", invalid, err)
		}
	}
}

func TestRejectPendingEnrollmentQuarantinesKeyAndIsIdempotent(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	store := newTestStore(t, now)
	spec := EnrollmentSpec{
		StorageIdentity: "agent", DomainID: "domain-1", AgentID: "agent-1",
		Kind: model.PKIIdentityKindAgent, Purpose: model.PKICertificatePurposeClient,
	}
	pending, err := store.PrepareEnrollment(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	pendingRoot := filepath.Join(store.Root(), identitiesDirName, "agent", pendingDirName)
	keyBefore, err := os.ReadFile(filepath.Join(pendingRoot, pendingKeyName))
	if err != nil {
		t.Fatal(err)
	}
	wrongRequestID := strings.Repeat("f", 32)
	if wrongRequestID == pending.Request.RequestID {
		wrongRequestID = strings.Repeat("e", 32)
	}
	if err := store.RejectPendingEnrollment("agent", pending.Request.RequestID, ""); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("empty rejection code error = %v, want ErrCredentialInvalid", err)
	}
	if err := store.RejectPendingEnrollment("agent", wrongRequestID, "invalid_csr"); !errors.Is(err, ErrPendingConflict) {
		t.Fatalf("wrong request rejection error = %v, want ErrPendingConflict", err)
	}
	if loaded, err := store.LoadPending("agent"); err != nil || loaded.Request.RequestID != pending.Request.RequestID {
		t.Fatalf("failed rejection changed pending request: %+v, error = %v", loaded, err)
	}

	if err := store.RejectPendingEnrollment("agent", pending.Request.RequestID, "invalid_csr"); err != nil {
		t.Fatalf("RejectPendingEnrollment() error = %v", err)
	}
	if _, err := store.LoadPending("agent"); !errors.Is(err, ErrPendingNotFound) {
		t.Fatalf("LoadPending() after rejection error = %v, want ErrPendingNotFound", err)
	}
	if replayable, err := store.PendingEnrollments(); err != nil || len(replayable) != 0 {
		t.Fatalf("PendingEnrollments() after rejection = %+v, error = %v", replayable, err)
	}
	rejectedRoot := filepath.Join(store.Root(), identitiesDirName, "agent", rejectedEnrollmentsDirName, pending.Request.RequestID, "invalid_csr")
	keyAfter, err := os.ReadFile(filepath.Join(rejectedRoot, pendingKeyName))
	if err != nil {
		t.Fatalf("read quarantined key: %v", err)
	}
	if !bytes.Equal(keyBefore, keyAfter) {
		t.Fatal("terminal rejection did not preserve the original private key")
	}
	for _, name := range []string{pendingJournalName, pendingCSRName, pendingKeyName} {
		if info, err := os.Lstat(filepath.Join(rejectedRoot, name)); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("quarantined %s is unavailable or unsafe: info=%v error=%v", name, info, err)
		}
	}
	if err := store.RejectPendingEnrollment("agent", pending.Request.RequestID, "invalid_csr"); err != nil {
		t.Fatalf("idempotent RejectPendingEnrollment() error = %v", err)
	}
	if err := store.RejectPendingEnrollment("agent", pending.Request.RequestID, "owner_mismatch"); !errors.Is(err, ErrPendingConflict) {
		t.Fatalf("conflicting rejection code error = %v, want ErrPendingConflict", err)
	}

	replacement, err := store.PrepareEnrollment(context.Background(), spec)
	if err != nil {
		t.Fatalf("PrepareEnrollment() after terminal rejection error = %v", err)
	}
	if replacement.Request.RequestID == pending.Request.RequestID || replacement.Request.CSRPEM == pending.Request.CSRPEM {
		t.Fatal("terminal rejection did not permit a fresh enrollment key and request")
	}
	if preserved, err := os.ReadFile(filepath.Join(rejectedRoot, pendingKeyName)); err != nil || !bytes.Equal(preserved, keyBefore) {
		t.Fatalf("fresh enrollment damaged quarantined key: error = %v", err)
	}
}

func TestRejectPendingEnrollmentReconcilesAfterCommittedProcessLoss(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	dataRoot := t.TempDir()
	failed := false
	store, err := NewStore(dataRoot, WithClock(func() time.Time { return now }), withPersistenceCheckpoint(func(point string) error {
		if point == "enrollment.after_rejection_publish" && !failed {
			failed = true
			return errEnrollmentContractInjected
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	pending, err := store.PrepareEnrollment(context.Background(), EnrollmentSpec{StorageIdentity: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RejectPendingEnrollment("agent", pending.Request.RequestID, "invalid_csr"); !errors.Is(err, errEnrollmentContractInjected) {
		t.Fatalf("RejectPendingEnrollment() error = %v, want injected committed failure", err)
	}

	synced := make([]string, 0, 4)
	reopened, err := NewStore(dataRoot, WithClock(func() time.Time { return now.Add(time.Minute) }), withDirectorySync(func(path string) error {
		synced = append(synced, path)
		return syncDirectory(path)
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.RejectPendingEnrollment("agent", pending.Request.RequestID, "invalid_csr"); err != nil {
		t.Fatalf("reopened idempotent rejection error = %v", err)
	}
	identityRoot := filepath.Join(reopened.Root(), identitiesDirName, "agent")
	wantSynced := []string{
		filepath.Join(identityRoot, rejectedEnrollmentsDirName, pending.Request.RequestID, "invalid_csr"),
		filepath.Join(identityRoot, rejectedEnrollmentsDirName, pending.Request.RequestID),
		filepath.Join(identityRoot, rejectedEnrollmentsDirName),
		identityRoot,
	}
	if !slices.Equal(synced, wantSynced) {
		t.Fatalf("committed rejection replay syncs = %v, want %v", synced, wantSynced)
	}
	if pending, err := reopened.PendingEnrollments(); err != nil || len(pending) != 0 {
		t.Fatalf("reopened replay set = %+v, error = %v", pending, err)
	}
}

func TestPendingEnrollmentRejectsSymlinkedPendingDirectory(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	external := newTestStore(t, now)
	if _, err := external.PrepareEnrollment(context.Background(), EnrollmentSpec{StorageIdentity: "agent"}); err != nil {
		t.Fatal(err)
	}
	externalPending := filepath.Join(external.Root(), identitiesDirName, "agent", pendingDirName)

	store := newTestStore(t, now)
	if _, err := store.PrepareEnrollment(context.Background(), EnrollmentSpec{StorageIdentity: "agent"}); err != nil {
		t.Fatal(err)
	}
	pendingPath := filepath.Join(store.Root(), identitiesDirName, "agent", pendingDirName)
	if err := os.RemoveAll(pendingPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(externalPending, pendingPath); err != nil {
		t.Skipf("directory symlinks are unavailable on this host: %v", err)
	}
	if _, err := store.LoadPending("agent"); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("LoadPending(symlink) error = %v, want ErrCredentialInvalid", err)
	}
	if _, err := store.PendingEnrollments(); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("PendingEnrollments(symlink) error = %v, want ErrCredentialInvalid", err)
	}
	if _, err := store.PrepareEnrollment(context.Background(), EnrollmentSpec{StorageIdentity: "agent"}); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("PrepareEnrollment(symlink) error = %v, want ErrCredentialInvalid", err)
	}
}

func TestPendingEnrollmentEnforcesControlPlaneCSRContract(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name   string
		mutate func(*x509.CertificateRequest)
	}{
		{name: "extra subject RDN", mutate: func(request *x509.CertificateRequest) {
			request.Subject.Organization = []string{"unexpected"}
		}},
		{name: "email SAN", mutate: func(request *x509.CertificateRequest) {
			request.EmailAddresses = []string{"unexpected@example.test"}
		}},
		{name: "unsupported extension", mutate: func(request *x509.CertificateRequest) {
			request.ExtraExtensions = append(request.ExtraExtensions, pkix.Extension{Id: asn1.ObjectIdentifier{1, 2, 3, 4}, Value: []byte{5, 0}})
		}},
		{name: "wrong signature algorithm", mutate: func(request *x509.CertificateRequest) {
			request.SignatureAlgorithm = x509.ECDSAWithSHA384
		}},
		{name: "duplicate DNS SAN", mutate: func(request *x509.CertificateRequest) {
			request.DNSNames = append(request.DNSNames, request.DNSNames[0])
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newTestStore(t, now)
			spec := EnrollmentSpec{
				StorageIdentity: "listener_1", DomainID: "domain-1", AgentID: "agent-1",
				Kind: model.PKIIdentityKindListener, ListenerID: "listener-1", Purpose: model.PKICertificatePurposeServer,
				DNSNames: []string{"relay.example.test"}, IPAddresses: []string{"192.0.2.20"},
			}
			pending, err := store.PrepareEnrollment(context.Background(), spec)
			if err != nil {
				t.Fatal(err)
			}
			rewritePendingCSRForContract(t, store, spec, pending, test.mutate)
			if _, err := store.LoadPending(spec.StorageIdentity); !errors.Is(err, ErrCredentialInvalid) {
				t.Fatalf("LoadPending() error = %v, want ErrCredentialInvalid", err)
			}
		})
	}
}

func TestPendingEnrollmentCanonicalizesOnlyFinalPEMNewline(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	store := newTestStore(t, now)
	pending, err := store.PrepareEnrollment(context.Background(), EnrollmentSpec{StorageIdentity: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	journalPath := filepath.Join(store.Root(), identitiesDirName, "agent", pendingDirName, pendingJournalName)
	journal, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	var shellStyle PendingEnrollment
	if err := json.Unmarshal(journal, &shellStyle); err != nil {
		t.Fatal(err)
	}
	shellStyle.Request.CSRPEM = strings.TrimSuffix(shellStyle.Request.CSRPEM, "\n")
	writePendingContractJournal(t, journalPath, shellStyle)
	loaded, err := store.LoadPending("agent")
	if err != nil {
		t.Fatalf("LoadPending(shell-style final newline) error = %v", err)
	}
	if !strings.HasSuffix(loaded.Request.CSRPEM, "\n") || loaded.Request.RequestID != pending.Request.RequestID {
		t.Fatalf("canonical loaded pending = %+v", loaded)
	}

	lines := strings.Split(strings.TrimSuffix(shellStyle.Request.CSRPEM, "\n"), "\n")
	if len(lines) < 3 || len(lines[1]) == 0 {
		t.Fatal("generated CSR PEM is unexpectedly short")
	}
	replacement := byte('A')
	if lines[1][0] == replacement {
		replacement = 'B'
	}
	lines[1] = string(replacement) + lines[1][1:]
	shellStyle.Request.CSRPEM = strings.Join(lines, "\n")
	writePendingContractJournal(t, journalPath, shellStyle)
	if _, err := store.LoadPending("agent"); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("LoadPending(changed PEM content) error = %v, want ErrCredentialInvalid", err)
	}
}

func TestShellPendingEnrollmentContract(t *testing.T) {
	t.Parallel()
	rawDataRoot := strings.TrimSpace(os.Getenv("NRE_TEST_SHELL_PKI_DATA_DIR"))
	if rawDataRoot == "" {
		t.Skip("NRE_TEST_SHELL_PKI_DATA_DIR is not set")
	}
	dataRoot := resolveShellContractDataRoot(t, rawDataRoot)
	store, err := NewStore(dataRoot)
	if err != nil {
		t.Fatalf("NewStore(shell data root) error = %v", err)
	}
	pending, err := store.LoadPending("agent")
	if err != nil {
		t.Fatalf("LoadPending(shell artifact) error = %v", err)
	}
	if pending.StorageIdentity != "agent" || len(pending.Request.RequestID) != 32 || !validLowerHex(pending.Request.RequestID) {
		t.Fatalf("shell pending identity/request ID = %+v", pending)
	}
	request, err := parseCSRPEM([]byte(pending.Request.CSRPEM))
	if err != nil || request.CheckSignature() != nil || !strings.HasSuffix(pending.Request.CSRPEM, "\n") {
		t.Fatalf("shell pending CSR is invalid: error = %v", err)
	}

	responsePath := filepath.Join(store.Root(), identitiesDirName, "agent", pendingDirName, "response.json")
	if _, err := os.Lstat(responsePath); errors.Is(err, os.ErrNotExist) {
		return
	} else if err != nil {
		t.Fatalf("inspect shell staged response: %v", err)
	}
	staged, stagedPending, err := store.LoadStagedRegistration("agent")
	if err != nil {
		t.Fatalf("LoadStagedRegistration(shell artifact) error = %v", err)
	}
	if stagedPending.Request.RequestID != pending.Request.RequestID || strings.TrimSpace(staged.AgentID) == "" ||
		strings.TrimSpace(staged.TunnelCredential.CertificateID) == "" || strings.TrimSpace(staged.SecuritySnapshot.PKIDomainID) == "" {
		t.Fatalf("shell staged registration is incomplete: staged=%+v pending=%+v", staged, stagedPending)
	}
	response, err := os.ReadFile(responsePath)
	if err != nil {
		t.Fatal(err)
	}
	canaries := []string{"control-secret", "register-secret", os.Getenv("NRE_TEST_SHELL_CONTROL_TOKEN_CANARY"), os.Getenv("NRE_TEST_SHELL_REGISTER_TOKEN_CANARY")}
	for _, canary := range canaries {
		if canary != "" && bytes.Contains(response, []byte(canary)) {
			t.Fatalf("shell staged response leaked token canary %q: %s", canary, response)
		}
	}
	var projected map[string]json.RawMessage
	if err := json.Unmarshal(response, &projected); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"agent_token", "register_token", "token"} {
		if _, exposed := projected[forbidden]; exposed {
			t.Fatalf("shell staged response exposes forbidden field %q", forbidden)
		}
	}
}

func rewritePendingCSRForContract(t *testing.T, store *Store, spec EnrollmentSpec, pending PendingEnrollment, mutate func(*x509.CertificateRequest)) {
	t.Helper()
	pendingRoot := filepath.Join(store.Root(), identitiesDirName, spec.StorageIdentity, pendingDirName)
	privatePEM, err := os.ReadFile(filepath.Join(pendingRoot, pendingKeyName))
	if err != nil {
		t.Fatal(err)
	}
	privateKey, err := parseECPrivateKeyPEM(privatePEM)
	if err != nil {
		t.Fatal(err)
	}
	identityURI := &url.URL{Scheme: "spiffe", Host: spec.DomainID, Path: "/agent/" + spec.AgentID + "/listener/" + spec.ListenerID}
	request := &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: identityURI.String()}, SignatureAlgorithm: x509.ECDSAWithSHA256,
		URIs: []*url.URL{identityURI}, DNSNames: append([]string(nil), spec.DNSNames...),
	}
	for _, value := range spec.IPAddresses {
		request.IPAddresses = append(request.IPAddresses, net.ParseIP(value))
	}
	mutate(request)
	der, err := x509.CreateCertificateRequest(rand.Reader, request, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
	pending.Request.CSRPEM = string(csrPEM)
	normalized, err := normalizeEnrollmentSpec(spec)
	if err != nil {
		t.Fatal(err)
	}
	pending.RequestFingerprint, err = enrollmentFingerprint(normalized, pending.Request)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pendingRoot, pendingCSRName), csrPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	writePendingContractJournal(t, filepath.Join(pendingRoot, pendingJournalName), pending)
}

func writePendingContractJournal(t *testing.T, path string, pending PendingEnrollment) {
	t.Helper()
	encoded, err := json.Marshal(pending)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
}

func resolveShellContractDataRoot(t *testing.T, raw string) string {
	t.Helper()
	if info, err := os.Stat(raw); err == nil && info.IsDir() {
		return raw
	}
	if runtime.GOOS == "windows" {
		if output, err := exec.Command("cygpath", "-w", raw).Output(); err == nil {
			converted := strings.TrimSpace(string(output))
			if info, statErr := os.Stat(converted); statErr == nil && info.IsDir() {
				return converted
			}
		}
		if len(raw) >= 3 && raw[0] == '/' && raw[2] == '/' {
			converted := strings.ToUpper(raw[1:2]) + ":" + filepath.FromSlash(raw[2:])
			if info, err := os.Stat(converted); err == nil && info.IsDir() {
				return converted
			}
		}
	}
	t.Fatalf("NRE_TEST_SHELL_PKI_DATA_DIR %q is not a readable directory", raw)
	return ""
}
