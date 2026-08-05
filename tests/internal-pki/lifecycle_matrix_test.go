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
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
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

type securityStateObservation struct {
	Version     int       `json:"version"`
	Hash        string    `json:"sha256"`
	ActivatedAt time.Time `json:"activated_at"`
	Snapshot    struct {
		PKIEpoch         int64 `json:"pki_epoch"`
		SecurityRevision int64 `json:"security_revision"`
	} `json:"snapshot"`
}

type securityPointerObservation struct {
	Version     int       `json:"version"`
	File        string    `json:"file"`
	Hash        string    `json:"sha256"`
	ActivatedAt time.Time `json:"activated_at"`
}

func TestInternalPKISnapshotDowngradeRecoversHighestDurableSecurityState(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	h := newTestHarness(t, ctx)
	control := h.startControl(filepath.Join(h.tempRoot, "downgrade-control"))
	initial := h.waitForPKI(control)
	agentData := filepath.Join(h.tempRoot, "downgrade-agent")
	agentID, agentToken := h.joinAndReplayRemoteAgent(control, agentData)
	agent := h.startRemoteAgent(control, agentID, agentToken, agentData)
	h.waitForRemoteHeartbeat(control, agentID, agent)

	listenerID := h.createPKIRelayListener(control, reserveLoopbackPort(t))
	h.revokePKIIdentity(control, "listener", localAgentID, fmtInt(listenerID))
	var advanced overviewEnvelope
	if err := eventually(ctx, processTimeout, func(context.Context) (bool, error) {
		advanced = h.waitForPKI(control)
		return advanced.Overview.SecurityRevision > initial.Overview.SecurityRevision, nil
	}); err != nil {
		t.Fatalf("wait for advanced security revision: %v", err)
	}
	oldest, newest := waitForSecurityHistory(t, ctx, agentData, 2)
	if oldest.Snapshot.PKIEpoch == newest.Snapshot.PKIEpoch && oldest.Snapshot.SecurityRevision >= newest.Snapshot.SecurityRevision {
		t.Fatalf("security history did not advance: oldest=%+v newest=%+v", oldest.Snapshot, newest.Snapshot)
	}
	agent.stop()
	forceSecurityPointer(t, agentData, oldest)

	agent = h.startRemoteAgent(control, agentID, agentToken, agentData)
	h.waitForRemoteHeartbeat(control, agentID, agent)
	waitForSecurityPointer(t, ctx, agentData, newest)
	h.assertTokenControlBoundary(control, agentID, agentToken)
}

func TestInternalPKIOfflineLastKnownGoodAndReconnectSafetyPriority(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	h := newTestHarness(t, ctx)
	controlData := filepath.Join(h.tempRoot, "offline-control")
	control := h.startControl(controlData)
	h.waitForPKI(control)
	agentData := filepath.Join(h.tempRoot, "offline-agent")
	agentID, agentToken := h.joinAndReplayRemoteAgent(control, agentData)
	agent := h.startRemoteAgent(control, agentID, agentToken, agentData)
	h.waitForRemoteHeartbeat(control, agentID, agent)
	relayPort := reserveLoopbackPort(t)
	listenerID, mutation := h.createPKIRelayListenerRequest(control, agentID, "offline-lkg-listener", relayPort)
	h.waitForMutation(control, mutation, "create offline LKG listener")
	localCertificate := loadActiveAgentCertificate(t, filepath.Join(controlData, "embedded-agent-state"))
	address := fmt.Sprintf("127.0.0.1:%d", relayPort)
	if err := eventually(ctx, processTimeout, func(context.Context) (bool, error) {
		return tlsHandshake(address, &localCertificate) == nil, nil
	}); err != nil {
		t.Fatalf("wait for remote relay credential activation: %v\n%s", err, agent.failureLog())
	}

	control.process.stop()
	if err := processRunningFor(agent, time.Second); err != nil {
		t.Fatalf("remote agent stopped with its control plane offline: %v\n%s", err, agent.failureLog())
	}
	if err := tlsHandshake(address, &localCertificate); err != nil {
		t.Fatalf("offline last-known-good relay was unavailable: %v\n%s", err, agent.failureLog())
	}
	agent.stop()

	control = h.startControl(controlData)
	h.waitForPKI(control)
	h.revokePKIIdentity(control, "listener", agentID, fmtInt(listenerID))
	agent = h.startRemoteAgent(control, agentID, agentToken, agentData)
	h.waitForRemoteHeartbeat(control, agentID, agent)
	if err := eventually(ctx, 5*time.Second, func(context.Context) (bool, error) {
		return tlsHandshake(address, &localCertificate) != nil, nil
	}); err != nil {
		t.Fatalf("reconnected agent did not prioritize the revocation fence: %v\n%s", err, agent.failureLog())
	}
	h.assertTokenControlBoundary(control, agentID, agentToken)
}

