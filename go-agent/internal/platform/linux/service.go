//go:build linux

package linux

import "syscall"

func ServiceUnitName() string {
	return "nginx-reverse-emby-agent"
}

func ExecReplacement(binary string, argv []string, env []string) error {
	return syscall.Exec(binary, argv, env)
}
