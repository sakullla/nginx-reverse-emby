//go:build !linux

package hosttraffic

func snapshotFromSystem([]string) (Snapshot, error) {
	return Snapshot{}, nil
}
