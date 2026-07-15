package generationsoak

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

type matrixEvidence struct {
	Path   string `json:"path"`
	Marker string `json:"marker"`
}

type matrixCase struct {
	ID       string           `json:"id"`
	Anchors  []string         `json:"anchors"`
	Evidence []matrixEvidence `json:"evidence"`
}

type goTestSuite struct {
	Name     string
	Module   string
	Packages []string
	Pattern  string
	Expected []string
}

func TestGenerationMatrixSmoke(t *testing.T) {
	root := repositoryRoot(t)
	validateMatrix(t, root, loadMatrix(t))
	runBackendSoak(t, root, 3)
}

func TestGenerationMatrix(t *testing.T) {
	root := repositoryRoot(t)
	validateMatrix(t, root, loadMatrix(t))
	t.Run("first-party mutation callers track accepted operations", TestFirstPartyMutationContractCoverage)

	suites := []goTestSuite{
		{
			Name:     "control-plane async, recovery, dependency, and real cutover",
			Module:   "panel/backend-go",
			Packages: []string{"./internal/controlplane/http", "./internal/controlplane/coordinator", "./internal/controlplane/dependency", "./internal/controlplane/localagent", "./internal/controlplane/service", "./internal/controlplane/cutover"},
			Pattern:  "Test(MutationEndpointsReturnAcceptedEnvelopeAndReplayOriginalResource|MutationReplaySurvivesCommittedResponseEnvelopeGapAndRestart|ObservabilityMetricsRequirePanelTokenAndExposeOnlyBoundedLabels|ClaimLatestSupersedesIntermediateAndSerializesAgent|FailurePersistsFullJitterAndStopsAfterFiveActualAttempts|CoordinatorRebuildsIdenticalDegradedAuditAfterRestart|PlanEvaluationUsesForwardApplyReverseDeleteAndDegradedTerminal|RevisionWorkerResumesStartedLeaseAfterRestart|RevisionAPIRemotePullClaimsOnlyCallerFrontierAndRejectsStaleReport|MasterEmbeddedCutoverAppliesHTTPRuleAndServesTraffic|MasterEmbeddedCutoverAppliesL4RuleAndForwardsTCP|MasterEmbeddedCutoverAppliesRelayListenerAndTrustChain|ManagedHTTPSMutationRoundTrip|GenerationCutoverSoak)$",
			Expected: []string{
				"TestMutationEndpointsReturnAcceptedEnvelopeAndReplayOriginalResource",
				"TestMutationReplaySurvivesCommittedResponseEnvelopeGapAndRestart",
				"TestObservabilityMetricsRequirePanelTokenAndExposeOnlyBoundedLabels",
				"TestClaimLatestSupersedesIntermediateAndSerializesAgent",
				"TestFailurePersistsFullJitterAndStopsAfterFiveActualAttempts",
				"TestCoordinatorRebuildsIdenticalDegradedAuditAfterRestart",
				"TestPlanEvaluationUsesForwardApplyReverseDeleteAndDegradedTerminal",
				"TestRevisionWorkerResumesStartedLeaseAfterRestart",
				"TestRevisionAPIRemotePullClaimsOnlyCallerFrontierAndRejectsStaleReport",
				"TestMasterEmbeddedCutoverAppliesHTTPRuleAndServesTraffic",
				"TestMasterEmbeddedCutoverAppliesL4RuleAndForwardsTCP",
				"TestMasterEmbeddedCutoverAppliesRelayListenerAndTrustChain",
				"TestManagedHTTPSMutationRoundTrip",
				"TestGenerationCutoverSoak",
			},
		},
		{
			Name:     "protocol generation publication, pinning, revoke, and oldest drain",
			Module:   "go-agent",
			Packages: []string{"./internal/modules/http", "./internal/modules/l4", "./internal/modules/relay", "./internal/modules/wireguard"},
			Pattern:  "Test(HTTPGenerationCandidatePublishesNewSessionsWithoutInterruptingOldRequest|HTTPGenerationViewReadinessFailurePreservesPublishedRuntime|HTTPGenerationDeleteRevokesOnlyTargetRequest|L4GenerationTCPPublishPinsExistingConnection|L4GenerationUDPTuplePinsAndReselectsAfterIdle|L4RuleEntityChangesRevokeOnlyDeleteAndDisable|L4GenerationDrainRevokesTargetAndForcesOldestGeneration|RelayGenerationCandidateKeepsSameBindingAndTLSInvisibleUntilPublish|RelayQUICGenerationCandidateKeepsAssociationAndTLSInvisibleUntilPublish|RelayNoopGenerationsDoNotDuplicateRuntimeDrainOwnership|WireGuardGenerationStableBindPublicationAndAssociationPinning|WireGuardGenerationDeleteAndDisableRevokeOnlyTargetProfile|WireGuardGenerationThirdGenerationForcesOldestAndReleasesRuntime)$",
			Expected: []string{
				"TestHTTPGenerationCandidatePublishesNewSessionsWithoutInterruptingOldRequest",
				"TestHTTPGenerationViewReadinessFailurePreservesPublishedRuntime",
				"TestHTTPGenerationDeleteRevokesOnlyTargetRequest",
				"TestL4GenerationTCPPublishPinsExistingConnection",
				"TestL4GenerationUDPTuplePinsAndReselectsAfterIdle",
				"TestL4RuleEntityChangesRevokeOnlyDeleteAndDisable",
				"TestL4GenerationDrainRevokesTargetAndForcesOldestGeneration",
				"TestRelayGenerationCandidateKeepsSameBindingAndTLSInvisibleUntilPublish",
				"TestRelayQUICGenerationCandidateKeepsAssociationAndTLSInvisibleUntilPublish",
				"TestRelayNoopGenerationsDoNotDuplicateRuntimeDrainOwnership",
				"TestWireGuardGenerationStableBindPublicationAndAssociationPinning",
				"TestWireGuardGenerationDeleteAndDisableRevokeOnlyTargetProfile",
				"TestWireGuardGenerationThirdGenerationForcesOldestAndReleasesRuntime",
			},
		},
		{
			Name:     "cross-platform app hot upgrade success and failure authority",
			Module:   "go-agent",
			Packages: []string{"./internal/app"},
			Pattern:  "Test(HotRestartReplacementRunsSupervisorActivationDrainAndAuthority|HotRestartReplacementAbortsAndRetainsParentOnFailure|HotRestartDrainWaitsForSameGenerationParentSessions)$",
			Expected: []string{
				"TestHotRestartReplacementRunsSupervisorActivationDrainAndAuthority",
				"TestHotRestartReplacementAbortsAndRetainsParentOnFailure",
				"TestHotRestartDrainWaitsForSameGenerationParentSessions",
			},
		},
	}
	for _, suite := range suites {
		suite := suite
		t.Run(suite.Name, func(t *testing.T) { runGoTestSuite(t, root, suite) })
	}
	runLinuxProcessMatrix(t, root)
	runLinuxPacketMatrix(t, root)
}

