package plugins

import "os"

// stableFileKey is comparable so hardlink detection remains O(n) at the
// maximum market file count. The platform implementation supplies a volume
// identity plus the filesystem's stable file identifier.
type stableFileKey struct {
	volume uint64
	high   uint64
	low    uint64
}

type stableFileSet map[stableFileKey]struct{}

func newStableFileSet(capacity int) stableFileSet {
	return make(stableFileSet, capacity)
}

// add returns false when the same platform file identity already exists.
func (set stableFileSet) add(key stableFileKey) bool {
	if _, duplicate := set[key]; duplicate {
		return false
	}
	set[key] = struct{}{}
	return true
}

func stableRegularFileKey(name string, info os.FileInfo) (stableFileKey, error) {
	return platformStableFileKey(name, info)
}
