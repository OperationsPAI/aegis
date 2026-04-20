package module_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestModulePackagesAvoidForeignRepositoryConstructors(t *testing.T) {
	moduleRoot := "."
	fileset := token.NewFileSet()

	err := filepath.WalkDir(moduleRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		rel, err := filepath.Rel(moduleRoot, path)
		if err != nil {
			return err
		}
		parts := strings.Split(rel, string(filepath.Separator))
		if len(parts) < 2 {
			return nil
		}
		ownerModule := parts[0]

		file, err := parser.ParseFile(fileset, path, nil, 0)
		if err != nil {
			return err
		}

		aliases := map[string]string{}
		for _, imp := range file.Imports {
			importPath := strings.Trim(imp.Path.Value, "\"")
			if !strings.HasPrefix(importPath, "aegis/module/") {
				continue
			}
			depModule := strings.TrimPrefix(importPath, "aegis/module/")
			if depModule == ownerModule {
				continue
			}
			alias := depModule
			if imp.Name != nil {
				alias = imp.Name.Name
			}
			aliases[alias] = depModule
		}

		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			depModule, ok := aliases[pkg.Name]
			if !ok {
				return true
			}
			if sel.Sel.Name == "NewRepository" || sel.Sel.Name == "Repository" {
				t.Errorf("%s reaches into %s repository via %s.%s; use %s.Reader/%s.Writer instead", path, depModule, pkg.Name, sel.Sel.Name, depModule, depModule)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("scan module packages: %v", err)
	}
}
