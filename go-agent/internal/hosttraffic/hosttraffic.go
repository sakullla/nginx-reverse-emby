package hosttraffic

import (
	"bufio"
	"encoding/binary"
	"io"
	"sort"
	"strconv"
	"strings"
)

type Counters struct {
	RXBytes uint64
	TXBytes uint64
}

type Snapshot struct {
	BootID     string
	Total      Counters
	Interfaces map[string]Counters
}

type Collector struct {
	interfaces map[string]struct{}
}

func NewCollector(interfaces []string) *Collector {
	allowed := make(map[string]struct{}, len(interfaces))
	for _, name := range interfaces {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		allowed[name] = struct{}{}
	}
	return &Collector{interfaces: allowed}
}

func (c *Collector) Snapshot() (Snapshot, error) {
	snapshot, err := snapshotFromSystem(c.selectedInterfaces())
	if err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func (c *Collector) selectedInterfaces() []string {
	if c == nil || len(c.interfaces) == 0 {
		return nil
	}
	names := make([]string, 0, len(c.interfaces))
	for name := range c.interfaces {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (s Snapshot) Payload() map[string]any {
	interfaces := make(map[string]any, len(s.Interfaces))
	for name, counters := range s.Interfaces {
		interfaces[name] = map[string]any{
			"rx_bytes": counters.RXBytes,
			"tx_bytes": counters.TXBytes,
		}
	}
	host := map[string]any{
		"total": map[string]any{
			"rx_bytes": s.Total.RXBytes,
			"tx_bytes": s.Total.TXBytes,
		},
		"interfaces": interfaces,
	}
	if s.BootID != "" {
		host["boot_id"] = s.BootID
	}
	return map[string]any{"traffic": map[string]any{"host": host}}
}

func parseBootID(r io.Reader) string {
	payload, err := io.ReadAll(r)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(payload))
}

func linkStats64Counters(raw []byte) (Counters, bool) {
	if len(raw) < 32 {
		return Counters{}, false
	}
	return Counters{
		RXBytes: binary.NativeEndian.Uint64(raw[16:24]),
		TXBytes: binary.NativeEndian.Uint64(raw[24:32]),
	}, true
}

func parseProcNetDev(r io.Reader, allowed []string) (Snapshot, error) {
	allowedSet := selectedInterfaceSet(allowed)
	scanner := bufio.NewScanner(r)
	snapshot := Snapshot{Interfaces: map[string]Counters{}}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.Contains(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "Inter-") || strings.HasPrefix(line, "face") {
			continue
		}
		name, counters, ok := parseProcNetDevLine(line)
		if !ok {
			continue
		}
		if !shouldCollectInterface(name, allowedSet) {
			continue
		}
		snapshot.Interfaces[name] = counters
		snapshot.Total.RXBytes += counters.RXBytes
		snapshot.Total.TXBytes += counters.TXBytes
	}
	if err := scanner.Err(); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func selectedInterfaceSet(allowed []string) map[string]struct{} {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		allowedSet[name] = struct{}{}
	}
	return allowedSet
}

func parseProcNetDevLine(line string) (string, Counters, bool) {
	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 {
		return "", Counters{}, false
	}
	name := strings.TrimSpace(parts[0])
	fields := strings.Fields(parts[1])
	if len(fields) < 16 {
		return "", Counters{}, false
	}
	rx, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil {
		return "", Counters{}, false
	}
	tx, err := strconv.ParseUint(fields[8], 10, 64)
	if err != nil {
		return "", Counters{}, false
	}
	return name, Counters{RXBytes: rx, TXBytes: tx}, true
}

func shouldCollectInterface(name string, allowed map[string]struct{}) bool {
	return shouldCollectInterfaceWithFlags(name, allowed, 0)
}

func shouldCollectInterfaceWithFlags(name string, allowed map[string]struct{}, flags uint32) bool {
	if len(allowed) > 0 {
		_, ok := allowed[name]
		return ok
	}
	if flags&0x8 != 0 {
		return false
	}
	switch {
	case name == "lo":
		return false
	case strings.HasPrefix(name, "docker"):
		return false
	case strings.HasPrefix(name, "br-"):
		return false
	case strings.HasPrefix(name, "veth"):
		return false
	case strings.HasPrefix(name, "lxc"):
		return false
	case strings.HasPrefix(name, "cilium_"):
		return false
	case strings.HasPrefix(name, "virbr"):
		return false
	case strings.HasPrefix(name, "tun"):
		return false
	case strings.HasPrefix(name, "tap"):
		return false
	default:
		return true
	}
}
