package wireguard_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
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
	file, err := parser.ParseFile(fset, filepath.Join("runtime.go"), nil, 0)
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
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
			t.Fatalf("runtime.go exports forbidden old provider constructor %s", fn.Name.Name)
		}
		if len(fn.Recv.List) == 0 {
			continue
		}
		if star, ok := fn.Recv.List[0].Type.(*ast.StarExpr); ok {
			if ident, ok := star.X.(*ast.Ident); ok && ident.Name == "Runtime" {
				t.Fatalf("runtime.go exports forbidden old provider method Runtime.%s", fn.Name.Name)
			}
		}
	}
}
