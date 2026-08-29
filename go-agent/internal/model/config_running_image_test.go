package model

import (
	"runtime"
	"testing"
)

func TestExecutableSHA256PrefersRunningImageOnLinux(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("running-image digest is the Linux /proc/self/exe path")
	}
	got := RunningExecutableSHA256("/this/path/does/not/exist")
	want := hashFile("/proc/self/exe")
	if got == "" || got != want {
		t.Fatalf("RunningExecutableSHA256() = %q, want running image %q", got, want)
	}
}

func TestHashFileEmptyPath(t *testing.T) {
	t.Parallel()
	if hashFile("") != "" {
		t.Fatal("hashFile(\"\") must be empty")
	}
}
