//go:build integration

package internalpki

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

const (
	integrationClockFileEnv             = "NRE_INTEGRATION_PKI_CLOCK_FILE"
	integrationAuthorityHeartbeatEnv    = "NRE_INTEGRATION_PKI_AUTHORITY_HEARTBEAT_INTERVAL"
	integrationPersistenceCrashPointEnv = "NRE_INTEGRATION_PKI_PERSISTENCE_CRASH_POINT"
	integrationRestoreCrashPointEnv     = "NRE_INTEGRATION_PKI_RESTORE_CRASH_POINT"
)

type renewalFileState struct {
	CredentialFingerprint string    `json:"credential_fingerprint_sha256"`
	DueAt                 time.Time `json:"due_at"`
}

type activeCredentialObservation struct {
	Generation      string
	ManifestHash    string
	CertificateID   string
	CertificateHash string
	PrivateKeyHash  string
	NotBefore       time.Time
	NotAfter        time.Time
}

type pkiOperationObservation struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	State     string `json:"state"`
	Phase     string `json:"phase"`
	LastError string `json:"last_error"`
}

type pkiIdentityObservation struct {
	ID                   string  `json:"id"`
	Kind                 string  `json:"kind"`
	AgentID              string  `json:"agent_id"`
	State                string  `json:"state"`
	CurrentCertificateID *string `json:"current_certificate_id"`
}

type pkiAuthorityObservation struct {
	Generation int64  `json:"generation"`
	Status     string `json:"status"`
}

func TestInternalPKIThirdLifetimeRenewalAndActivePointerCrash(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	h := newTestHarness(t, ctx)
	clockPath := filepath.Join(h.tempRoot, "pki-clock.txt")
	initialNow := time.Now().UTC().Truncate(time.Second)
	writeIntegrationClock(t, clockPath, initialNow)

	control := h.startControlWithOverrides(filepath.Join(h.tempRoot, "clock-control"), map[string]string{
		integrationClockFileEnv: clockPath,
	})
	h.waitForPKI(control)
	agentData := filepath.Join(h.tempRoot, "clock-agent")
	agentID, agentToken := h.joinAndReplayRemoteAgent(control, agentData)
	agent := h.startRemoteAgentWithOverrides(control, agentID, agentToken, agentData, map[string]string{
		integrationClockFileEnv: clockPath,
	})
	h.waitForRemoteHeartbeat(control, agentID, agent)

	first := waitForCompleteActiveCredential(t, ctx, agentData, nil)
	firstRenewal := waitForRenewalState(t, ctx, agentData, "")
	lifetime := first.NotAfter.Sub(first.NotBefore)
	if lifetime <= 0 || firstRenewal.DueAt.Before(first.NotBefore.Add(lifetime/2)) || !firstRenewal.DueAt.Before(first.NotAfter) {
		t.Fatalf("renewal due %s is outside the credential lifetime %s..%s", firstRenewal.DueAt, first.NotBefore, first.NotAfter)
	}

	writeIntegrationClock(t, clockPath, firstRenewal.DueAt.Add(time.Second))
	second := waitForCompleteActiveCredential(t, ctx, agentData, &first)
	if second.CertificateID == first.CertificateID || second.PrivateKeyHash == first.PrivateKeyHash {
		t.Fatalf("third-lifetime renewal reused credential material: first=%+v second=%+v", first, second)
	}
	secondRenewal := waitForRenewalState(t, ctx, agentData, firstRenewal.CredentialFingerprint)
	if !secondRenewal.DueAt.After(firstRenewal.DueAt) {
		t.Fatalf("renewed credential due_at did not advance: first=%s second=%s", firstRenewal.DueAt, secondRenewal.DueAt)
	}

	agent.stop()
	crashing := h.startRemoteAgentWithOverrides(control, agentID, agentToken, agentData, map[string]string{
		integrationClockFileEnv:             clockPath,
		integrationPersistenceCrashPointEnv: "credential.after_pointer_publish",
	})
	writeIntegrationClock(t, clockPath, secondRenewal.DueAt.Add(time.Second))
	waitForProcessExitCode(t, crashing, 87, processTimeout)
	third := waitForCompleteActiveCredential(t, ctx, agentData, &second)

	restarted := h.startRemoteAgentWithOverrides(control, agentID, agentToken, agentData, map[string]string{
		integrationClockFileEnv: clockPath,
	})
	waitForRenewalState(t, ctx, agentData, secondRenewal.CredentialFingerprint)
	stable := waitForCompleteActiveCredential(t, ctx, agentData, nil)
	if stable.Generation != third.Generation || stable.ManifestHash != third.ManifestHash || stable.CertificateID != third.CertificateID {
		t.Fatalf("restart did not retain the complete pointer-published generation: crash=%+v restart=%+v", third, stable)
	}
	if err := processRunningFor(restarted, time.Second); err != nil {
		t.Fatalf("agent did not recover after pointer-publish crash: %v\n%s", err, restarted.failureLog())
	}
}

