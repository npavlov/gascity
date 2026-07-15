package mailbox

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestReadOnlyGeneratedBoundaryHasExactlyFourGETs(t *testing.T) {
	typeOf := reflect.TypeOf((*supervisorMailReader)(nil)).Elem()
	got := make([]string, 0, typeOf.NumMethod())
	for index := 0; index < typeOf.NumMethod(); index++ {
		got = append(got, typeOf.Method(index).Name)
	}
	sort.Strings(got)
	want := []string{
		"GetV0CityByCityNameMailByIdWithResponse",
		"GetV0CityByCityNameMailCountWithResponse",
		"GetV0CityByCityNameMailThreadByIdWithResponse",
		"GetV0CityByCityNameMailWithResponse",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("generated boundary methods = %v, want %v", got, want)
	}
}

func TestReadOnlyProductionASTContainsNoMailMutationCall(t *testing.T) {
	paths := []string{"*.go", "../api/mail.go"}
	for _, pattern := range paths {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range matches {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			ast.Inspect(file, func(node ast.Node) bool {
				selector, ok := node.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				name := selector.Sel.Name
				if !strings.Contains(name, "Mail") {
					return true
				}
				for _, mutation := range []string{"Send", "Post", "Delete", "Archive", "MarkUnread", "Read", "Reply"} {
					if strings.Contains(name, mutation) {
						t.Errorf("%s contains forbidden mail mutation selector %s", path, name)
					}
				}
				return true
			})
		}
	}
}
