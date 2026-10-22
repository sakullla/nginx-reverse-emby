//go:build unix

package nodetransfer

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// inheritWorkspaceOwner makes newly created paths readable by the sandboxed
// plugin by copying the owner of the instance workspace directory. The
// per-attempt sandbox UID is attempt state this app-level service cannot see;
// the workspace directory owner is the authoritative source for it.
func inheritWorkspaceOwner(workspaceRoot, instanceID string, paths ...string) error {
	info, err := os.Stat(filepath.Join(workspaceRoot, instanceID))
	if err != nil || !info.IsDir() {
		return errors.New("node transfer instance workspace is not owned")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid <= 0 {
		return nil
	}
	for _, path := range paths {
		if err := os.Chown(path, int(stat.Uid), -1); err != nil {
			return errors.New("node transfer artifact ownership could not be assigned")
		}
	}
	return nil
}