func TestInternalPKIForceRotateAndBlockedCAOverlap(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	h := newTestHarness(t, ctx)
	clockPath := filepath.Join(h.tempRoot, "rotation-clock.txt")
	initialNow := time.Now().UTC().Truncate(time.Second)
	writeIntegrationClock(t, clockPath, initialNow)

	control := h.startControlWithOverrides(filepath.Join(h.tempRoot, "rotation-control"), map[string]string{
		integrationClockFileEnv:          clockPath,
		integrationAuthorityHeartbeatEnv: "2h",
	})
	h.waitForPKI(control)
	agentData := filepath.Join(h.tempRoot, "rotation-agent")
	agentID, agentToken := h.joinAndReplayRemoteAgent(control, agentData)
	agent := h.startRemoteAgentWithOverrides(control, agentID, agentToken, agentData, map[string]string{
		integrationClockFileEnv: clockPath,
	})
	h.waitForRemoteHeartbeat(control, agentID, agent)
	if err := processRunningFor(agent, 2*time.Second); err != nil {
		t.Fatalf("agent exited before establishing its task session: %v\n%s", err, agent.failureLog())
	}

	identity := h.activeAgentPKIIdentity(control, agentID)
	beforeCertificate := *identity.CurrentCertificateID
	force := h.invokeConfirmedPKIAction(control, "force_rotate", identity.ID,
		"/panel-api/pki/identities/"+url.PathEscape(identity.ID)+"/force-rotate")
	force = h.waitForPKIOperation(control, force.ID, func(value pkiOperationObservation) bool {
		return value.State == "succeeded" || value.State == "failed"
	})
	if force.State != "succeeded" {
		t.Fatalf("forced endpoint rotation = %+v\n%s", force, control.process.failureLog())
	}
	rotated := h.waitForAgentCertificateChange(control, agentID, beforeCertificate)
	if rotated.CurrentCertificateID == nil || *rotated.CurrentCertificateID == beforeCertificate {
		t.Fatalf("forced endpoint rotation retained certificate %q", beforeCertificate)
	}

	agent.stop()
	blockedStart := time.Now().UTC()
	writeIntegrationClock(t, clockPath, blockedStart)
	rotation := h.invokeConfirmedPKIAction(control, "ca_rotate", "", "/panel-api/pki/authorities/rotate")
	rotation = h.waitForPKIOperation(control, rotation.ID, func(value pkiOperationObservation) bool {
		return value.Phase == "distribute_trust" && value.State == "running"
	})
	h.assertPKIAuthorityStatuses(control, map[int64]string{1: "active", 2: "prepared"})

	writeIntegrationClock(t, clockPath, blockedStart.Add(time.Hour+time.Second))
	blocked := h.waitForPKIOperation(control, rotation.ID, func(value pkiOperationObservation) bool {
		return value.Phase == "distribute_trust" && value.State == "blocked"
	})
	if blocked.LastError == "" {
		t.Fatalf("blocked CA rotation has no diagnostic: %+v", blocked)
	}

	agent = h.startRemoteAgentWithOverrides(control, agentID, agentToken, agentData, map[string]string{
		integrationClockFileEnv: clockPath,
	})
	h.waitForRemoteHeartbeat(control, agentID, agent)
	overlap := h.waitForPKIOperation(control, rotation.ID, func(value pkiOperationObservation) bool {
		return value.Phase == "overlap" && value.State == "running"
	})
	if overlap.LastError != "" {
		t.Fatalf("resumed CA rotation retained blocker: %+v", overlap)
	}
	h.assertPKIAuthorityStatuses(control, map[int64]string{1: "retiring", 2: "active"})
}

