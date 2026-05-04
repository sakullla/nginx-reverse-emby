package hosttraffic

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseProcNetDevFiltersVirtualInterfaces(t *testing.T) {
	input := `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 100 0 0 0 0 0 0 0 200 0 0 0 0 0 0 0
  eth0: 1000 1 0 0 0 0 0 0 2000 2 0 0 0 0 0 0
docker0: 3000 3 0 0 0 0 0 0 4000 4 0 0 0 0 0 0
vethabc: 5000 5 0 0 0 0 0 0 6000 6 0 0 0 0 0 0
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
