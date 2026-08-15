//go:build !linux

package rpc

func allocateAttemptSandboxUID() (int, func(), error) { return 0, func() {}, nil }
func ownAttemptSandboxPaths(string, int) error        { return nil }
