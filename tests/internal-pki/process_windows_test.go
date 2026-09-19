//go:build integration && windows

package internalpki

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

const createNewProcessGroup = 0x00000200

func configureProcessTree(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
}

func cleanupProcessTreeFiles(path string) error {
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for {
		resetTestTreeACL(path)
		if err := os.RemoveAll(path); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if !time.Now().Before(deadline) {
			return errors.Join(errors.New("timed out waiting for Windows process files to close"), lastErr)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func resetTestTreeACL(path string) {
	icacls := "icacls.exe"
	if systemRoot := os.Getenv("SystemRoot"); systemRoot != "" {
		candidate := filepath.Join(systemRoot, "System32", "icacls.exe")
		if _, err := os.Stat(candidate); err == nil {
			icacls = candidate
		}
	}
	_ = exec.Command(icacls, path, "/reset", "/T", "/C", "/Q").Run()
}

func terminateProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	taskkill := "taskkill.exe"
	if systemRoot := os.Getenv("SystemRoot"); systemRoot != "" {
		candidate := filepath.Join(systemRoot, "System32", "taskkill.exe")
		if _, err := os.Stat(candidate); err == nil {
			taskkill = candidate
		}
	}
	_ = exec.Command(taskkill, "/PID", fmtInt(cmd.Process.Pid), "/T", "/F").Run()
}