func TestInternalPKIRestoreCrashSelectsOnlyCompleteGeneration(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	h := newTestHarness(t, ctx)

	source := h.startControl(filepath.Join(h.tempRoot, "restore-source"))
	sourceOverview := h.waitForPKI(source)
	h.createPKIRelayListener(source, reserveLoopbackPort(t))
	archive := h.exportProtectedBackup(source)
	source.process.stop()

	for _, test := range []struct {
		point          string
		exitCode       int
		wantSourceData bool
	}{
		{point: "after_swap", exitCode: 86, wantSourceData: false},
		{point: "after_commit", exitCode: 86, wantSourceData: true},
	} {
		t.Run(test.point, func(t *testing.T) {
			dataDir := filepath.Join(h.tempRoot, "restore-"+test.point)
			target := h.startControlWithOverrides(dataDir, map[string]string{
				integrationRestoreCrashPointEnv: test.point,
			})
			targetBefore := h.waitForPKI(target)
			h.createPKIRelayListener(target, reserveLoopbackPort(t))
			targetBefore = h.waitForPKI(target)
			if targetBefore.Overview.PKIDomainID == sourceOverview.Overview.PKIDomainID {
				t.Fatal("independent restore target unexpectedly reused the source PKI domain")
			}
			h.importProtectedBackupExpectCrash(target, archive, true)
			waitForProcessExitCode(t, target.process, test.exitCode, processTimeout)

			restarted := h.startControl(dataDir)
			after := h.waitForPKI(restarted)
			if test.wantSourceData {
				if after.Overview.PKIDomainID != sourceOverview.Overview.PKIDomainID || after.Overview.PKIEpoch <= targetBefore.Overview.PKIEpoch {
					t.Fatalf("committed restore did not select the complete imported generation: source=%+v target=%+v after=%+v", sourceOverview.Overview, targetBefore.Overview, after.Overview)
				}
			} else if after.Overview.PKIDomainID != targetBefore.Overview.PKIDomainID || after.Overview.PKIEpoch != targetBefore.Overview.PKIEpoch {
				t.Fatalf("uncommitted restore did not roll back to the complete old generation: before=%+v after=%+v", targetBefore.Overview, after.Overview)
			}
			restarted.process.stop()
		})
	}
}

func writeIntegrationClock(t *testing.T, path string, value time.Time) {
	t.Helper()
	encoded := []byte(value.UTC().Format(time.RFC3339Nano))
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatalf("write integration PKI clock: %v", err)
	}
}

func (h *testHarness) invokeConfirmedPKIAction(control controlInstance, action, targetID, actionPath string) pkiOperationObservation {
	h.t.Helper()
	confirmation := h.mustJSON(http.MethodPost, control.baseURL+"/panel-api/pki/confirmations", map[string]string{
		"action": action, "target_id": targetID,
	}, map[string]string{"X-Panel-Token": h.panelToken})
	if confirmation.Status != http.StatusCreated {
		h.t.Fatalf("issue %s confirmation status = %d: %s", action, confirmation.Status, confirmation.Body)
	}
	var confirmed struct {
		Confirmation struct {
			Nonce string `json:"nonce"`
		} `json:"confirmation"`
	}
	if err := json.Unmarshal(confirmation.Body, &confirmed); err != nil || confirmed.Confirmation.Nonce == "" {
		h.t.Fatalf("decode %s confirmation: error=%v body=%s", action, err, confirmation.Body)
	}
	response := h.mustJSON(http.MethodPost, control.baseURL+actionPath, map[string]string{
		"reason": "integration lifecycle matrix", "confirmation_nonce": confirmed.Confirmation.Nonce,
	}, map[string]string{"X-Panel-Token": h.panelToken})
	if response.Status != http.StatusAccepted {
		h.t.Fatalf("start %s status = %d: %s\n%s", action, response.Status, response.Body, control.process.failureLog())
	}
	var envelope struct {
		Operation pkiOperationObservation `json:"operation"`
	}
	if err := json.Unmarshal(response.Body, &envelope); err != nil || envelope.Operation.ID == "" {
		h.t.Fatalf("decode %s operation: error=%v body=%s", action, err, response.Body)
	}
	return envelope.Operation
}

func (h *testHarness) waitForPKIOperation(control controlInstance, operationID string, ready func(pkiOperationObservation) bool) pkiOperationObservation {
	h.t.Helper()
	var observed pkiOperationObservation
	err := eventually(h.ctx, processTimeout, func(ctx context.Context) (bool, error) {
		response, err := h.request(ctx, http.MethodGet, control.baseURL+"/panel-api/pki/operations/"+url.PathEscape(operationID), nil, map[string]string{
			"X-Panel-Token": h.panelToken,
		})
		if err != nil {
			return false, err
		}
		if response.Status != http.StatusOK {
			return false, fmt.Errorf("PKI operation status %d: %s", response.Status, response.Body)
		}
		var envelope struct {
			Operation pkiOperationObservation `json:"operation"`
		}
		if err := json.Unmarshal(response.Body, &envelope); err != nil {
			return false, err
		}
		observed = envelope.Operation
		return ready(observed), nil
	})
	if err != nil {
		h.t.Fatalf("wait for PKI operation %s: %v; last=%+v\n%s", operationID, err, observed, control.process.failureLog())
	}
	return observed
}

