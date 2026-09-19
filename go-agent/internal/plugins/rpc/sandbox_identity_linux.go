//go:build linux

package rpc

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var attemptSandboxUIDs = struct {
	sync.Mutex
	active map[int]sandboxUIDLease
}{active: make(map[int]sandboxUIDLease)}

var (
	attemptSandboxUIDLeaseRoot = "/run/nre-agent/plugin-sandbox-uids"
	linuxUIDUsageForSandbox    = linuxUIDUsage
)

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
		if allocated {
			attemptSandboxUIDs.Unlock()
			continue
		}
		leasePath := ""
		if identity != "" {
			claimed, created, path, err := claimPersistentSandboxUID(uid, identity)
			if err != nil {
				attemptSandboxUIDs.Unlock()
				return 0, nil, err
			}
			if !claimed {
				attemptSandboxUIDs.Unlock()
				continue
			}
			leasePath = path
			inUse, pluginSandboxOnly := linuxUIDUsageForSandbox(uid)
			if created && inUse && !pluginSandboxOnly {
				_ = os.Remove(leasePath)
				attemptSandboxUIDs.Unlock()
				continue
			}
			attemptSandboxUIDs.active[uid] = sandboxUIDLease{identity: identity, refs: 1}
			attemptSandboxUIDs.Unlock()
			var once sync.Once
			return uid, func() { once.Do(func() { releaseAttemptSandboxUID(uid, identity) }) }, nil
		}
		inUse, _ := linuxUIDUsageForSandbox(uid)
		if !inUse {
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

func claimPersistentSandboxUID(uid int, identity string) (bool, bool, string, error) {
	root := filepath.Clean(attemptSandboxUIDLeaseRoot)
	if root == "" || !filepath.IsAbs(root) {
		return false, false, "", errors.New("plugin sandbox uid lease root is invalid")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return false, false, "", err
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return false, false, "", errors.New("plugin sandbox uid lease root is not private")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 {
		return false, false, "", errors.New("plugin sandbox uid lease root is not root-owned")
	}
	leasePath := filepath.Join(root, strconv.Itoa(uid))
	digest := sha256.Sum256([]byte(identity))
	want := []byte(fmt.Sprintf("%x\n", digest[:]))
	file, err := os.OpenFile(leasePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		if _, writeErr := file.Write(want); writeErr != nil {
			_ = file.Close()
			_ = os.Remove(leasePath)
			return false, false, "", writeErr
		}
		if syncErr := file.Sync(); syncErr != nil {
			_ = file.Close()
			_ = os.Remove(leasePath)
			return false, false, "", syncErr
		}
		if closeErr := file.Close(); closeErr != nil {
			_ = os.Remove(leasePath)
			return false, false, "", closeErr
		}
		return true, true, leasePath, nil
	}
	if !errors.Is(err, os.ErrExist) {
		return false, false, "", err
	}
	info, err = os.Lstat(leasePath)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return false, false, "", errors.New("plugin sandbox uid lease is invalid")
	}
	stat, ok = info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 {
		return false, false, "", errors.New("plugin sandbox uid lease is not root-owned")
	}
	for attempt := 0; attempt < 20; attempt++ {
		current, readErr := os.ReadFile(leasePath)
		if readErr != nil {
			return false, false, "", readErr
		}
		if len(current) > 0 {
			return string(current) == string(want), false, leasePath, nil
		}
		time.Sleep(time.Millisecond)
	}
	return false, false, "", errors.New("plugin sandbox uid lease is incomplete")
}

func linuxUIDUsage(uid int) (bool, bool) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return true, false
	}
	want := strconv.Itoa(uid)
	found := false
	pluginSandboxOnly := true
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
				found = true
				cmdline, readErr := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
				if readErr != nil || string(bytes.SplitN(cmdline, []byte{0}, 2)[0]) != "/plugin/plugin" {
					pluginSandboxOnly = false
				}
				break
			}
		}
	}
	return found, found && pluginSandboxOnly
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
