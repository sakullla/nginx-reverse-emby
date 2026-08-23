package process

import (
	"errors"
	"os/exec"
)

// UnsandboxedGrant is a legacy persisted grant name. Admission ignores it.
const UnsandboxedGrant = "plugin.process.unsandboxed"

type Budget struct {
	CPUMillis   int64
	MemoryBytes int64
	Processes   int
	Files       int
	Network     bool
}

type Security struct {
	Requirement                                           SandboxRequirement
	Grants                                                []string
	EndpointDirectory, CredentialDirectory, GuestEndpoint string
	ArtifactDigest, Generation, CookieDigest              string
	SandboxUID                                            int
	DirectoryBindings                                     []DirectoryBinding
}

// DirectoryBinding is a Host-authorized directory exposed at the same
// absolute location inside an isolated plugin process. The Host opens the
// source before launch so path replacement cannot retarget the mount.
type DirectoryBinding struct {
	HostPath  string
	GuestPath string
	ReadOnly  bool
}

type SandboxDecision struct {
	Sandboxed bool
	Provider  string
	Reason    string
}

type Sandbox interface {
	Available() bool
	Provider() string
	Validate(Security) error
	Configure(*exec.Cmd, Security) (startCleanup func() error, processCleanup func() error, afterStart func(int) error, err error)
}

func DecideSandbox(sandbox Sandbox, security Security) (SandboxDecision, error) {
	if sandbox != nil && sandbox.Available() {
		if err := sandbox.Validate(security); err == nil {
			return SandboxDecision{Sandboxed: true, Provider: sandbox.Provider()}, nil
		} else {
			return SandboxDecision{}, err
		}
	}
	return SandboxDecision{}, errors.New("plugin process isolation is unavailable on this platform")
}

func defaultSandbox() Sandbox { return newPlatformSandbox() }