func (h *testHarness) activeAgentPKIIdentity(control controlInstance, agentID string) pkiIdentityObservation {
	h.t.Helper()
	response := h.mustJSON(http.MethodGet, control.baseURL+"/panel-api/pki/identities", nil, map[string]string{
		"X-Panel-Token": h.panelToken,
	})
	if response.Status != http.StatusOK {
		h.t.Fatalf("list PKI identities status = %d: %s", response.Status, response.Body)
	}
	var envelope struct {
		Identities []pkiIdentityObservation `json:"identities"`
	}
	if err := json.Unmarshal(response.Body, &envelope); err != nil {
		h.t.Fatalf("decode PKI identities: %v", err)
	}
	for _, identity := range envelope.Identities {
		if identity.Kind == "agent" && identity.AgentID == agentID && identity.State == "active" && identity.CurrentCertificateID != nil {
			return identity
		}
	}
	h.t.Fatalf("active PKI agent identity %q not found: %s", agentID, response.Body)
	return pkiIdentityObservation{}
}

func (h *testHarness) waitForAgentCertificateChange(control controlInstance, agentID, previous string) pkiIdentityObservation {
	h.t.Helper()
	var observed pkiIdentityObservation
	err := eventually(h.ctx, processTimeout, func(context.Context) (bool, error) {
		observed = h.activeAgentPKIIdentity(control, agentID)
		return observed.CurrentCertificateID != nil && *observed.CurrentCertificateID != previous, nil
	})
	if err != nil {
		h.t.Fatalf("wait for agent %s certificate rotation from %s: %v", agentID, previous, err)
	}
	return observed
}

func (h *testHarness) assertPKIAuthorityStatuses(control controlInstance, expected map[int64]string) {
	h.t.Helper()
	response := h.mustJSON(http.MethodGet, control.baseURL+"/panel-api/pki/authorities", nil, map[string]string{
		"X-Panel-Token": h.panelToken,
	})
	if response.Status != http.StatusOK {
		h.t.Fatalf("list PKI authorities status = %d: %s", response.Status, response.Body)
	}
	var envelope struct {
		Authorities []pkiAuthorityObservation `json:"authorities"`
	}
	if err := json.Unmarshal(response.Body, &envelope); err != nil {
		h.t.Fatalf("decode PKI authorities: %v", err)
	}
	actual := make(map[int64]string, len(envelope.Authorities))
	for _, authority := range envelope.Authorities {
		actual[authority.Generation] = authority.Status
	}
	for generation, status := range expected {
		if actual[generation] != status {
			h.t.Fatalf("PKI authority generation %d status = %q, want %q; all=%s", generation, actual[generation], status, response.Body)
		}
	}
}

func waitForRenewalState(t *testing.T, ctx context.Context, dataDir, previousFingerprint string) renewalFileState {
	t.Helper()
	path := filepath.Join(dataDir, "pki", "identities", "agent", "renewal.json")
	var observed renewalFileState
	err := eventually(ctx, processTimeout, func(context.Context) (bool, error) {
		encoded, err := os.ReadFile(path)
		if err != nil {
			return false, err
		}
		var candidate renewalFileState
		if err := json.Unmarshal(encoded, &candidate); err != nil {
			return false, err
		}
		if candidate.CredentialFingerprint == "" || candidate.DueAt.IsZero() || candidate.CredentialFingerprint == previousFingerprint {
			return false, nil
		}
		observed = candidate
		return true, nil
	})
	if err != nil {
		t.Fatalf("wait for durable PKI renewal state: %v", err)
	}
	return observed
}

func waitForCompleteActiveCredential(t *testing.T, ctx context.Context, dataDir string, previous *activeCredentialObservation) activeCredentialObservation {
	t.Helper()
	var observed activeCredentialObservation
	err := eventually(ctx, processTimeout, func(context.Context) (bool, error) {
		candidate, err := readCompleteActiveCredential(dataDir)
		if err != nil {
			return false, err
		}
		if previous != nil && candidate.Generation == previous.Generation {
			return false, nil
		}
		observed = candidate
		return true, nil
	})
	if err != nil {
		t.Fatalf("wait for complete active credential: %v", err)
	}
	return observed
}

