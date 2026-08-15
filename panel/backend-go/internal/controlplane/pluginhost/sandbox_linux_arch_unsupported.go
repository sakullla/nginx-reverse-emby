//go:build linux && !amd64 && !arm64

package pluginhost

const (
	backendLinuxSandboxArchSupported = false
	backendSeccompAuditArch          = uint32(0)
	backendSeccompX32Bit             = uint32(0)
)
