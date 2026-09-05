package service

import (
	"errors"
	"net/netip"
	"testing"
	"time"
)

func TestDatasetFetchAlwaysRejectsMetadataAndSpecialUseDestinations(t *testing.T) {
	for _, text := range []string{"100.100.100.200", "::ffff:100.100.100.200", "100.64.0.1", "169.254.169.254", "::ffff:169.254.169.254", "fd00:ec2::254", "168.63.129.16", "0.1.2.3", "192.0.2.1", "198.18.0.1", "240.0.0.1", "255.255.255.255", "fe80::1", "2001:db8::1", "2002:a00:1::1", "64:ff9b::6464:64c8", "224.0.0.1", "ff02::1"} {
		address := netip.MustParseAddr(text)
		for _, private := range []bool{false, true} {
			if datasetFetchAddressAllowed(address, private) {
				t.Fatalf("special-use destination %s allowed with private=%v", text, private)
			}
		}
	}
	// The real fetch path must stop before a connection/GET even when private
	// sources are allowed; the assertions above first guard against making an
	// unintended metadata connection if the predicate ever regresses.
	for _, url := range []string{"http://100.100.100.200/meta-data/", "http://[::ffff:100.100.100.200]/meta-data/"} {
		if _, err := fetchDatasetPayload(t.Context(), url, DatasetRetrieval{AllowPrivate: true}, 4096, time.Second); !errors.Is(err, errPluginHostDenied) {
			t.Fatalf("metadata fetch was not denied before dial: %v", err)
		}
	}
}

func TestDatasetFetchPrivateConsentPreservesLegitimatePrivateSources(t *testing.T) {
	for _, text := range []string{"10.1.2.3", "172.16.1.2", "192.168.1.2", "127.0.0.1", "::1", "fd12:3456::1", "::ffff:10.1.2.3"} {
		address := netip.MustParseAddr(text)
		if datasetFetchAddressAllowed(address, false) || !datasetFetchAddressAllowed(address, true) {
			t.Fatalf("private consent policy differs for %s", text)
		}
	}
	for _, text := range []string{"1.1.1.1", "8.8.8.8", "2606:4700:4700::1111"} {
		if !datasetFetchAddressAllowed(netip.MustParseAddr(text), false) {
			t.Fatalf("public source blocked: %s", text)
		}
	}
}
