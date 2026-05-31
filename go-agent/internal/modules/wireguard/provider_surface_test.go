package wireguard_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeDoesNotExportRelayProviderSurface(t *testing.T) {
	t.Parallel()

	forbidden := map[string]struct{}{
		"Provider":                    {},
		"ProviderForAgent":            {},
		"TransactionProvider":         {},
		"TransactionProviderForAgent": {},
		"NewRuntimeProvider":          {},
		"NewTransactionProvider":      {},
	}
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(name), nil, 0)
		if err != nil {
			t.Fatalf("ParseFile(%s) error = %v", name, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if _, ok := forbidden[fn.Name.Name]; !ok {
				continue
			}
			if fn.Recv == nil {
				t.Fatalf("%s exports forbidden old provider constructor %s", name, fn.Name.Name)
			}
			if len(fn.Recv.List) == 0 {
				continue
			}
			if star, ok := fn.Recv.List[0].Type.(*ast.StarExpr); ok {
				if ident, ok := star.X.(*ast.Ident); ok && ident.Name == "Runtime" {
					t.Fatalf("%s exports forbidden old provider method Runtime.%s", name, fn.Name.Name)
				}
			}
		}
	}
}
