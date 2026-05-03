package service

import (
	"fmt"
	"strings"
	"time"
)

func normalizeTrafficDirection(direction string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(direction)) {
	case "", "both":
		return "both", nil
	case "rx", "tx", "max":
		return strings.ToLower(strings.TrimSpace(direction)), nil
	default:
		return "", fmt.Errorf("%w: direction must be rx, tx, both, or max", ErrInvalidArgument)
	}
}

func accountedBytes(direction string, rx, tx uint64) uint64 {
	switch normalized, _ := normalizeTrafficDirection(direction); normalized {
	case "rx":
		return rx
	case "tx":
		return tx
	case "max":
		if rx > tx {
			return rx
		}
		return tx
	default:
		return rx + tx
	}
}

func normalizeCycleStartDay(day int) (int, error) {
	if day == 0 {
		return 1, nil
	}
	if day < 1 || day > 28 {
		return 0, fmt.Errorf("%w: cycle_start_day must be between 1 and 28", ErrInvalidArgument)
	}
	return day, nil
}

func monthlyCycleWindow(now time.Time, cycleStartDay int) (time.Time, time.Time) {
	day, err := normalizeCycleStartDay(cycleStartDay)
	if err != nil {
		day = 1
	}
	loc := now.Location()
	candidate := time.Date(now.Year(), now.Month(), day, 0, 0, 0, 0, loc)
	if now.Before(candidate) {
		end := candidate
		start := candidate.AddDate(0, -1, 0)
		return start, end
	}
	start := candidate
	end := candidate.AddDate(0, 1, 0)
	return start, end
}

func quotaPercent(used uint64, quota *int64) float64 {
	if quota == nil {
		return 0
	}
	if *quota == 0 {
		if used == 0 {
			return 0
		}
		return 100
	}
	return float64(used) * 100 / float64(*quota)
}

func quotaRemaining(used uint64, quota *int64) *int64 {
	if quota == nil {
		return nil
	}
	remaining := *quota - int64(minUint64ToInt64(used))
	return &remaining
}

func quotaBlocked(used uint64, policy TrafficPolicy) (bool, string) {
	if !policy.BlockWhenExceeded || !quotaOverLimit(used, policy.MonthlyQuotaBytes) {
		return false, ""
	}
	return true, "monthly quota exceeded"
}

func quotaOverLimit(used uint64, quota *int64) bool {
	if quota == nil {
		return false
	}
	if *quota == 0 {
		return used > 0
	}
	return used >= uint64(*quota)
}

func minUint64ToInt64(value uint64) int64 {
	if value > uint64(^uint64(0)>>1) {
		return int64(^uint64(0) >> 1)
	}
	return int64(value)
}
