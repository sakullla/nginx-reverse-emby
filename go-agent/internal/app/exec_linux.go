//go:build linux

package app

import "syscall"

func serviceUnitName() string {
	return "nginx-reverse-emby-agent"
}

func execReplacement(binary string, argv []string, env []string) error {
	return syscall.Exec(binary, argv, env)
}
