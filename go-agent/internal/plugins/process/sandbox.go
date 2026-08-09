package process

import (
	"errors"
	"os/exec"
	"strings"
)

const UnsandboxedGrant = "plugin.process.unsandboxed"

type Budget struct {
	CPUMillis   int64
	MemoryBytes int64
	Processes   int
	Files       int
	Network     bool
}

type Security struct {
	Capabilities []string
	Grants       []string
	Budget       Budget
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
	highRisk := false
	for _, capability := range security.Capabilities {
		capability = strings.ToLower(strings.TrimSpace(capability))
		if strings.HasPrefix(capability, "docker.") || strings.HasPrefix(capability, "network.") || strings.HasPrefix(capability, "process.") || strings.HasPrefix(capability, "filesystem.host") {
			highRisk = true
			break
		}
	}
	if sandbox != nil && sandbox.Available() {
		if err := sandbox.Validate(security); err == nil {
			return SandboxDecision{Sandboxed: true, Provider: sandbox.Provider()}, nil
		} else if !hasUnsandboxedGrant(security.Grants) {
			return SandboxDecision{}, err
		}
	}
	if hasUnsandboxedGrant(security.Grants) {
		return SandboxDecision{Sandboxed: false, Provider: "unsandboxed", Reason: "explicit unsandboxed grant"}, nil
	}
	if highRisk {
		return SandboxDecision{}, errors.New("high-risk plugin process capability requires a platform sandbox or explicit plugin.process.unsandboxed grant")
	}
	return SandboxDecision{Sandboxed: false, Provider: "unavailable", Reason: "sandbox unavailable for low-risk plugin"}, nil
}

func hasUnsandboxedGrant(grants []string) bool {
	for _, grant := range grants {
		if strings.TrimSpace(grant) == UnsandboxedGrant {
			return true
		}
	}
	return false
}

func defaultSandbox() Sandbox { return newPlatformSandbox() }
