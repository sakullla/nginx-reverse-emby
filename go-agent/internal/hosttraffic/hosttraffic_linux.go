//go:build linux

package hosttraffic

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

func snapshotFromSystem(allowed []string) (Snapshot, error) {
	snapshot, err := snapshotFromNetlink(allowed)
	if err == nil {
		snapshot.BootID = readBootID()
		return snapshot, nil
	}
	snapshot, fallbackErr := snapshotFromProcNetDev(allowed)
	if fallbackErr != nil {
		return Snapshot{}, fmt.Errorf("read netlink: %w; read /proc/net/dev: %v", err, fallbackErr)
	}
	snapshot.BootID = readBootID()
	return snapshot, nil
}

func snapshotFromNetlink(allowed []string) (Snapshot, error) {
	allowedSet := selectedInterfaceSet(allowed)
	tab, err := syscall.NetlinkRIB(syscall.RTM_GETLINK, syscall.AF_UNSPEC)
	if err != nil {
		return Snapshot{}, err
	}
	msgs, err := syscall.ParseNetlinkMessage(tab)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot := Snapshot{Interfaces: map[string]Counters{}}
	for _, msg := range msgs {
		switch msg.Header.Type {
		case syscall.NLMSG_DONE:
			return snapshot, nil
		case syscall.RTM_NEWLINK:
			if len(msg.Data) < syscall.SizeofIfInfomsg {
				continue
			}
			ifi := (*syscall.IfInfomsg)(unsafe.Pointer(&msg.Data[0]))
			attrs, err := syscall.ParseNetlinkRouteAttr(&msg)
			if err != nil {
				return Snapshot{}, err
			}
			name, counters, ok := netlinkInterfaceStats(attrs)
			if !ok || !shouldCollectInterfaceWithFlags(name, allowedSet, ifi.Flags) {
				continue
			}
			snapshot.Interfaces[name] = counters
			snapshot.Total.RXBytes += counters.RXBytes
			snapshot.Total.TXBytes += counters.TXBytes
		}
	}
	return snapshot, nil
}

func netlinkInterfaceStats(attrs []syscall.NetlinkRouteAttr) (string, Counters, bool) {
	var name string
	var counters Counters
	var hasCounters bool
	for _, attr := range attrs {
		switch attr.Attr.Type {
		case syscall.IFLA_IFNAME:
			name = strings.TrimRight(string(attr.Value), "\x00")
		case syscall.IFLA_STATS64:
			var ok bool
			counters, ok = linkStats64Counters(attr.Value)
			hasCounters = ok
		}
	}
	return name, counters, name != "" && hasCounters
}

func snapshotFromProcNetDev(allowed []string) (Snapshot, error) {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return Snapshot{}, fmt.Errorf("read /proc/net/dev: %w", err)
	}
	defer f.Close()
	return parseProcNetDev(f, allowed)
}

func readBootID() string {
	f, err := os.Open("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ""
	}
	defer f.Close()
	return parseBootID(f)
}
