//go:build linux

package rpc

import (
	"crypto/rand"
	"crypto/sha256"
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
	active map[int]sandboxUIDLease
}{active: make(map[int]sandboxUIDLease)}

type sandboxUIDLease struct {
	identity string
	refs     int
}

func allocateAttemptSandboxUID(identities ...string) (int, func(), error) {
	if os.Geteuid() != 0 {
		return 0, func() {}, nil
	}
	identity := ""
	if len(identities) > 0 {
		identity = strings.TrimSpace(identities[0])
	}
	for attempt := 0; attempt < 128; attempt++ {
		var value uint32
		if identity != "" {
			digest := sha256.Sum256([]byte(identity + "\x00" + strconv.Itoa(attempt)))
			value = binary.LittleEndian.Uint32(digest[:4])
		} else {
			var random [4]byte
			if _, err := rand.Read(random[:]); err != nil {
				return 0, nil, err
			}
			value = binary.LittleEndian.Uint32(random[:])
		}
		uid := 100000 + int(value%2000000000)
		attemptSandboxUIDs.Lock()
		lease, allocated := attemptSandboxUIDs.active[uid]
		if allocated && identity != "" && lease.identity == identity {
			lease.refs++
			attemptSandboxUIDs.active[uid] = lease
			attemptSandboxUIDs.Unlock()
			var once sync.Once
			return uid, func() {
				once.Do(func() { releaseAttemptSandboxUID(uid, identity) })
			}, nil
		}
		inUse := linuxUIDInUse(uid)
		if !allocated && !inUse {
			attemptSandboxUIDs.active[uid] = sandboxUIDLease{identity: identity, refs: 1}
			attemptSandboxUIDs.Unlock()
			var once sync.Once
			return uid, func() { once.Do(func() { releaseAttemptSandboxUID(uid, identity) }) }, nil
		}
		attemptSandboxUIDs.Unlock()
	}
	return 0, nil, errors.New("allocate collision-free plugin sandbox uid")
}

func releaseAttemptSandboxUID(uid int, identity string) {
	attemptSandboxUIDs.Lock()
	defer attemptSandboxUIDs.Unlock()
	lease, ok := attemptSandboxUIDs.active[uid]
	if !ok || lease.identity != identity {
		return
	}
	lease.refs--
	if lease.refs <= 0 {
		delete(attemptSandboxUIDs.active, uid)
		return
	}
	attemptSandboxUIDs.active[uid] = lease
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
