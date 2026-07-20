package service

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestBackupArchiveOmitsRetiredMembersAndFields(t *testing.T) {
	retiredPrefix := "wire" + "guard"
	bundle := BackupBundle{
		Manifest: BackupManifest{
			PackageVersion:     BackupPackageVersion,
			SourceArchitecture: BackupSourceArchitectureGo,
		},
		Agents: []BackupAgent{backupAgentFromRow(storage.AgentRow{
			ID:               "edge-a",
			Name:             "edge-a",
			AgentToken:       "token-edge-a",
			CapabilitiesJSON: marshalJSON([]string{"http_rules", retiredPrefix}, "[]"),
		})},
		HTTPRules: []BackupHTTPRule{{
			ID:          1,
			AgentID:     "edge-a",
			FrontendURL: "https://media.example.com",
			Backends:    []HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
			Enabled:     true,
		}},
		L4Rules: []BackupL4Rule{{
			ID:           2,
			AgentID:      "edge-a",
			Name:         "media",
			Protocol:     "tcp",
			ListenHost:   "0.0.0.0",
			ListenPort:   9000,
			UpstreamHost: "127.0.0.1",
			UpstreamPort: 9001,
			Enabled:      true,
		}},
		EgressProfiles: []BackupEgressProfile{{
			ID:      3,
			Name:    "direct",
			Type:    "direct",
			Enabled: true,
		}},
		RelayListeners: []BackupRelayListener{{
			ID:            4,
			AgentID:       "edge-a",
			Name:          "relay",
			ListenHost:    "0.0.0.0",
			BindHosts:     []string{"0.0.0.0"},
			ListenPort:    7443,
			PublicHost:    "relay.example.com",
			PublicPort:    7443,
			TransportMode: "tls_tcp",
		}},
	}

	archive, err := encodeBackupBundle(bundle)
	if err != nil {
		t.Fatalf("encodeBackupBundle() error = %v", err)
	}
	files := readBackupUnitArchive(t, archive)
	for _, name := range []string{
		retiredPrefix + "_profiles.json",
		retiredPrefix + "_clients.json",
	} {
		if _, exists := files[name]; exists {
			t.Fatalf("archive contains retired member %q", name)
		}
	}
	for name, content := range files {
		normalizedContent := bytes.ToLower(content)
		if bytes.Contains(normalizedContent, []byte(retiredPrefix)) || bytes.Contains(normalizedContent, []byte("w"+"g_")) {
			t.Fatalf("archive member %q contains retired content", name)
		}
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		var payload any
		if err := json.Unmarshal(content, &payload); err != nil {
			t.Fatalf("json.Unmarshal(%s) error = %v", name, err)
		}
		assertBackupUnitJSONKeysCurrent(t, payload, retiredPrefix)
	}
}

func TestBackupDecodeIgnoresUnknownLegacyMembersAndFields(t *testing.T) {
	retiredPrefix := "wire" + "guard"
	manifest := backupUnitJSON(t, BackupManifest{
		PackageVersion:     BackupPackageVersion,
		SourceArchitecture: BackupSourceArchitectureGo,
	})
	httpRules := backupUnitJSON(t, []map[string]any{{
		"id":                             11,
		"agent_id":                       "edge-a",
		"frontend_url":                   "https://media.example.com",
		"backends":                       []map[string]any{{"url": "http://127.0.0.1:8096"}},
		"enabled":                        true,
		retiredPrefix + "_entry_enabled": true,
		retiredPrefix + "_profile_id":    41,
	}})
	archive := writeBackupUnitArchive(t, []backupUnitArchiveFile{
		{name: backupManifestFile, content: manifest},
		{name: backupHTTPRulesFile, content: httpRules},
		{name: retiredPrefix + "_profiles.json", content: []byte("{not-json")},
	})

	bundle, err := decodeBackupBundle(archive)
	if err != nil {
		t.Fatalf("decodeBackupBundle() error = %v", err)
	}
	if len(bundle.HTTPRules) != 1 {
		t.Fatalf("decoded HTTP rules = %d, want 1", len(bundle.HTTPRules))
	}
	if got := bundle.HTTPRules[0].FrontendURL; got != "https://media.example.com" {
		t.Fatalf("decoded frontend URL = %q", got)
	}
	decoded := backupUnitJSON(t, bundle.HTTPRules[0])
	var payload any
	if err := json.Unmarshal(decoded, &payload); err != nil {
		t.Fatalf("json.Unmarshal(decoded rule) error = %v", err)
	}
	assertBackupUnitJSONKeysCurrent(t, payload, retiredPrefix)
}

func TestBackupFiltersUnsupportedResourceReferences(t *testing.T) {
	retiredMode := "wire" + "guard"
	if backupL4ListenModeSupported(retiredMode) {
		t.Fatalf("retired listen mode is supported")
	}
	if !backupL4ListenModeSupported("tcp") || !backupL4ListenModeSupported("proxy") {
		t.Fatalf("current listen modes are not supported")
	}
	if !backupRuleReferencesExcludedResource([][]int{{7}}, nil, map[int]struct{}{7: {}}, nil) {
		t.Fatalf("excluded relay reference was not detected")
	}
	egressID := 9
	if !backupRuleReferencesExcludedResource(nil, &egressID, nil, map[int]struct{}{9: {}}) {
		t.Fatalf("excluded egress reference was not detected")
	}
	if backupRuleReferencesExcludedResource([][]int{{1}}, &egressID, map[int]struct{}{}, map[int]struct{}{}) {
		t.Fatalf("current resource references were excluded")
	}
}

type backupUnitArchiveFile struct {
	name    string
	content []byte
}

func writeBackupUnitArchive(t *testing.T, files []backupUnitArchiveFile) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	tw := tar.NewWriter(gz)
	for _, file := range files {
		if err := writeBackupFile(tw, file.name, file.content); err != nil {
			t.Fatalf("writeBackupFile(%s) error = %v", file.name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar.Close() error = %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip.Close() error = %v", err)
	}
	return buffer.Bytes()
}

func readBackupUnitArchive(t *testing.T, archive []byte) map[string][]byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("gzip.NewReader() error = %v", err)
	}
	defer gz.Close()

	files := map[string][]byte{}
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("tar.Next() error = %v", err)
		}
		content, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("io.ReadAll(%s) error = %v", header.Name, err)
		}
		files[header.Name] = content
	}
	return files
}

func backupUnitJSON(t *testing.T, value any) []byte {
	t.Helper()
	content, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return content
}

func assertBackupUnitJSONKeysCurrent(t *testing.T, value any, retiredPrefix string) {
	t.Helper()
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			normalized := strings.ToLower(key)
			if strings.Contains(normalized, retiredPrefix) || strings.Contains(normalized, "w"+"g_") {
				t.Fatalf("JSON contains retired key %q", key)
			}
			assertBackupUnitJSONKeysCurrent(t, child, retiredPrefix)
		}
	case []any:
		for _, child := range typed {
			assertBackupUnitJSONKeysCurrent(t, child, retiredPrefix)
		}
	}
}
