package hosttraffic

import (
	"bufio"
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
	return snapshotFromSystem(c.selectedInterfaces())
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

func parseProcNetDev(r io.Reader, allowed []string) (Snapshot, error) {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		allowedSet[name] = struct{}{}
	}
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
	if len(allowed) > 0 {
		_, ok := allowed[name]
		return ok
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
