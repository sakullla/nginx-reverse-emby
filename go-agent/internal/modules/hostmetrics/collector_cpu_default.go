//go:build !darwin

package hostmetrics

func defaultCPUPercentSampler() cpuPercentFunc {
	return newCPUPercentSampler(readCPUTimes)
}