func TestInternalPKIEmergencyRotateReenrollsRemoteListenerAndRestoresRelay(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Minute)
	defer cancel()
	h := newTestHarness(t, ctx)
	control := h.startControl(filepath.Join(h.tempRoot, "emergency-control"))
	before := h.waitForPKI(control)
	disabledPort := reserveLoopbackPort(t)
	disabledName := "emergency-disabled-local-listener"
	disabledListenerID, disabledMutation := h.createPKIRelayListenerRequest(
		control, localAgentID, disabledName, disabledPort,
	)
	h.waitForMutation(control, disabledMutation, "create emergency local listener before disabling it")
	h.updatePKIRelayListenerEnabled(control, localAgentID, disabledListenerID, disabledName, disabledPort, false)
	disabledAddress := fmt.Sprintf("127.0.0.1:%d", disabledPort)
	if connection, err := net.DialTimeout("tcp", disabledAddress, 500*time.Millisecond); err == nil {
		_ = connection.Close()
		t.Fatal("disabled embedded listener accepted a connection before emergency rotation")
	}
	agentData := filepath.Join(h.tempRoot, "emergency-agent")
	agentID, agentToken := h.joinAndReplayRemoteAgent(control, agentData)
	agent := h.startRemoteAgent(control, agentID, agentToken, agentData)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("emergency remote agent log:\n%s", agent.failureLog())
			for _, name := range []string{"runtime-state.json", "generation-journal.json", "desired-snapshot.json"} {
				if data, err := os.ReadFile(filepath.Join(agentData, name)); err == nil {
					t.Logf("emergency remote %s:\n%s", name, data)
				}
			}
			_ = filepath.WalkDir(filepath.Join(agentData, "pki"), func(path string, entry os.DirEntry, err error) error {
				if err != nil || entry.IsDir() || filepath.Ext(path) != ".json" {
					return nil
				}
				relative, relErr := filepath.Rel(agentData, path)
				if relErr != nil || (!strings.Contains(relative, "listener-1") && !strings.Contains(relative, "security")) {
					return nil
				}
				if data, readErr := os.ReadFile(path); readErr == nil {
					t.Logf("emergency remote %s:\n%s", relative, data)
				}
				return nil
			})
		}
	})
	h.waitForRemoteHeartbeat(control, agentID, agent)
	backendURL, backendStop := startLoopbackHTTPServer(t, "emergency-relay-restored")
	defer backendStop()
	listenerID, mutation := h.createPKIRelayListenerRequest(control, agentID, "emergency-remote-listener", reserveLoopbackPort(t))
	h.waitForMutation(control, mutation, "create emergency remote listener")
	frontendURL := fmt.Sprintf("http://127.0.0.1:%d", reserveLoopbackPort(t))
	h.createRelayedHTTPRule(control, frontendURL, backendURL, listenerID)
	h.waitForHTTPBody(control, frontendURL, "emergency-relay-restored")

	operation := h.invokeConfirmedPKIAction(control, "emergency_ca_rotate", "", "/panel-api/pki/authorities/emergency-rotate")
	operation = h.waitForPKIOperation(control, operation.ID, func(value pkiOperationObservation) bool {
		return value.Phase == "relay_enable_pending" || value.State == "failed"
	})
	if operation.State == "failed" {
		t.Fatalf("emergency rotation failed before re-enrollment: %+v\n%s", operation, control.process.failureLog())
	}
	agent.stop()
	newAgentToken := h.boundReenrollRemoteAgent(control, agentData, before.Overview.PKIDomainID, agentID)
	agent = h.startRemoteAgent(control, agentID, newAgentToken, agentData)
	h.waitForRemoteHeartbeat(control, agentID, agent)
	operation = h.waitForPKIOperation(control, operation.ID, func(value pkiOperationObservation) bool {
		return value.State == "succeeded" || value.State == "failed"
	})
	if operation.State != "succeeded" {
		t.Fatalf("emergency rotation did not converge after bound re-enrollment: %+v\n%s\n%s", operation, control.process.failureLog(), agent.failureLog())
	}
	h.assertPKIAuthorityStatuses(control, map[int64]string{2: "active"})
	h.waitForHTTPBody(control, frontendURL, "emergency-relay-restored")
	if connection, err := net.DialTimeout("tcp", disabledAddress, 500*time.Millisecond); err == nil {
		_ = connection.Close()
		t.Fatal("disabled embedded listener accepted a connection after emergency rotation")
	}
	h.assertTokenControlBoundary(control, agentID, newAgentToken)
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