func TestGenerationSoak100(t *testing.T) {
	runBackendSoak(t, repositoryRoot(t), 100)
}

func TestFirstPartyMutationContractCoverage(t *testing.T) {
	root := repositoryRoot(t)
	checks := map[string][]string{
		"panel/backend-go/internal/controlplane/http/operation_api_test.go": {
			"want 202", "status_url", "Idempotency-Key",
		},
		"panel/frontend/src/api/runtime.js": {
			"preserveMutationEnvelope", "recordAcceptedOperation",
		},
		"panel/frontend/src/stores/operations.js": {
			"trackMutationResult", "status_url", "refreshOperation",
		},
	}
	for path, markers := range checks {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(data)
		for _, marker := range markers {
			if !strings.Contains(text, marker) {
				t.Errorf("%s missing async-operation marker %q", path, marker)
			}
		}
	}
}

func loadMatrix(t *testing.T) []matrixCase {
	t.Helper()
	data, err := os.ReadFile("matrix.json")
	if err != nil {
		t.Fatalf("read matrix.json: %v", err)
	}
	var cases []matrixCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatalf("decode matrix.json: %v", err)
	}
	return cases
}

func validateMatrix(t *testing.T, root string, cases []matrixCase) {
	t.Helper()
	want := make(map[string]bool, 15)
	for index := 1; index <= 15; index++ {
		want[fmt.Sprintf("AC%d", index)] = true
	}
	seen := make(map[string]bool, len(cases))
	for _, item := range cases {
		if !want[item.ID] {
			t.Errorf("unexpected acceptance id %q", item.ID)
		}
		if seen[item.ID] {
			t.Errorf("duplicate acceptance id %q", item.ID)
		}
		seen[item.ID] = true
		if len(item.Anchors) == 0 || len(item.Evidence) == 0 {
			t.Errorf("%s must have anchors and evidence", item.ID)
		}
		for _, evidence := range item.Evidence {
			data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(evidence.Path)))
			if err != nil {
				t.Errorf("%s evidence %s is unreadable: %v", item.ID, evidence.Path, err)
				continue
			}
			if !strings.Contains(string(data), evidence.Marker) {
				t.Errorf("%s evidence %s missing marker %q", item.ID, evidence.Path, evidence.Marker)
			}
		}
	}
	var missing []string
	for id := range want {
		if !seen[id] {
			missing = append(missing, id)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("acceptance matrix missing %v", missing)
	}
}

