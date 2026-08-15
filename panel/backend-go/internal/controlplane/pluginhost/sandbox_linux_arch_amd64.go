//go:build linux && amd64

package pluginhost

import "golang.org/x/sys/unix"

const (
	backendLinuxSandboxArchSupported = true
	backendSeccompAuditArch          = uint32(unix.AUDIT_ARCH_X86_64)
	backendSeccompX32Bit             = uint32(0x40000000)
)
