//go:build windows

package generationsoak

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

func configureProcessTree(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

func killProcessTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, treeErr := exec.CommandContext(ctx, "taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).CombinedOutput()
	processErr := cmd.Process.Kill()
	if errors.Is(processErr, os.ErrProcessDone) {
		processErr = nil
	}
	if treeErr != nil {
		treeErr = fmt.Errorf("taskkill: %w: %s", treeErr, output)
	}
	return errors.Join(treeErr, processErr)
}

func waitProcessTreeGone(cmd *exec.Cmd, timeout time.Duration) error {
	if cmd.Process == nil {
		return nil
	}
	deadline := time.Now().Add(timeout)
	for cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		if time.Now().After(deadline) {
			return fmt.Errorf("root process %d still exists after %s", cmd.Process.Pid, timeout)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}
