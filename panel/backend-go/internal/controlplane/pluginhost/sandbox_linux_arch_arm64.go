//go:build linux && arm64

package pluginhost

import "golang.org/x/sys/unix"

const (
	backendLinuxSandboxArchSupported = true
	backendSeccompAuditArch          = uint32(unix.AUDIT_ARCH_AARCH64)
	backendSeccompX32Bit             = uint32(0)
)
