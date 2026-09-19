package cloudflare

import (
	"encoding/binary"
	"testing"
)

func TestDNSWireCanonicalQueryAndPointerSafety(t *testing.T) {
	for _, test := range []struct {
		name    string
		value   string
		want    string
		wantErr bool
	}{
		{name: "canonical", value: "_ACME-Challenge.Example.COM.", want: "_acme-challenge.example.com"},
		{name: "root", value: ".", wantErr: true},
		{name: "invalid label", value: "bad/name.example", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := normalizeDNSName(test.value)
			if (err != nil) != test.wantErr || (!test.wantErr && got != test.want) {
				t.Fatalf("normalizeDNSName(%q) = %q, %v", test.value, got, err)
			}
		})
	}

	query, err := encodeDNSQuery(0x1234, "_acme-challenge.example.com", TypeTXT)
	if err != nil {
		t.Fatalf("encodeDNSQuery() error = %v", err)
	}
	name, offset, err := decodeDNSName(query, 12)
	if err != nil {
		t.Fatalf("decodeDNSName(query) error = %v", err)
	}
	if name != "_acme-challenge.example.com" || offset+4 != len(query) || binary.BigEndian.Uint16(query[offset:offset+2]) != uint16(TypeTXT) {
		t.Fatalf("encoded query name=%q offset=%d bytes=%v", name, offset, query)
	}
	for _, malformed := range [][]byte{{0xc0, 0x00}, {0xc0, 0x02, 0x00}, {0xc0}} {
		if _, _, err := decodeDNSName(malformed, 0); err == nil {
			t.Fatalf("decodeDNSName(%v) accepted unsafe compression pointer", malformed)
		}
	}
}
