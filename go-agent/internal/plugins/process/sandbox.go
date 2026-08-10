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
	Requirement                                           SandboxRequirement
	Grants                                                []string
	EndpointDirectory, CredentialDirectory, GuestEndpoint string
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

type defenseInDepthSandbox interface {
	DefenseInDepth() bool
}

func requiresDefenseInDepth(sandbox Sandbox, security Security) bool {
	defense, ok := sandbox.(defenseInDepthSandbox)
	return ok && defense.DefenseInDepth() && hasUnsandboxedGrant(security.Grants)
}

func DecideSandbox(sandbox Sandbox, security Security) (SandboxDecision, error) {
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
	if security.Requirement.HighRisk() {
		return SandboxDecision{}, errors.New("high-risk plugin sandbox requirement requires a platform sandbox or explicit plugin.process.unsandboxed grant")
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
