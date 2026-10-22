package dockerproxy

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

type scriptedRunner struct {
	calls [][]string
	dirs  []string
	steps []scriptedStep
}

type scriptedStep struct {
	args   []string
	output []byte
	err    error
}

func (runner *scriptedRunner) Run(_ context.Context, dir string, args ...string) ([]byte, error) {
	runner.calls = append(runner.calls, append([]string(nil), args...))
	runner.dirs = append(runner.dirs, dir)
	if len(runner.calls) > len(runner.steps) {
		return nil, nil
	}
	step := runner.steps[len(runner.calls)-1]
	return step.output, step.err
}

func TestValidateArgsAllowsOnlyVolumeArchiveRestoreShapes(t *testing.T) {
	t.Parallel()
	allowed := [][]string{
		{"volume", "archive", "mydata"},
		{"volume", "restore", "mydata"},
		{"volume", "archive", "vol.v2"},
	}
	for _, args := range allowed {
		if err := validateArgs(args); err != nil {
			t.Fatalf("validateArgs(%q) rejected managed volume shape: %v", args, err)
		}
	}
	denied := [][]string{
		{"volume"},
		{"volume", "archive"},
		{"volume", "archive", "mydata", "extra"},
		{"volume", "prune", "-f"},
		{"volume", "archive", "-flag"},
		{"volume", "archive", "a/b"},
		{"volume", "inspect", "mydata"},
	}
	for _, args := range denied {
		if err := validateArgs(args); err == nil {
			t.Fatalf("validateArgs(%q) accepted an unmanaged volume shape", args)
		}
	}
}

func TestVolumeOpArchiveRunsPinnedHelperAndStagesUnderWorkspace(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	runner := &scriptedRunner{steps: []scriptedStep{
		{args: []string{"volume", "inspect", "mydata"}},
		{args: []string{"run"}},
	}}
	handler := &handler{workspaceRoot: root, runner: runner}
	response, err := handler.volumeOp(context.Background(), Request{Args: pluginsdk.DockerVolumeArchiveArgs("mydata"), AppID: "app1"})
	if err != nil {
		t.Fatalf("volume archive failed: %v", err)
	}
	if response.ExitCode != 0 || response.Error != "" {
		t.Fatalf("volume archive response = %+v", response)
	}
	wantArchive := "app1/backup-staging/volumes/mydata.tar"
	if response.ArchivePath != wantArchive {
		t.Fatalf("archive path = %q want %q", response.ArchivePath, wantArchive)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("runner calls = %d want 2", len(runner.calls))
	}
	run := runner.calls[1]
	joined := strings.Join(run, " ")
	for _, want := range []string{"run", "--rm", "--mount", "type=volume,source=mydata,target=/volume", pluginsdk.DockerVolumeHelperImage, "tar", "cf", "/archive/mydata.tar"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("helper run args %q missing %q", joined, want)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "app1", "backup-staging", "volumes")); err != nil {
		t.Fatalf("staging directory was not created: %v", err)
	}
}

func TestVolumeOpArchiveFailsWhenVolumeMissing(t *testing.T) {
	t.Parallel()
	runner := &scriptedRunner{steps: []scriptedStep{{err: os.ErrNotExist}}}
	handler := &handler{workspaceRoot: t.TempDir(), runner: runner}
	_, err := handler.volumeOp(context.Background(), Request{Args: pluginsdk.DockerVolumeArchiveArgs("ghost"), AppID: "app1"})
	if err == nil || !strings.Contains(err.Error(), "was not found") {
		t.Fatalf("missing volume error = %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("helper must not run for a missing volume, calls = %d", len(runner.calls))
	}
}

func TestVolumeOpRestoreRequiresStagedArchiveAndCreatesVolume(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	handler := &handler{workspaceRoot: root, runner: &scriptedRunner{}}
	if _, err := handler.volumeOp(context.Background(), Request{Args: pluginsdk.DockerVolumeRestoreArgs("mydata"), AppID: "app1"}); err == nil {
		t.Fatal("restore without a staged archive succeeded")
	}
	staged := filepath.Join(root, "app1", "backup-staging", "volumes", "mydata.tar")
	if err := os.MkdirAll(filepath.Dir(staged), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staged, []byte("tar"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &scriptedRunner{steps: []scriptedStep{{}, {}}}
	handler.runner = runner
	response, err := handler.volumeOp(context.Background(), Request{Args: pluginsdk.DockerVolumeRestoreArgs("mydata"), AppID: "app1"})
	if err != nil {
		t.Fatalf("volume restore failed: %v", err)
	}
	if response.ExitCode != 0 || response.Error != "" {
		t.Fatalf("volume restore response = %+v", response)
	}
	if !reflect.DeepEqual(runner.calls[0], []string{"volume", "create", "mydata"}) {
		t.Fatalf("restore must create the volume first, got %q", runner.calls[0])
	}
	joined := strings.Join(runner.calls[1], " ")
	for _, want := range []string{pluginsdk.DockerVolumeHelperImage, "tar", "xf", "/archive/mydata.tar"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("helper run args %q missing %q", joined, want)
		}
	}
}

func TestVolumeOpRejectsInvalidRequestShape(t *testing.T) {
	t.Parallel()
	handler := &handler{workspaceRoot: t.TempDir(), runner: &scriptedRunner{}}
	requests := []Request{
		{Args: []string{"volume", "archive", "mydata"}},
		{Args: []string{"volume", "archive", "mydata"}, AppID: "app1", Compose: "services: {}"},
	}
	for _, request := range requests {
		if _, err := handler.volumeOp(context.Background(), request); err == nil {
			t.Fatalf("volume request %+v accepted", request)
		}
	}
}
