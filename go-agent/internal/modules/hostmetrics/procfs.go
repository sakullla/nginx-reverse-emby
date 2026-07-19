package hostmetrics

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

func parseProcStat(r io.Reader) (cpuTimes, error) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 || fields[0] != "cpu" {
			continue
		}
		if len(fields) < 5 {
			return cpuTimes{}, errors.New("aggregate CPU line has too few fields")
		}

		last := len(fields) - 1
		if last > 8 {
			last = 8
		}
		values := make([]uint64, last+1)
		var total uint64
		for i := 1; i <= last; i++ {
			value, err := strconv.ParseUint(fields[i], 10, 64)
			if err != nil {
				return cpuTimes{}, fmt.Errorf("parse aggregate CPU field %d: %w", i, err)
			}
			values[i] = value
			total += value
		}
		idle := values[4]
		if last >= 5 {
			idle += values[5]
		}
		return cpuTimes{Total: total, Idle: idle}, nil
	}
	if err := scanner.Err(); err != nil {
		return cpuTimes{}, fmt.Errorf("read aggregate CPU statistics: %w", err)
	}
	return cpuTimes{}, errors.New("aggregate CPU line is missing")
}

func parseProcMeminfo(r io.Reader) (*memorySnapshot, error) {
	var total uint64
	var free uint64
	var available uint64
	var cached uint64
	var reclaimable uint64
	hasAvailable := false

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		parts := strings.SplitN(scanner.Text(), ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		if key != "MemTotal" && key != "MemFree" && key != "MemAvailable" && key != "Cached" && key != "SReclaimable" {
			continue
		}
		value, err := parseProcMemoryValue(parts[1])
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", key, err)
		}
		switch key {
		case "MemTotal":
			total = value
		case "MemFree":
			free = value
		case "MemAvailable":
			available = value
			hasAvailable = true
		case "Cached":
			cached = value
		case "SReclaimable":
			reclaimable = value
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read memory statistics: %w", err)
	}
	if total == 0 {
		return nil, errors.New("MemTotal is missing or zero")
	}
	if !hasAvailable {
		available = free + cached + reclaimable
	}
	if available > total {
		available = total
	}
	used := total - available
	return &memorySnapshot{
		Total:       total,
		Used:        used,
		UsedPercent: float64(used) / float64(total) * 100,
	}, nil
}

func parseProcMemoryValue(raw string) (uint64, error) {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return 0, errors.New("value is missing")
	}
	value, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil {
		return 0, err
	}
	if len(fields) > 1 && !strings.EqualFold(fields[1], "kB") {
		return 0, fmt.Errorf("unsupported unit %q", fields[1])
	}
	const bytesPerKiB = uint64(1024)
	if value > ^uint64(0)/bytesPerKiB {
		return 0, errors.New("value overflows bytes")
	}
	return value * bytesPerKiB, nil
}

func parseProcNetDev(r io.Reader) ([]networkCounter, error) {
	var counters []networkCounter
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		separator := strings.LastIndexByte(line, ':')
		if separator < 0 {
			continue
		}
		name := strings.TrimSpace(line[:separator])
		fields := strings.Fields(line[separator+1:])
		if name == "" || len(fields) < 9 {
			continue
		}
		received, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse %s received bytes: %w", name, err)
		}
		sent, err := strconv.ParseUint(fields[8], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse %s sent bytes: %w", name, err)
		}
		counters = append(counters, networkCounter{Name: name, BytesRecv: received, BytesSent: sent})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read network statistics: %w", err)
	}
	return counters, nil
}