func TestInternalPKIMigrationEpochFenceAndSingleActive(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	defer cancel()
	h := newTestHarness(t, ctx)

	sourceData := filepath.Join(h.tempRoot, "migration-source")
	controlOverrides := map[string]string{"NRE_ENABLE_LOCAL_AGENT": "false"}
	owner := h.startControlWithOverrides(sourceData, controlOverrides)
	sourceOverview := h.waitForPKI(owner)
	agentData := filepath.Join(h.tempRoot, "migration-agent")
	agentID, agentToken := h.joinAndReplayRemoteAgent(owner, agentData)
	agent := h.startRemoteAgent(owner, agentID, agentToken, agentData)
	h.waitForRemoteHeartbeat(owner, agentID, agent)
	agent.stop()

	follower := h.startControlWithOverrides(sourceData, controlOverrides)
	h.waitForPKIOverviewState(follower, func(value overviewEnvelope) bool {
		return value.Overview.RuntimeStatus == "degraded"
	})
	blockedMutation := h.mustJSON(http.MethodPost, follower.baseURL+"/panel-api/pki/enrollment-tokens", map[string]string{
		"scope": "new_agent",
	}, map[string]string{"X-Panel-Token": h.panelToken})
	if blockedMutation.Status != http.StatusServiceUnavailable {
		t.Fatalf("contending follower PKI mutation status = %d, want 503: %s", blockedMutation.Status, blockedMutation.Body)
	}
	h.assertTokenControlBoundary(follower, agentID, agentToken)

	owner.process.stop()
	promoted := h.waitForPKIOverviewState(follower, func(value overviewEnvelope) bool {
		return value.Overview.RuntimeStatus == "ready" && value.Overview.PKIDomainID == sourceOverview.Overview.PKIDomainID
	})
	if promoted.Overview.PKIEpoch != sourceOverview.Overview.PKIEpoch {
		t.Fatalf("single-active promotion changed PKI epoch: owner=%+v follower=%+v", sourceOverview.Overview, promoted.Overview)
	}
	promotedMutation := h.mustJSON(http.MethodPost, follower.baseURL+"/panel-api/pki/enrollment-tokens", map[string]string{
		"scope": "new_agent",
	}, map[string]string{"X-Panel-Token": h.panelToken})
	if promotedMutation.Status != http.StatusCreated {
		t.Fatalf("promoted follower PKI mutation status = %d, want 201: %s", promotedMutation.Status, promotedMutation.Body)
	}

	sourceSeen := h.agentLastSeen(follower, agentID)
	agent = h.startRemoteAgent(follower, agentID, agentToken, agentData)
	h.waitForRemoteHeartbeatAfter(follower, agentID, sourceSeen, agent)
	agent.stop()
	archive := h.exportProtectedBackup(follower)

	targetData := filepath.Join(h.tempRoot, "migration-target")
	target := h.startControlWithOverrides(targetData, controlOverrides)
	targetBefore := h.waitForPKI(target)
	if target.dataDir == follower.dataDir || target.baseURL == follower.baseURL {
		t.Fatalf("migration fixture did not change data directory and address: source=%+v target=%+v", follower, target)
	}
	if state := h.importProtectedBackup(target, archive, "e2e-backup-passphrase-strong", true); state != "succeeded" {
		t.Fatalf("forced migration import state = %q, want succeeded\n%s", state, target.process.failureLog())
	}
	// The public response does not expose cleanup_pending. The documented
	// fail-closed path therefore restarts the same isolated target before it is
	// allowed to serve migrated agents or relay traffic.
	target.process.stop()
	target = h.startControlWithOverrides(targetData, controlOverrides)
	migrated := h.waitForPKIOverviewState(target, func(value overviewEnvelope) bool {
		return value.Overview.RuntimeStatus == "ready" &&
			value.Overview.PKIDomainID == sourceOverview.Overview.PKIDomainID &&
			value.Overview.PKIEpoch > sourceOverview.Overview.PKIEpoch &&
			value.Overview.PKIEpoch > targetBefore.Overview.PKIEpoch
	})

	importedSeen := h.agentLastSeen(target, agentID)
	agent = h.startRemoteAgent(target, agentID, agentToken, agentData)
	h.waitForRemoteHeartbeatAfter(target, agentID, importedSeen, agent)
	migratedSecurity := waitForActiveSecurityEpoch(t, ctx, agentData, migrated.Overview.PKIEpoch)
	agent.stop()

	oldSourceSeen := h.agentLastSeen(follower, agentID)
	downgradeAttempt := h.startRemoteAgent(follower, agentID, agentToken, agentData)
	h.waitForAgentLastSeenAfter(follower, agentID, oldSourceSeen)
	h.waitForAgentSyncError(agentData, "heartbeat failed: 409 Conflict")
	waitForSecurityPointer(t, ctx, agentData, migratedSecurity)
	h.assertTokenControlBoundary(follower, agentID, agentToken)
	downgradeAttempt.stop()
}

