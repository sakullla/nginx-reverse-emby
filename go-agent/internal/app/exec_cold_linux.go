//go:build linux

package app

import "syscall"

func execColdReplacement(binary string, argv, env []string) error {
	return syscall.Exec(binary, argv, env)
}
