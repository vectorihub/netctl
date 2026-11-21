package tests

import (
	"bytes"
	"flag"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// updateSurface rewrites docs/api-surface.txt instead of comparing with it.
var updateSurface = flag.Bool("update-surface", false, "rewrite docs/api-surface.txt")

// surfacePath is the recorded exported surface, relative to this test's directory.
const surfacePath = "../../docs/api-surface.txt"

// text prints a piece of syntax on one line, so a signature or a field type reads the same
// in the record whatever the source layout.
func text(fset *token.FileSet, node any) string {
	var b bytes.Buffer
	if err := printer.Fprint(&b, fset, node); err != nil {
		return "?"
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// exportedReceiver reports whether a method's receiver is an exported type, and names it.
func exportedReceiver(fset *token.FileSet, recv *ast.FieldList) (string, bool) {
	if recv == nil || len(recv.List) == 0 {
		return "", false
	}
	name := strings.TrimPrefix(text(fset, recv.List[0].Type), "*")
	if i := strings.Index(name, "["); i >= 0 {
		name = name[:i]
	}
	return name, ast.IsExported(name)
}

// packageSurface returns one line for each exported identifier of the package in dir:
// constants, variables, types (with their exported fields and interface methods), functions
// and methods, each with its type or signature.
func packageSurface(t *testing.T, dir, importPath string) []string {
	t.Helper()
	fset := token.NewFileSet()
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if recv, ok := exportedReceiver(fset, d.Recv); ok && d.Name.IsExported() {
					lines = append(lines, importPath+": method "+recv+"."+d.Name.Name+strings.TrimPrefix(text(fset, d.Type), "func"))
				} else if d.Recv == nil && d.Name.IsExported() {
					lines = append(lines, importPath+": func "+d.Name.Name+strings.TrimPrefix(text(fset, d.Type), "func"))
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					lines = append(lines, specSurface(fset, importPath, d.Tok, spec)...)
				}
			}
		}
	}
	return lines
}

// specSurface returns the lines for one const, var or type declaration.
func specSurface(fset *token.FileSet, importPath string, tok token.Token, spec ast.Spec) []string {
	var lines []string
	switch s := spec.(type) {
	case *ast.ValueSpec:
		for _, name := range s.Names {
			if name.IsExported() {
				kind := ""
				if s.Type != nil {
					kind = " " + text(fset, s.Type)
				}
				lines = append(lines, importPath+": "+tok.String()+" "+name.Name+kind)
			}
		}
	case *ast.TypeSpec:
		if !s.Name.IsExported() {
			return nil
		}
		switch ty := s.Type.(type) {
		case *ast.StructType:
			lines = append(lines, importPath+": type "+s.Name.Name+" struct")
			for _, f := range ty.Fields.List {
				if len(f.Names) == 0 && embeddedExported(fset, f.Type) {
					lines = append(lines, importPath+": embed "+s.Name.Name+" "+text(fset, f.Type))
				}
				for _, name := range f.Names {
					if name.IsExported() {
						lines = append(lines, importPath+": field "+s.Name.Name+"."+name.Name+" "+text(fset, f.Type))
					}
				}
			}
		case *ast.InterfaceType:
			lines = append(lines, importPath+": type "+s.Name.Name+" interface")
			for _, m := range ty.Methods.List {
				if len(m.Names) == 0 {
					lines = append(lines, importPath+": embed "+s.Name.Name+" "+text(fset, m.Type))
				}
				for _, name := range m.Names {
					if name.IsExported() {
						lines = append(lines, importPath+": imethod "+s.Name.Name+"."+name.Name+strings.TrimPrefix(text(fset, m.Type), "func"))
					}
				}
			}
		default:
			sep := " "
			if s.Assign.IsValid() {
				sep = " = "
			}
			lines = append(lines, importPath+": type "+s.Name.Name+sep+text(fset, s.Type))
		}
	}
	return lines
}

// embeddedExported reports whether an embedded field's type is exported: its name, without
// a pointer star or a package qualifier, starts with a capital. An embedded exported type
// promotes its fields and methods, so it is part of the surface.
func embeddedExported(fset *token.FileSet, expr ast.Expr) bool {
	name := strings.TrimPrefix(text(fset, expr), "*")
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	if i := strings.Index(name, "["); i >= 0 {
		name = name[:i]
	}
	return ast.IsExported(name)
}

// moduleSurface walks the module and returns the sorted surface of every public package:
// every directory with Go files except tests, internal, testdata, features and arch.
func moduleSurface(t *testing.T) string {
	t.Helper()
	root := "../.."
	var lines []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return err
		}
		switch d.Name() {
		case "tests", "internal", "testdata", "features", "arch", ".git", ".apidiff-base":
			return filepath.SkipDir
		}
		matches, _ := filepath.Glob(filepath.Join(path, "*.go"))
		if len(matches) == 0 {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		importPath := "github.com/vectorihub/netctl"
		if rel != "." {
			importPath += "/" + filepath.ToSlash(rel)
		}
		lines = append(lines, packageSurface(t, path, importPath)...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	return "# The exported surface of netctl. Generated by arch/tests (go test ./arch/tests -update-surface);\n" +
		"# review the diff of this file on every tag. See docs/versioning.md for what a change means.\n" +
		strings.Join(lines, "\n") + "\n"
}

// TestTheAPISurfaceIsRecorded is the record of the public API (V76): docs/api-surface.txt is
// the exported surface of every public package, and a change to it must be made on purpose,
// by regenerating the file and reviewing the diff, never by accident.
func TestTheAPISurfaceIsRecorded(t *testing.T) {
	got := moduleSurface(t)
	if *updateSurface {
		if err := os.WriteFile(surfacePath, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(surfacePath)
	if err != nil {
		t.Fatalf("%v; create it with: go test ./arch/tests -update-surface", err)
	}
	if got != string(want) {
		t.Errorf("the exported API differs from docs/api-surface.txt; if the change is meant, run: go test ./arch/tests -update-surface and review the diff")
	}
}

// TestTheSurfaceHoldsTheFacade checks the record is not empty and includes the entry point,
// so a walker that silently found nothing cannot pass.
func TestTheSurfaceHoldsTheFacade(t *testing.T) {
	got := moduleSurface(t)
	for _, want := range []string{
		"github.com/vectorihub/netctl: func Plan(in Inputs) (Result, error)",
		"github.com/vectorihub/netctl/plan: method Result.Render() (map[string][]byte, error)",
		"github.com/vectorihub/netctl/types: type Clock interface",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the surface lacks %q", want)
		}
	}
}
