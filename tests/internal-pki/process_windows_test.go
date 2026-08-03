//go:build integration && windows

package internalpki

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

const createNewProcessGroup = 0x00000200

func configureProcessTree(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
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
