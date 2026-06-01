//go:build !linux

package app

import "fmt"

func serviceUnitName() string {
	return "nginx-reverse-emby-agent"
}

func execReplacement(binary string, argv []string, env []string) error {
	return fmt.Errorf("exec replacement is only supported on linux")
}