func waitForActiveSecurityEpoch(t *testing.T, ctx context.Context, dataDir string, expectedEpoch int64) securityStateObservation {
	t.Helper()
	securityRoot := filepath.Join(dataDir, "pki", "security")
	var pointer securityPointerObservation
	var state securityStateObservation
	err := eventually(ctx, processTimeout, func(context.Context) (bool, error) {
		pointerData, err := os.ReadFile(filepath.Join(securityRoot, "active.json"))
		if err != nil {
			return false, err
		}
		if err := json.Unmarshal(pointerData, &pointer); err != nil {
			return false, err
		}
		stateData, err := os.ReadFile(filepath.Join(securityRoot, "snapshots", pointer.File))
		if err != nil {
			return false, err
		}
		if err := json.Unmarshal(stateData, &state); err != nil {
			return false, err
		}
		if pointer.Version != 1 || state.Version != 1 || pointer.Hash == "" || pointer.Hash != state.Hash ||
			pointer.File != securityStateFile(state) {
			return false, fmt.Errorf("active security pointer and immutable state are inconsistent")
		}
		return state.Snapshot.PKIEpoch == expectedEpoch, nil
	})
	if err != nil {
		t.Fatalf("wait for active security epoch %d: %v; pointer=%+v state=%+v", expectedEpoch, err, pointer, state.Snapshot)
	}
	return state
}

