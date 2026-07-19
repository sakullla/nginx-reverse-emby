package revision

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestSemanticSnapshotDigestIgnoresOrderingAndRevisionMetadata(t *testing.T) {
	t.Parallel()
	first := storage.Snapshot{
		Revision: 12,
		Rules: []storage.HTTPRule{
			{ID: 2, AgentID: "edge-1", FrontendURL: "https://b.example.com", Revision: 12},
			{ID: 1, AgentID: "edge-1", FrontendURL: "https://a.example.com", Revision: 11},
		},
		RelayListeners: []storage.RelayListener{
			{ID: 2, AgentID: "edge-1", BindHosts: []string{"127.0.0.2", "127.0.0.1"}, Tags: []string{"z", "a"}, Revision: 12},
		},
	}
	second := storage.Snapshot{
		Revision: 99,
		Rules: []storage.HTTPRule{
			{ID: 1, AgentID: "edge-1", FrontendURL: "https://a.example.com", Revision: 98},
			{ID: 2, AgentID: "edge-1", FrontendURL: "https://b.example.com", Revision: 99},
		},
		RelayListeners: []storage.RelayListener{
			{ID: 2, AgentID: "edge-1", BindHosts: []string{"127.0.0.1", "127.0.0.2"}, Tags: []string{"a", "z"}, Revision: 99},
		},
	}

	firstDigest, err := SemanticSnapshotDigest(first)
	if err != nil {
		t.Fatalf("SemanticSnapshotDigest(first) error = %v", err)
	}
	secondDigest, err := SemanticSnapshotDigest(second)
	if err != nil {
		t.Fatalf("SemanticSnapshotDigest(second) error = %v", err)
	}
	if firstDigest != secondDigest {
		t.Fatalf("semantic digests differ: %s != %s", firstDigest, secondDigest)
	}

	payload, payloadDigest, err := CanonicalSnapshotPayload(first)
	if err != nil {
		t.Fatalf("CanonicalSnapshotPayload() error = %v", err)
	}
	if len(payload) == 0 || payloadDigest == "" {
		t.Fatalf("canonical payload len=%d digest=%q", len(payload), payloadDigest)
	}
	if payloadDigest == firstDigest {
		t.Fatal("artifact digest unexpectedly ignores revision metadata")
	}
}

func TestCanonicalSnapshotPreservesConfiguredBackendOrder(t *testing.T) {
	t.Parallel()
	snapshot := storage.Snapshot{
		Revision: 7,
		Rules: []storage.HTTPRule{{
			ID: 1, Backends: []storage.HTTPBackend{{URL: "https://second.example"}, {URL: "https://first.example"}},
		}},
		L4Rules: []storage.L4Rule{{
			ID: 2, Backends: []storage.L4Backend{{Host: "second.example", Port: 9002}, {Host: "first.example", Port: 9001}},
		}},
	}
	payload, _, err := CanonicalSnapshotPayload(snapshot)
	if err != nil {
		t.Fatalf("CanonicalSnapshotPayload() error = %v", err)
	}
	var delivered storage.Snapshot
	if err := json.Unmarshal(payload, &delivered); err != nil {
		t.Fatalf("decode canonical payload: %v", err)
	}
	if !reflect.DeepEqual(delivered.Rules[0].Backends, snapshot.Rules[0].Backends) {
		t.Fatalf("HTTP backend order = %+v, want %+v", delivered.Rules[0].Backends, snapshot.Rules[0].Backends)
	}
	if !reflect.DeepEqual(delivered.L4Rules[0].Backends, snapshot.L4Rules[0].Backends) {
		t.Fatalf("L4 backend order = %+v, want %+v", delivered.L4Rules[0].Backends, snapshot.L4Rules[0].Backends)
	}

	reordered := snapshot
	reordered.Rules = append([]storage.HTTPRule(nil), snapshot.Rules...)
	reordered.Rules[0].Backends = []storage.HTTPBackend{{URL: "https://first.example"}, {URL: "https://second.example"}}
	reordered.L4Rules = append([]storage.L4Rule(nil), snapshot.L4Rules...)
	reordered.L4Rules[0].Backends = []storage.L4Backend{{Host: "first.example", Port: 9001}, {Host: "second.example", Port: 9002}}
	firstDigest, err := SemanticSnapshotDigest(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := SemanticSnapshotDigest(reordered)
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest == secondDigest {
		t.Fatal("semantic digest ignored configured backend order")
	}
}

func TestRequestFingerprintIsStableForEquivalentMaps(t *testing.T) {
	t.Parallel()
	first, err := RequestFingerprint(map[string]any{"enabled": true, "name": "edge", "ports": []int{443, 80}})
	if err != nil {
		t.Fatalf("RequestFingerprint(first) error = %v", err)
	}
	second, err := RequestFingerprint(map[string]any{"ports": []int{443, 80}, "name": "edge", "enabled": true})
	if err != nil {
		t.Fatalf("RequestFingerprint(second) error = %v", err)
	}
	if first != second {
		t.Fatalf("fingerprints differ: %s != %s", first, second)
	}
}

func TestSnapshotPackageRespectsTargetEligibility(t *testing.T) {
	packageInfo := &storage.VersionPackage{
		URL: "/downloads/nre-agent", SHA256: "digest", Platform: "linux-amd64",
	}
	tests := []struct {
		name        string
		target      Target
		wantPackage bool
	}{
		{
			name:        "eligible Linux agent",
			target:      Target{Platform: "linux-amd64", Capabilities: []string{"package_manifest_v1"}},
			wantPackage: true,
		},
		{
			name:   "unsupported platform",
			target: Target{Platform: "darwin-arm64", Capabilities: []string{"package_manifest_v1"}},
		},
		{
			name:   "missing manifest capability",
			target: Target{Platform: "linux-arm64", Capabilities: []string{"http_rules"}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := snapshotForTargetPackageEligibility(storage.Snapshot{VersionPackage: packageInfo}, test.target)
			if got := snapshot.VersionPackage != nil; got != test.wantPackage {
				t.Fatalf("package retained = %v, want %v", got, test.wantPackage)
			}
		})
	}
}
