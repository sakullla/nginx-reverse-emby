//go:build !windows

package generationsoak

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

func configureProcessTree(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcessTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if err == nil || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func waitProcessTreeGone(cmd *exec.Cmd, timeout time.Duration) error {
	if cmd.Process == nil {
		return nil
	}
	deadline := time.Now().Add(timeout)
	for {
		err := syscall.Kill(-cmd.Process.Pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		if err != nil {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("process group %d still exists after %s", cmd.Process.Pid, timeout)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