func runGoTestSuite(t *testing.T, root string, suite goTestSuite) {
	t.Helper()
	args := []string{"test"}
	args = append(args, suite.Packages...)
	args = append(args, "-run", suite.Pattern, "-count=1", "-v")
	output := runCommand(t, filepath.Join(root, filepath.FromSlash(suite.Module)), nil, "go", args...)
	requireExpectedTests(t, output, suite.Expected)
}

func runBackendSoak(t *testing.T, root string, iterations int) {
	t.Helper()
	output := runCommand(t, filepath.Join(root, "panel", "backend-go"), []string{fmt.Sprintf("NRE_GENERATION_SOAK_ITERATIONS=%d", iterations)}, "go", "test", "./internal/controlplane/cutover", "-run", "^TestGenerationCutoverSoak$", "-count=1", "-v")
	requireExpectedTests(t, output, []string{"TestGenerationCutoverSoak"})
}

func runLinuxProcessMatrix(t *testing.T, root string) {
	t.Helper()
	pattern := "Test(SupervisorReadinessActivationAndAuthorityOrdering|PostReadinessFailuresAbortChildAndRecoverParentAuthority)$"
	expected := []string{"TestSupervisorReadinessActivationAndAuthorityOrdering", "TestPostReadinessFailuresAbortChildAndRecoverParentAuthority"}
	if runtime.GOOS == "linux" {
		output := runCommand(t, filepath.Join(root, "go-agent"), nil, "go", "test", "./internal/hotrestart", "-run", pattern, "-count=1", "-v")
		requireExpectedTests(t, output, expected)
		return
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatalf("Linux process hot-restart matrix requires docker on %s: %v", runtime.GOOS, err)
	}
	output := runCommand(t, root, nil, "docker", "run", "--rm", "-v", root+":/workspace", "-w", "/workspace/go-agent", "golang:1.26", "go", "test", "./internal/hotrestart", "-run", pattern, "-count=1", "-v")
	requireExpectedTests(t, output, expected)
}

func runLinuxPacketMatrix(t *testing.T, root string) {
	t.Helper()
	pattern := "TestHotRestartPacket(ProtocolMatrix|FailureAndRepeatMatrix|RepeatedUpgradeCleanup|FailedChildCleansProcessGroup)$"
	expected := []string{"TestHotRestartPacketProtocolMatrix", "TestHotRestartPacketFailureAndRepeatMatrix", "TestHotRestartPacketRepeatedUpgradeCleanup", "TestHotRestartPacketFailedChildCleansProcessGroup"}
	if runtime.GOOS == "linux" {
		output := runCommand(t, filepath.Join(root, "go-agent"), nil, "go", "test", "./internal/app", "-run", pattern, "-count=1", "-v")
		requireExpectedTests(t, output, expected)
		return
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatalf("Linux packet matrix requires docker on %s: %v", runtime.GOOS, err)
	}
	output := runCommand(t, root, nil, "docker", "run", "--rm", "-v", root+":/workspace", "-w", "/workspace/go-agent", "golang:1.26", "go", "test", "./internal/app", "-run", pattern, "-count=1", "-v")
	requireExpectedTests(t, output, expected)
}

func requireExpectedTests(t *testing.T, output string, expected []string) {
	t.Helper()
	if strings.Contains(output, "[no tests to run]") {
		t.Fatal("nested go test reported no tests to run")
	}
	for _, testName := range expected {
		if !strings.Contains(output, "=== RUN   "+testName) {
			t.Errorf("nested test %s did not run", testName)
		}
		if strings.Contains(output, "--- SKIP: "+testName) {
			t.Errorf("nested test %s was skipped", testName)
		}
		if !strings.Contains(output, "--- PASS: "+testName) {
			t.Errorf("nested test %s did not pass", testName)
		}
	}
}

func runCommand(t *testing.T, dir string, extraEnv []string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), extraEnv...)
	configureProcessTree(cmd)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s %s in %s: %v", name, strings.Join(args, " "), dir, err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var err error
	select {
	case err = <-done:
	case <-time.After(10 * time.Minute):
		killProcessTree(cmd)
		err = <-done
		t.Fatalf("%s %s timed out after 10m in %s\n%s", name, strings.Join(args, " "), dir, output.String())
	}
	if err != nil {
		t.Fatalf("%s %s failed in %s: %v\n%s", name, strings.Join(args, " "), dir, err, output.String())
	}
	t.Logf("%s %s\n%s", name, strings.Join(args, " "), output.String())
	return output.String()
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
