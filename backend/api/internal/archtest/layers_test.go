// Package archtest enforces the dependency rules between the groups under
// internal/ (see internal/README.md). It only contains tests.
package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const modulePath = "github.com/Gigman2/sentinel/backend/api/"

// group returns the layout group of a module-relative package directory,
// e.g. "internal/platform/httpx" → "platform", "cmd/sentinel-api" → "cmd".
func group(dir string) string {
	parts := strings.Split(dir, "/")
	if parts[0] != "internal" || len(parts) < 2 {
		return parts[0]
	}
	return parts[1]
}

// allowed lists which groups each group's production code may import.
// A group may always import itself. Test files are exempt.
var allowed = map[string][]string{
	"platform": {"migrations"}, // database embeds the SQL files
	"domain":   {},
	"auth":     {"platform", "domain"},
	"modules":  {"platform", "domain", "auth"},
	"server":   {"platform", "domain", "auth", "modules"},
	"support":  {"platform", "domain", "auth", "modules", "server"},
	"cmd":      {"platform", "domain", "auth", "modules", "server", "support"},
	// migrations embeds SQL only.
	"migrations": {},
}

func TestInternalLayering(t *testing.T) {
	root, err := filepath.Abs("../..") // backend/api
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	checked := 0

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, filepath.Dir(path))
		rel = filepath.ToSlash(rel)
		from := group(rel)
		rules, known := allowed[from]
		if !known {
			t.Errorf("%s: package is outside the known layout groups; add it to internal/README.md and this test", rel)
			return nil
		}

		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		checked++
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			target, ok := strings.CutPrefix(p, modulePath)
			if !ok {
				continue
			}
			to := group(target)
			if to == from || contains(rules, to) {
				continue
			}
			file, _ := filepath.Rel(root, path)
			t.Errorf("%s imports %s: %q may not depend on %q", file, target, from, to)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked == 0 {
		t.Fatal("no Go files checked; is the test running from internal/archtest?")
	}
}

// sqlStatement matches string literals that are SQL statements.
var sqlStatement = regexp.MustCompile(`(?is)^\s*(SELECT\s|INSERT\s+INTO\s|UPDATE\s+\S+\s+SET\s|DELETE\s+FROM\s|WITH\s+\S+\s+AS\s*\()`)

// groupsWithStores are the groups whose SQL must live in store files.
// platform and support are exempt: they are infrastructure and tooling.
var groupsWithStores = map[string]bool{"auth": true, "modules": true, "server": true}

func isStoreFile(name string) bool {
	return name == "store.go" || (strings.HasPrefix(name, "store_") && strings.HasSuffix(name, ".go"))
}

// TestSQLOnlyInStores keeps queries out of handlers and services: in auth,
// modules and server, SQL statements may appear only in store.go or store_*.go.
func TestSQLOnlyInStores(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	err = filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || isStoreFile(d.Name()) {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if !groupsWithStores[group(filepath.ToSlash(filepath.Dir(rel)))] {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, err := strconv.Unquote(lit.Value)
			if err == nil && sqlStatement.MatchString(s) {
				t.Errorf("%s:%d: SQL statement outside a store file; move it to store.go", rel, fset.Position(lit.Pos()).Line)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
