package pluginsdk_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPublishedModuleIsSelfContained(t *testing.T) {
	if os.Getenv("NRE_SDK_CLEAN_MODULE_TEST") == "1" {
		return
	}
	moduleRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	cleanRoot := filepath.Join(t.TempDir(), "plugin-sdk-go")
	if err := os.CopyFS(cleanRoot, os.DirFS(moduleRoot)); err != nil {
		t.Fatal(err)
	}

	generated := filepath.Join(cleanRoot, "go", "protoschema", "descriptors_clean.go")
	command := exec.Command("go", "run", "./go/protoschema/cmd/generate", "-output", generated)
	command.Dir = cleanRoot
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("descriptor generator failed in clean module copy: %v\n%s", err, output)
	}
	want, err := os.ReadFile(filepath.Join(cleanRoot, "go", "protoschema", "descriptors_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(generated)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("clean module descriptor generation differs from the published source")
	}
	if err := os.Remove(generated); err != nil {
		t.Fatal(err)
	}

	command = exec.Command("go", "test", "./...")
	command.Dir = cleanRoot
	command.Env = append(os.Environ(), "NRE_SDK_CLEAN_MODULE_TEST=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("tests failed in clean module copy without sibling plugin-sdk content: %v\n%s", err, output)
	}
}
