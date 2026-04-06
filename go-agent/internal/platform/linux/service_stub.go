//go:build !linux

package linux

import "fmt"

func ServiceUnitName() string {
	return "nginx-reverse-emby-agent"
}

func ExecReplacement(binary string, argv []string, env []string) error {
	return fmt.Errorf("exec replacement is only supported on linux")
}
