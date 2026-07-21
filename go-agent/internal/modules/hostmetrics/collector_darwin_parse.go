package hostmetrics

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

func parseDarwinCPUUsage(output []byte) (float64, error) {
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "CPU usage:") {
			continue
		}
		for _, segment := range strings.Split(line, ",") {
			fields := strings.Fields(segment)
			if len(fields) < 2 || !strings.EqualFold(fields[len(fields)-1], "idle") {
				continue
			}
			idle, err := strconv.ParseFloat(strings.TrimSuffix(fields[len(fields)-2], "%"), 64)
			if err != nil {
				return 0, fmt.Errorf("parse Darwin idle CPU percentage: %w", err)
			}
			if idle < 0 || idle > 100 {
				return 0, fmt.Errorf("Darwin idle CPU percentage %v is outside [0,100]", idle)
			}
			return 100 - idle, nil
		}
		return 0, fmt.Errorf("Darwin CPU usage line has no idle percentage: %q", line)
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("scan Darwin CPU output: %w", err)
	}
	return 0, errors.New("Darwin CPU usage line is unavailable")
}

func parseDarwinAvailableMemory(output []byte) (uint64, error) {
	var pageSize uint64
	var freePages uint64
	var inactivePages uint64
	var foundFree bool
	var foundInactive bool

	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if pageSize == 0 {
			const marker = "page size of "
			if _, rest, found := strings.Cut(line, marker); found {
				fields := strings.Fields(rest)
				if len(fields) > 0 {
					pageSize, _ = strconv.ParseUint(fields[0], 10, 64)
				}
			}
		}
		key, rawValue, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		fields := strings.Fields(rawValue)
		if len(fields) == 0 {
			continue
		}
		value, err := strconv.ParseUint(strings.TrimSuffix(fields[0], "."), 10, 64)
		if err != nil {
			continue
		}
		switch strings.TrimSpace(key) {
		case "Pages free":
			freePages, foundFree = value, true
		case "Pages inactive":
			inactivePages, foundInactive = value, true
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("scan Darwin virtual memory output: %w", err)
	}
	if pageSize == 0 || !foundFree || !foundInactive {
		return 0, errors.New("Darwin virtual memory output is incomplete")
	}
	maxUint64 := ^uint64(0)
	if inactivePages > maxUint64-freePages {
		return maxUint64, nil
	}
	availablePages := freePages + inactivePages
	if availablePages > maxUint64/pageSize {
		return maxUint64, nil
	}
	return availablePages * pageSize, nil
}

func parseDarwinNetworkCounters(output []byte) ([]networkCounter, error) {
	var counters []networkCounter
	seenLinks := make(map[string]struct{})
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 {
			continue
		}
		linkIndex := -1
		for i, field := range fields {
			if strings.HasPrefix(field, "<Link#") {
				linkIndex = i
				break
			}
		}
		if linkIndex < 0 {
			continue
		}

		firstCounter := linkIndex + 1
		if firstCounter >= len(fields) {
			continue
		}
		if _, err := strconv.ParseUint(fields[firstCounter], 10, 64); err != nil {
			firstCounter++
		}
		if firstCounter+5 >= len(fields) {
			continue
		}
		bytesRecv, recvErr := strconv.ParseUint(fields[firstCounter+2], 10, 64)
		bytesSent, sentErr := strconv.ParseUint(fields[firstCounter+5], 10, 64)
		if recvErr != nil || sentErr != nil {
			return nil, fmt.Errorf("parse Darwin network counters for %q", fields[0])
		}
		linkID := fields[linkIndex]
		if _, duplicate := seenLinks[linkID]; duplicate {
			continue
		}
		seenLinks[linkID] = struct{}{}
		counters = append(counters, networkCounter{
			Name:      fields[0],
			BytesRecv: bytesRecv,
			BytesSent: bytesSent,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan Darwin network output: %w", err)
	}
	if len(counters) == 0 {
		return nil, errors.New("Darwin network counters are unavailable")
	}
	return counters, nil
}