func (h *testHarness) waitForPKIOverviewState(control controlInstance, accept func(overviewEnvelope) bool) overviewEnvelope {
	h.t.Helper()
	var observed overviewEnvelope
	err := eventually(h.ctx, 2*processTimeout, func(ctx context.Context) (bool, error) {
		select {
		case processErr := <-control.process.done:
			return false, fmt.Errorf("control process exited early: %v\n%s", processErr, control.process.failureLog())
		default:
		}
		response, err := h.request(ctx, http.MethodGet, control.baseURL+"/panel-api/pki/overview", nil, map[string]string{
			"X-Panel-Token": h.panelToken,
		})
		if err != nil {
			return false, err
		}
		if response.Status != http.StatusOK {
			return false, fmt.Errorf("PKI overview status %d: %s", response.Status, response.Body)
		}
		if err := json.Unmarshal(response.Body, &observed); err != nil {
			return false, err
		}
		return observed.OK && accept(observed), nil
	})
	if err != nil {
		h.t.Fatalf("wait for PKI overview state: %v; last=%+v\n%s", err, observed.Overview, control.process.failureLog())
	}
	return observed
}

func (h *testHarness) agentLastSeen(control controlInstance, agentID string) time.Time {
	h.t.Helper()
	seen, err := h.readAgentLastSeen(h.ctx, control, agentID)
	if err != nil {
		h.t.Fatalf("read agent %s last-seen time: %v", agentID, err)
	}
	return seen
}

func (h *testHarness) readAgentLastSeen(ctx context.Context, control controlInstance, agentID string) (time.Time, error) {
	response, err := h.request(ctx, http.MethodGet, control.baseURL+"/panel-api/agents/"+url.PathEscape(agentID), nil, map[string]string{
		"X-Panel-Token": h.panelToken,
	})
	if err != nil {
		return time.Time{}, err
	}
	if response.Status != http.StatusOK {
		return time.Time{}, fmt.Errorf("agent status %d: %s", response.Status, response.Body)
	}
	var envelope struct {
		Agent struct {
			LastSeenAt string `json:"last_seen_at"`
		} `json:"agent"`
	}
	if err := json.Unmarshal(response.Body, &envelope); err != nil {
		return time.Time{}, err
	}
	if strings.TrimSpace(envelope.Agent.LastSeenAt) == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, envelope.Agent.LastSeenAt)
}

func (h *testHarness) waitForRemoteHeartbeatAfter(control controlInstance, agentID string, previous time.Time, process *childProcess) {
	h.t.Helper()
	var observed time.Time
	err := eventually(h.ctx, processTimeout, func(ctx context.Context) (bool, error) {
		select {
		case processErr := <-process.done:
			return false, fmt.Errorf("remote agent exited early: %v\n%s", processErr, process.failureLog())
		default:
		}
		seen, err := h.readAgentLastSeen(ctx, control, agentID)
		if err != nil {
			return false, err
		}
		observed = seen
		return seen.After(previous), nil
	})
	if err != nil {
		h.t.Fatalf("wait for a new remote heartbeat after %s: %v; last=%s\ncontrol:\n%s\nagent:\n%s",
			previous, err, observed, control.process.failureLog(), process.failureLog())
	}
}

func (h *testHarness) waitForAgentLastSeenAfter(control controlInstance, agentID string, previous time.Time) {
	h.t.Helper()
	var observed time.Time
	err := eventually(h.ctx, processTimeout, func(ctx context.Context) (bool, error) {
		seen, err := h.readAgentLastSeen(ctx, control, agentID)
		if err != nil {
			return false, err
		}
		observed = seen
		return seen.After(previous), nil
	})
	if err != nil {
		h.t.Fatalf("wait for agent last-seen after %s: %v; last=%s\n%s", previous, err, observed, control.process.failureLog())
	}
}

