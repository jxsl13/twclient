// Package doccov holds the documentation-coverage gate (V69): every exported
// identifier in the shipped library packages must carry a doc comment. It is a
// test-only package — it ships no API of its own.
package doccov

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// shippedPkgs are the library packages whose public surface must be fully
// documented (V69). cmd/ is the gitignored harness and is exempt.
var shippedPkgs = []string{
	"packet", "packer", "network", "net6", "net7", "physics", "client", "master",
}

// TestExportedSymbolsDocumented fails listing any exported func/type/const/var
// in a shipped package that lacks a doc comment (V69). A doc comment on the
// enclosing const/var block counts for its specs.
func TestExportedSymbolsDocumented(t *testing.T) {
	for _, pkg := range shippedPkgs {
		missing := undocumented(t, filepath.Join("..", pkg))
		if len(missing) > 0 {
			t.Errorf("%s: %d undocumented exported symbol(s): %s",
				pkg, len(missing), strings.Join(missing, ", "))
		}
	}
}

func undocumented(t *testing.T, dir string) []string {
	t.Helper()
	fset := token.NewFileSet()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatalf("glob %s: %v", dir, err)
	}
	var missing []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		for _, d := range af.Decls {
			switch decl := d.(type) {
			case *ast.FuncDecl:
				if decl.Name.IsExported() && decl.Doc == nil {
					recv := ""
					if decl.Recv != nil && len(decl.Recv.List) > 0 {
						recv = recvName(decl.Recv.List[0].Type) + "."
					}
					missing = append(missing, "func "+recv+decl.Name.Name)
				}
			case *ast.GenDecl:
				for _, s := range decl.Specs {
					switch sp := s.(type) {
					case *ast.TypeSpec:
						if sp.Name.IsExported() && sp.Doc == nil && decl.Doc == nil {
							missing = append(missing, "type "+sp.Name.Name)
						}
					case *ast.ValueSpec:
						if sp.Doc != nil || decl.Doc != nil {
							continue
						}
						for _, n := range sp.Names {
							if n.IsExported() {
								missing = append(missing, n.Name)
							}
						}
					}
				}
			}
		}
	}
	return missing
}

func recvName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return recvName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr: // generic receiver
		return recvName(t.X)
	}
	return "?"
}
