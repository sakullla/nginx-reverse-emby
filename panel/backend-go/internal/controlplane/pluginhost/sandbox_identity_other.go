//go:build !linux

package pluginhost

func allocateAttemptSandboxUID() (int, func(), error) { return 0, func() {}, nil }
func ownAttemptSandboxPaths(string, int) error        { return nil }
