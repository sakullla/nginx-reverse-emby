package pluginsdk_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPublishedModuleIsSelfContained(t *testing.T) {
	moduleRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	cleanRoot := filepath.Join(t.TempDir(), "plugin-sdk-go")
	if err := os.CopyFS(cleanRoot, os.DirFS(moduleRoot)); err != nil {
		t.Fatal(err)
	}

	command := exec.Command("go", "list", "-deps", "-mod=readonly", "./...")
	command.Dir = cleanRoot
	command.Env = append(os.Environ(), "GOWORK=off")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("published module depends on content outside its archive: %v\n%s", err, output)
	}
}