func readCompleteActiveCredential(dataDir string) (activeCredentialObservation, error) {
	identityRoot := filepath.Join(dataDir, "pki", "identities", "agent")
	pointerBytes, err := os.ReadFile(filepath.Join(identityRoot, "active.json"))
	if err != nil {
		return activeCredentialObservation{}, err
	}
	var pointer struct {
		Generation   string `json:"generation"`
		ManifestHash string `json:"manifest_sha256"`
	}
	if err := json.Unmarshal(pointerBytes, &pointer); err != nil || pointer.Generation == "" || filepath.Base(pointer.Generation) != pointer.Generation {
		return activeCredentialObservation{}, fmt.Errorf("invalid active pointer: generation=%q error=%v", pointer.Generation, err)
	}
	generationRoot := filepath.Join(identityRoot, "generations", pointer.Generation)
	manifestBytes, err := os.ReadFile(filepath.Join(generationRoot, "manifest.json"))
	if err != nil {
		return activeCredentialObservation{}, err
	}
	manifestDigest := sha256.Sum256(manifestBytes)
	manifestHash := hex.EncodeToString(manifestDigest[:])
	if manifestHash != pointer.ManifestHash {
		return activeCredentialObservation{}, fmt.Errorf("active manifest hash mismatch: got %s want %s", manifestHash, pointer.ManifestHash)
	}
	var manifest struct {
		Generation string `json:"generation"`
		Credential struct {
			CertificateID string    `json:"certificate_id"`
			NotBefore     time.Time `json:"not_before"`
			NotAfter      time.Time `json:"not_after"`
		} `json:"credential"`
	}
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil || manifest.Generation != pointer.Generation || manifest.Credential.CertificateID == "" {
		return activeCredentialObservation{}, fmt.Errorf("invalid active manifest: generation=%q certificate=%q error=%v", manifest.Generation, manifest.Credential.CertificateID, err)
	}
	certificatePEM, err := os.ReadFile(filepath.Join(generationRoot, "certificate.pem"))
	if err != nil {
		return activeCredentialObservation{}, err
	}
	privateKeyPEM, err := os.ReadFile(filepath.Join(generationRoot, "private-key.pem"))
	if err != nil {
		return activeCredentialObservation{}, err
	}
	pair, err := tls.X509KeyPair(certificatePEM, privateKeyPEM)
	if err != nil {
		return activeCredentialObservation{}, fmt.Errorf("active key pair is incomplete: %w", err)
	}
	if len(pair.Certificate) == 0 {
		return activeCredentialObservation{}, errors.New("active key pair is incomplete: certificate chain is empty")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return activeCredentialObservation{}, err
	}
	certificateDigest := sha256.Sum256(certificatePEM)
	privateKeyDigest := sha256.Sum256(privateKeyPEM)
	return activeCredentialObservation{
		Generation: pointer.Generation, ManifestHash: manifestHash, CertificateID: manifest.Credential.CertificateID,
		CertificateHash: hex.EncodeToString(certificateDigest[:]), PrivateKeyHash: hex.EncodeToString(privateKeyDigest[:]),
		NotBefore: leaf.NotBefore.UTC(), NotAfter: leaf.NotAfter.UTC(),
	}, nil
}

func waitForProcessExitCode(t *testing.T, process *childProcess, want int, timeout time.Duration) {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-process.done:
		process.once.Do(func() {})
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != want {
			t.Fatalf("process exit = %v, want code %d\n%s", err, want, process.failureLog())
		}
	case <-timer.C:
		t.Fatalf("process did not exit with code %d\n%s", want, process.failureLog())
	}
}

func processRunningFor(process *childProcess, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case err := <-process.done:
		process.once.Do(func() {})
		return err
	case <-timer.C:
		return nil
	}
}

func (h *testHarness) importProtectedBackupExpectCrash(control controlInstance, archive []byte, force bool) {
	h.t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("archive", "internal-pki.backup")
	if err != nil {
		h.t.Fatalf("create crash restore part: %v", err)
	}
	if _, err := part.Write(archive); err != nil {
		h.t.Fatalf("write crash restore archive: %v", err)
	}
	for name, value := range map[string]string{
		"passphrase": protectedBackupPassphrase, "force": fmt.Sprintf("%t", force), "reason": "crash-boundary restore",
	} {
		if err := writer.WriteField(name, value); err != nil {
			h.t.Fatalf("write crash restore field: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		h.t.Fatalf("close crash restore form: %v", err)
	}
	response, requestErr := h.request(h.ctx, http.MethodPost, control.baseURL+"/panel-api/pki/backups/import", body.Bytes(), map[string]string{
		"X-Panel-Token": h.panelToken, "Content-Type": writer.FormDataContentType(),
	})
	if requestErr == nil {
		h.t.Fatalf("restore crash hook returned HTTP %d instead of terminating the process: %s", response.Status, response.Body)
	}
}
