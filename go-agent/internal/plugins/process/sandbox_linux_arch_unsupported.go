//go:build linux && !amd64 && !arm64

package process

const (
	linuxSandboxArchSupported = false
	linuxSeccompAuditArch     = uint32(0)
	linuxSeccompX32Bit        = uint32(0)
)

var linuxSeccompProcessCreationSyscalls []uint32
