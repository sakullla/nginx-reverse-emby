//go:build linux

package pluginhost

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

var controlSandboxUIDs = struct {
	sync.Mutex
	active map[int]struct{}
}{active: make(map[int]struct{})}

func allocateAttemptSandboxUID() (int, func(), error) {
	if os.Geteuid() != 0 {
		return 0, func() {}, nil
	}
	for attempt := 0; attempt < 128; attempt++ {
		var value [4]byte
		if _, err := rand.Read(value[:]); err != nil {
			return 0, nil, err
		}
		uid := 100000 + int(binary.LittleEndian.Uint32(value[:])%2000000000)
		controlSandboxUIDs.Lock()
		_, allocated := controlSandboxUIDs.active[uid]
		inUse := controlLinuxUIDInUse(uid)
		if !allocated && !inUse {
			controlSandboxUIDs.active[uid] = struct{}{}
			controlSandboxUIDs.Unlock()
			var once sync.Once
			return uid, func() {
				once.Do(func() { controlSandboxUIDs.Lock(); delete(controlSandboxUIDs.active, uid); controlSandboxUIDs.Unlock() })
			}, nil
		}
		controlSandboxUIDs.Unlock()
	}
	return 0, nil, errors.New("allocate collision-free control-plane plugin sandbox uid")
}

func controlLinuxUIDInUse(uid int) bool {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return true
	}
	want := strconv.Itoa(uid)
	for _, entry := range entries {
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		body, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "status"))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(body), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 2 && fields[0] == "Uid:" && fields[1] == want {
				return true
			}
		}
	}
	return false
}

func ownAttemptSandboxPaths(root string, uid int) error {
	if uid == 0 {
		return nil
	}
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("control-plane plugin attempt security tree contains a symlink")
		}
		return os.Lchown(path, uid, uid)
	})
}
