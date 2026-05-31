package hosttraffic

import (
	"encoding/binary"
	"reflect"
	"strings"
	"testing"
)

func TestParseBootIDTrimsWhitespace(t *testing.T) {
	if got := parseBootID(strings.NewReader(" boot-123 \n")); got != "boot-123" {
		t.Fatalf("parseBootID() = %q, want boot-123", got)
	}
}

func TestSnapshotPayloadIncludesBootID(t *testing.T) {
	snapshot := Snapshot{
		BootID: "boot-123",
		Total:  Counters{RXBytes: 1000, TXBytes: 2000},
		Interfaces: map[string]Counters{
			"eth0": {RXBytes: 1000, TXBytes: 2000},
		},
	}

	payload := snapshot.Payload()
	host := payload["traffic"].(map[string]any)["host"].(map[string]any)
	if host["boot_id"] != "boot-123" {
		t.Fatalf("boot_id = %#v, want boot-123", host["boot_id"])
	}
}

func TestLinkStats64CountersReadsRXAndTXBytes(t *testing.T) {
	raw := make([]byte, 32)
	binary.NativeEndian.PutUint64(raw[16:24], 1234)
	binary.NativeEndian.PutUint64(raw[24:32], 5678)

	counters, ok := linkStats64Counters(raw)
	if !ok {
		t.Fatal("linkStats64Counters() ok = false")
	}
	if counters.RXBytes != 1234 || counters.TXBytes != 5678 {
		t.Fatalf("counters = %+v, want rx=1234 tx=5678", counters)
	}
}

func TestParseProcNetDevFiltersVirtualInterfaces(t *testing.T) {
	input := `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 100 0 0 0 0 0 0 0 200 0 0 0 0 0 0 0
  eth0: 1000 1 0 0 0 0 0 0 2000 2 0 0 0 0 0 0
docker0: 3000 3 0 0 0 0 0 0 4000 4 0 0 0 0 0 0
vethabc: 5000 5 0 0 0 0 0 0 6000 6 0 0 0 0 0 0
 lxc68a65d636db0: 9000 9 0 0 0 0 0 0 10000 10 0 0 0 0 0 0
cilium_vxlan: 11000 11 0 0 0 0 0 0 12000 12 0 0 0 0 0 0
  ens3: 7000 7 0 0 0 0 0 0 8000 8 0 0 0 0 0 0
`

	snapshot, err := parseProcNetDev(strings.NewReader(input), nil)
	if err != nil {
		t.Fatalf("parseProcNetDev() error = %v", err)
	}

	wantInterfaces := map[string]Counters{
		"eth0": {RXBytes: 1000, TXBytes: 2000},
		"ens3": {RXBytes: 7000, TXBytes: 8000},
	}
	if !reflect.DeepEqual(snapshot.Interfaces, wantInterfaces) {
		t.Fatalf("Interfaces = %#v, want %#v", snapshot.Interfaces, wantInterfaces)
	}
	if snapshot.Total.RXBytes != 8000 || snapshot.Total.TXBytes != 10000 {
		t.Fatalf("Total = %#v, want rx=8000 tx=10000", snapshot.Total)
	}
}

func TestParseProcNetDevHonorsExplicitInterfaces(t *testing.T) {
	input := `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 100 0 0 0 0 0 0 0 200 0 0 0 0 0 0 0
  eth0: 1000 1 0 0 0 0 0 0 2000 2 0 0 0 0 0 0
docker0: 3000 3 0 0 0 0 0 0 4000 4 0 0 0 0 0 0
`

	snapshot, err := parseProcNetDev(strings.NewReader(input), []string{"docker0", "missing"})
	if err != nil {
		t.Fatalf("parseProcNetDev() error = %v", err)
	}

	wantInterfaces := map[string]Counters{
		"docker0": {RXBytes: 3000, TXBytes: 4000},
	}
	if !reflect.DeepEqual(snapshot.Interfaces, wantInterfaces) {
		t.Fatalf("Interfaces = %#v, want %#v", snapshot.Interfaces, wantInterfaces)
	}
	if snapshot.Total.RXBytes != 3000 || snapshot.Total.TXBytes != 4000 {
		t.Fatalf("Total = %#v, want docker0 totals", snapshot.Total)
	}
}
