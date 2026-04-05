package config

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDefaultUsesBootstrapAgentID(t *testing.T) {
	cfg := Default()

	if cfg.AgentID != "bootstrap" {
		t.Fatalf("expected bootstrap agent ID, got %q", cfg.AgentID)
	}
}

func TestConfigPackageDeclaresConcreteConfigType(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to resolve test file location")
	}

	configFile := filepath.Join(filepath.Dir(currentFile), "config.go")
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, configFile, nil, 0)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	for _, decl := range file.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.TYPE {
			continue
		}

		for _, spec := range genDecl.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok || typeSpec.Name.Name != "Config" {
				continue
			}

			if _, ok := typeSpec.Type.(*ast.StructType); ok {
				return
			}

			t.Fatalf("expected Config to be declared as a struct in config.go")
		}
	}

	t.Fatal("expected config.go to declare type Config")
}