func (h *testHarness) waitForAgentSyncError(dataDir, contains string) {
	h.t.Helper()
	path := filepath.Join(dataDir, "runtime-state.json")
	var observed string
	err := eventually(h.ctx, processTimeout, func(context.Context) (bool, error) {
		encoded, err := os.ReadFile(path)
		if err != nil {
			return false, err
		}
		var state struct {
			Metadata map[string]string `json:"metadata"`
		}
		if err := json.Unmarshal(encoded, &state); err != nil {
			return false, err
		}
		observed = state.Metadata["last_sync_error"]
		return strings.Contains(observed, contains), nil
	})
	if err != nil {
		h.t.Fatalf("wait for agent sync error containing %q: %v; last=%q", contains, err, observed)
	}
}

func writeIntegrationClock(t *testing.T, path string, value time.Time) {
	t.Helper()
	encoded := []byte(value.UTC().Format(time.RFC3339Nano))
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatalf("write integration PKI clock: %v", err)
	}
}

func waitForSecurityHistory(t *testing.T, ctx context.Context, dataDir string, minimum int) (securityStateObservation, securityStateObservation) {
	t.Helper()
	var states []securityStateObservation
	err := eventually(ctx, processTimeout, func(context.Context) (bool, error) {
		entries, err := os.ReadDir(filepath.Join(dataDir, "pki", "security", "snapshots"))
		if err != nil {
			return false, err
		}
		states = states[:0]
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			encoded, err := os.ReadFile(filepath.Join(dataDir, "pki", "security", "snapshots", entry.Name()))
			if err != nil {
				return false, err
			}
			var state securityStateObservation
			if err := json.Unmarshal(encoded, &state); err != nil || state.Version != 1 || len(state.Hash) < 16 || state.ActivatedAt.IsZero() {
				return false, fmt.Errorf("invalid durable security state %q", entry.Name())
			}
			states = append(states, state)
		}
		sort.Slice(states, func(left, right int) bool {
			if states[left].Snapshot.PKIEpoch != states[right].Snapshot.PKIEpoch {
				return states[left].Snapshot.PKIEpoch < states[right].Snapshot.PKIEpoch
			}
			return states[left].Snapshot.SecurityRevision < states[right].Snapshot.SecurityRevision
		})
		return len(states) >= minimum, nil
	})
	if err != nil {
		t.Fatalf("wait for %d durable security states: %v", minimum, err)
	}
	return states[0], states[len(states)-1]
}

func securityStateFile(state securityStateObservation) string {
	return fmt.Sprintf("%d-%d-%s.json", state.Snapshot.PKIEpoch, state.Snapshot.SecurityRevision, state.Hash[:16])
}

func forceSecurityPointer(t *testing.T, dataDir string, state securityStateObservation) {
	t.Helper()
	pointer := securityPointerObservation{
		Version: 1, File: securityStateFile(state), Hash: state.Hash, ActivatedAt: state.ActivatedAt,
	}
	encoded, err := json.Marshal(pointer)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dataDir, "pki", "security", "active.json")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatalf("force downgraded security pointer: %v", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("secure downgraded security pointer: %v", err)
	}
}

func waitForSecurityPointer(t *testing.T, ctx context.Context, dataDir string, expected securityStateObservation) {
	t.Helper()
	path := filepath.Join(dataDir, "pki", "security", "active.json")
	var observed securityPointerObservation
	err := eventually(ctx, processTimeout, func(context.Context) (bool, error) {
		encoded, err := os.ReadFile(path)
		if err != nil {
			return false, err
		}
		if err := json.Unmarshal(encoded, &observed); err != nil {
			return false, err
		}
		return observed.File == securityStateFile(expected) && observed.Hash == expected.Hash, nil
	})
	if err != nil {
		t.Fatalf("wait for highest durable security pointer: %v; expected=%s/%s observed=%s/%s",
			err, securityStateFile(expected), expected.Hash, observed.File, observed.Hash)
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
		h.t.Fatalf("wait for PKI operation %s: %v; last=%+v\nidentities=%s\ncertificates=%s\n%s",
			operationID, err, observed,
			h.diagnosticGET(control, "/panel-api/pki/identities"),
			h.diagnosticGET(control, "/panel-api/pki/certificates"),
			control.process.failureLog())
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
