//go:build linux

package rpc

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

var attemptSandboxUIDs = struct {
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
		attemptSandboxUIDs.Lock()
		_, allocated := attemptSandboxUIDs.active[uid]
		inUse := linuxUIDInUse(uid)
		if !allocated && !inUse {
			attemptSandboxUIDs.active[uid] = struct{}{}
			attemptSandboxUIDs.Unlock()
			var once sync.Once
			return uid, func() {
				once.Do(func() { attemptSandboxUIDs.Lock(); delete(attemptSandboxUIDs.active, uid); attemptSandboxUIDs.Unlock() })
			}, nil
		}
		attemptSandboxUIDs.Unlock()
	}
	return 0, nil, errors.New("allocate collision-free plugin sandbox uid")
}

func linuxUIDInUse(uid int) bool {
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
			return errors.New("plugin attempt security tree contains a symlink")
		}
		return os.Lchown(path, uid, uid)
	})
}
