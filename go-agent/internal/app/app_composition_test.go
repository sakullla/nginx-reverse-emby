package app

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestNewComposesModulesWithoutBusinessRuntimeFields(t *testing.T) {
	app, err := New(Config{
		AgentID:        "agent",
		AgentName:      "agent",
		MasterURL:      "https://master.example.com",
		AgentToken:     "token",
		CurrentVersion: "0.1.0",
		DataDir:        t.TempDir(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = app.Close() })

	wantModules := map[string]bool{
		"certs":       false,
		"egress":      false,
		"relay":       false,
		"http":        false,
		"l4":          false,
		"diagnostics": false,
		"traffic":     false,
		"wireguard":   false,
	}
	for _, name := range app.ModuleNames() {
		if _, ok := wantModules[name]; ok {
			wantModules[name] = true
		}
	}
	for name, found := range wantModules {
		if !found {
			t.Fatalf("ModuleNames() missing %q from %v", name, app.ModuleNames())
		}
	}

	appType := reflect.TypeOf(App{})
	for _, fieldName := range []string{
		"certApplier",
		"httpApplier",
		"l4Applier",
		"relayApplier",
		"certModule",
		"egressModule",
		"httpModule",
		"l4Module",
		"relayModule",
		"trafficModule",
		"wireGuardRuntime",
	} {
		if _, ok := appType.FieldByName(fieldName); ok {
			t.Fatalf("App still owns business runtime field %q", fieldName)
		}
	}
}

func TestAppPackageDoesNotCreateBusinessRuntimesDirectly(t *testing.T) {
	forbidden := []string{
		"newHTTPRuntimeManager",
		"newL4RuntimeManager",
		"newRelayRuntimeManager",
		"newSharedWireGuardRuntime",
		"modulecerts.NewManager",
		"certs.NewManager",
		"modulewireguard.NewRuntime",
		"hosttraffic.NewCollector",
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir(.) error = %v", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		source, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", name, err)
		}
		for _, forbiddenText := range forbidden {
			if strings.Contains(string(source), forbiddenText) {
				t.Fatalf("%s contains forbidden business runtime constructor %q", name, forbiddenText)
			}
		}
	}
}
