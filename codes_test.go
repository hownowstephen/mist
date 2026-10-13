package mist

import (
	"flag"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

var updateCodes = flag.Bool("update-codes", false, "rewrite testdata/codes.txt")

// Error.Code is part of the API: testdata/codes.txt lists every code, so renaming or
// dropping one shows up in review. Run go test -run TestCodes -update-codes after adding one.
func TestCodes(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	set := map[string]bool{}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				if id, ok := n.Fun.(*ast.Ident); ok && id.Name == "bail" {
					set[codeOf(t, n.Args[1])] = true
				}
			case *ast.KeyValueExpr:
				if id, ok := n.Key.(*ast.Ident); ok && id.Name == "Code" {
					set[codeOf(t, n.Value)] = true
				}
			}
			return true
		})
	}
	var got []string
	for c := range set {
		got = append(got, c)
	}
	slices.Sort(got)
	text := strings.Join(got, "\n") + "\n"
	if *updateCodes {
		if err := os.WriteFile("testdata/codes.txt", []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, _ := os.ReadFile("testdata/codes.txt")
	if string(want) != text {
		t.Errorf("Error codes changed; review, then run go test -run TestCodes -update-codes.\ngot:\n%s", text)
	}
}

// codeOf renders a code expression, with identifiers as placeholders: "tag:"+name is tag:NAME.
func codeOf(t *testing.T, e ast.Expr) string {
	switch e := e.(type) {
	case *ast.BasicLit:
		s, err := strconv.Unquote(e.Value)
		if err != nil {
			t.Fatal(err)
		}
		return s
	case *ast.BinaryExpr:
		return codeOf(t, e.X) + codeOf(t, e.Y)
	case *ast.Ident:
		return strings.ToUpper(e.Name)
	}
	t.Fatalf("code %T isn't a literal or a concatenation", e)
	return ""
}
