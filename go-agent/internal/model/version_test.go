package model

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestPackageManifestJSONRoundTrip(t *testing.T) {
	want := PackageManifest{
		SchemaVersion: PackageManifestVersion,
		Filename:      "nre-agent-linux-amd64",
		Platform:      "linux-amd64",
		SHA256:        "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Size:          1234,
	}
	payload, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var got PackageManifest
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("manifest round trip = %+v, want %+v", got, want)
	}
}
