package pluginhost

import (
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type RuntimeDirectoryReference struct {
	InstanceID string
	Generation string
}

func PruneRuntimeDirectories(root string, references []RuntimeDirectoryReference, cutoff time.Time) (int, error) {
	if strings.TrimSpace(root) == "" || cutoff.IsZero() {
		return 0, errors.New("plugin runtime cleanup root and cutoff are required")
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(absoluteRoot)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	protected := make(map[string]struct{}, len(references))
	for _, reference := range references {
		name, err := runtimeDirectoryName(reference.InstanceID, reference.Generation)
		if err != nil {
			return 0, err
		}
		protected[name] = struct{}{}
	}
	deleted := 0
	for _, entry := range entries {
		if !entry.IsDir() || !managedRuntimeDirectoryName(entry.Name()) {
			continue
		}
		if _, keep := protected[entry.Name()]; keep {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return deleted, err
		}
		if !info.ModTime().Before(cutoff) {
			continue
		}
		candidate := filepath.Join(absoluteRoot, entry.Name())
		relative, err := filepath.Rel(absoluteRoot, candidate)
		if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return deleted, errors.New("plugin runtime cleanup path escapes managed root")
		}
		if err := os.RemoveAll(candidate); err != nil {
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}

func runtimeDirectoryName(instanceID, generation string) (string, error) {
	instanceID = strings.TrimSpace(instanceID)
	generation = strings.TrimSpace(generation)
	if instanceID == "" || generation == "" || filepath.IsAbs(instanceID) || filepath.IsAbs(generation) || strings.ContainsAny(instanceID, `/\\`) || strings.ContainsAny(generation, `/\\`) {
		return "", errors.New("plugin runtime cleanup reference is invalid")
	}
	return instanceID + "-" + generation, nil
}

func managedRuntimeDirectoryName(name string) bool {
	separator := strings.LastIndexByte(name, '-')
	if separator <= 0 || len(name)-separator-1 != 64 {
		return false
	}
	digest := name[separator+1:]
	decoded, err := hex.DecodeString(digest)
	return err == nil && len(decoded) == 32 && digest == strings.ToLower(digest)
}
