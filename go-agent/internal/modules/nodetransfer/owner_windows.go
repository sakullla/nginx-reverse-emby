//go:build windows

package nodetransfer

// Ownership is only meaningful on unix; non-unix builds skip chown.
func inheritWorkspaceOwner(workspaceRoot, instanceID string, paths ...string) error {
	return nil
}
