//go:build linux && integration && ssplugin

package rpc

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func runSSCapacity(t *testing.T, work, pluginRoot string, port int, password, count string, pluginPID int) {
	t.Helper()
	n, err := strconv.Atoi(count)
	if err != nil || n < 3000 || n > 4096 {
		t.Fatal("NRE_SS_CAPACITY must be 3000..4096")
	}
	binary := filepath.Join(work, "ss-capacity-client")
	runGo(t, pluginRoot, "test", "-c", "-tags=capacity", "-o", binary, "./plugins/shadowsocks-server")
	config, _ := json.Marshal(struct {
		Endpoint, Password string
		Connections        int
	}{net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), password, n})
	command := exec.CommandContext(t.Context(), binary, "-test.run=^TestManagedShadowsocksCapacity$", "-test.v", "-test.timeout=4m")
	command.Env = append(os.Environ(), "NRE_SS_CAPACITY_CONFIG="+string(config))
	baselineFDs, _ := os.ReadDir("/proc/self/fd")
	done := make(chan struct{})
	peaks := make(chan [2]int64, 1)
	go func() {
		var peak [2]int64
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				peaks <- peak
				return
			case <-ticker.C:
				peak[0] = max(peak[0], ssCapacityProcessRSS(os.Getpid(), false))
				peak[1] = max(peak[1], ssCapacityProcessRSS(pluginPID, true))
			}
		}
	}()
	output, err := command.CombinedOutput()
	close(done)
	peak := <-peaks
	t.Logf("capacity host_peak_rss_mib=%.1f plugin_tree_peak_rss_mib=%.1f\n%s", float64(peak[0])/(1<<20), float64(peak[1])/(1<<20), output)
	if err != nil {
		t.Fatalf("capacity client: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		fds, _ := os.ReadDir("/proc/self/fd")
		if len(fds) <= len(baselineFDs)+128 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("host sockets were not reclaimed: before=%d after=%d", len(baselineFDs), len(fds))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func ssCapacityProcessRSS(pid int, children bool) int64 {
	data, _ := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "status"))
	var total int64
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			fields := strings.Fields(line)
			if len(fields) > 1 {
				kb, _ := strconv.ParseInt(fields[1], 10, 64)
				total = kb * 1024
			}
		}
	}
	if children {
		data, _ = os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "task", strconv.Itoa(pid), "children"))
		for _, field := range strings.Fields(string(data)) {
			child, _ := strconv.Atoi(field)
			total += ssCapacityProcessRSS(child, true)
		}
	}
	return total
}
