package generationsoak

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
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
}

func TestGenerationMatrixSmoke(t *testing.T) {
	root := repositoryRoot(t)
	validateMatrix(t, root, loadMatrix(t))
	runBackendSoak(t, root, 3)
}

func TestGenerationMatrix(t *testing.T) {
	root := repositoryRoot(t)
	validateMatrix(t, root, loadMatrix(t))

	suites := []goTestSuite{
		{
			Name:     "control-plane async, recovery, dependency, and real cutover",
			Module:   "panel/backend-go",
			Packages: []string{"./internal/controlplane/http", "./internal/controlplane/coordinator", "./internal/controlplane/dependency", "./internal/controlplane/localagent", "./internal/controlplane/service", "./internal/controlplane/cutover"},
			Pattern:  "Test(MutationEndpointsReturnAcceptedEnvelopeAndReplayOriginalResource|MutationReplaySurvivesCommittedResponseEnvelopeGapAndRestart|ClaimLatestSupersedesIntermediateAndSerializesAgent|FailurePersistsFullJitterAndStopsAfterFiveActualAttempts|CoordinatorRebuildsIdenticalDegradedAuditAfterRestart|PlanEvaluationUsesForwardApplyReverseDeleteAndDegradedTerminal|RevisionWorkerResumesStartedLeaseAfterRestart|RevisionAPIRemotePullClaimsOnlyCallerFrontierAndRejectsStaleReport|MasterEmbeddedCutover|GenerationCutoverSoak)$",
		},
		{
			Name:     "protocol generation publication, pinning, revoke, and oldest drain",
			Module:   "go-agent",
			Packages: []string{"./internal/modules/http", "./internal/modules/l4", "./internal/modules/relay", "./internal/modules/wireguard"},
			Pattern:  "Test(HTTPGenerationCandidatePublishesNewSessionsWithoutInterruptingOldRequest|HTTPGenerationViewReadinessFailurePreservesPublishedRuntime|HTTPGenerationDeleteRevokesOnlyTargetRequest|L4GenerationTCPPublishPinsExistingConnection|L4GenerationUDPTuplePinsAndReselectsAfterIdle|L4RuleEntityChangesRevokeOnlyDeleteAndDisable|L4GenerationDrainRevokesTargetAndForcesOldestGeneration|RelayGenerationCandidateKeepsSameBindingAndTLSInvisibleUntilPublish|RelayQUICGenerationCandidateKeepsAssociationAndTLSInvisibleUntilPublish|RelayNoopGenerationsDoNotDuplicateRuntimeDrainOwnership|WireGuardGenerationStableBindPublicationAndAssociationPinning|WireGuardGenerationDeleteAndDisableRevokeOnlyTargetProfile|WireGuardGenerationThirdGenerationForcesOldestAndReleasesRuntime)$",
		},
		{
			Name:     "process hot upgrade success and failure authority",
			Module:   "go-agent",
			Packages: []string{"./internal/hotrestart", "./internal/app"},
			Pattern:  "Test(SupervisorReadinessActivationAndAuthorityOrdering|PostReadinessFailuresAbortChildAndRecoverParentAuthority|HotRestartReplacementRunsSupervisorActivationDrainAndAuthority|HotRestartReplacementAbortsAndRetainsParentOnFailure|HotRestartDrainWaitsForSameGenerationParentSessions)$",
		},
	}
	for _, suite := range suites {
		suite := suite
		t.Run(suite.Name, func(t *testing.T) { runGoTestSuite(t, root, suite) })
	}
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
	args = append(args, "-run", suite.Pattern, "-count=1")
	runCommand(t, filepath.Join(root, filepath.FromSlash(suite.Module)), nil, "go", args...)
}

func runBackendSoak(t *testing.T, root string, iterations int) {
	t.Helper()
	runCommand(t, filepath.Join(root, "panel", "backend-go"), []string{fmt.Sprintf("NRE_GENERATION_SOAK_ITERATIONS=%d", iterations)}, "go", "test", "./internal/controlplane/cutover", "-run", "^TestGenerationCutoverSoak$", "-count=1", "-v")
}

func runLinuxPacketMatrix(t *testing.T, root string) {
	t.Helper()
	pattern := "TestHotRestartPacket(ProtocolMatrix|FailureAndRepeatMatrix|RepeatedUpgradeCleanup|FailedChildCleansProcessGroup)$"
	if runtime.GOOS == "linux" {
		runCommand(t, filepath.Join(root, "go-agent"), nil, "go", "test", "./internal/app", "-run", pattern, "-count=1")
		return
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatalf("Linux packet matrix requires docker on %s: %v", runtime.GOOS, err)
	}
	runCommand(t, root, nil, "docker", "run", "--rm", "-v", root+":/workspace", "-w", "/workspace/go-agent", "golang:1.26", "go", "test", "./internal/app", "-run", pattern, "-count=1")
}

func runCommand(t *testing.T, dir string, extraEnv []string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), extraEnv...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s failed in %s: %v\n%s", name, strings.Join(args, " "), dir, err, output)
	}
	t.Logf("%s %s\n%s", name, strings.Join(args, " "), output)
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
