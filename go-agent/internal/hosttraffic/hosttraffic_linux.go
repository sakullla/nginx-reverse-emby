//go:build linux

package hosttraffic

import (
	"fmt"
	"os"
)

func snapshotFromSystem(allowed []string) (Snapshot, error) {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return Snapshot{}, fmt.Errorf("read /proc/net/dev: %w", err)
	}
	defer f.Close()
	return parseProcNetDev(f, allowed)
}
