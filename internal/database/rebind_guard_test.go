package database

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// TestNoUnboundPlaceholders guards the dialect-portability seam: every
// placeholder-bearing query a repository issues must route through Rebind, so a
// second backend (Postgres, demand-gated — see docs/architecture.md)
// stays a one-function change in dbinterface.Rebind rather than a sweep of every
// call site.
//
// It parses this package's non-test source and flags any
// ExecContext/QueryContext/QueryRowContext call whose SQL argument is an unbound
// '?'-bearing query — either an inline string literal, or a bare const/var
// identifier whose value contains '?'. Both must instead be wrapped as
// q.Rebind(...). A Rebind(...) call (an *ast.CallExpr) is the wrapped, correct
// form; a migration body via string(body) is also a CallExpr and carries no
// placeholders. Limitation: idents are matched by name, not full scope resolution,
// and SQL assembled dynamically (fmt.Sprintf/concat) is not inspected — neither
// pattern exists in this package, which is the point of the guard.
func TestNoUnboundPlaceholders(t *testing.T) {
	t.Parallel()
	queryMethods := map[string]bool{"ExecContext": true, "QueryContext": true, "QueryRowContext": true}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files = append(files, file)
	}

	// Pass 1: collect const/var names whose string value contains a '?' placeholder.
	sqlIdents := map[string]bool{}
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			spec, ok := n.(*ast.ValueSpec)
			if !ok {
				return true
			}
			for i, name := range spec.Names {
				if i >= len(spec.Values) {
					continue
				}
				if lit, ok := spec.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING && strings.Contains(lit.Value, "?") {
					sqlIdents[name.Name] = true
				}
			}
			return true
		})
	}

	// Pass 2: flag query calls whose SQL argument is an unbound '?' literal or a
	// bare '?'-bearing identifier (a Rebind(...) wrapper is a CallExpr, not flagged).
	var violations []string
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !queryMethods[sel.Sel.Name] || len(call.Args) < 2 {
				return true
			}
			switch arg := call.Args[1].(type) {
			case *ast.BasicLit:
				if arg.Kind == token.STRING && strings.Contains(arg.Value, "?") {
					violations = append(violations, fset.Position(arg.Pos()).String())
				}
			case *ast.Ident:
				if sqlIdents[arg.Name] {
					violations = append(violations, fset.Position(arg.Pos()).String())
				}
			}
			return true
		})
	}
	if len(violations) > 0 {
		t.Errorf("SQL with `?` placeholders must be wrapped in q.Rebind(...) (dialect seam); unbound at:\n  %s",
			strings.Join(violations, "\n  "))
	}
}
