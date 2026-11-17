package goselfupdate

import (
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const modulePath = "github.com/datapointchris/goselfupdate"

// requestDoor is the one function that builds and sends an HTTP request, so
// every request carries the credential, the API version header and the
// response size bound.
const requestDoor = "(*GitHubSource).fetch"

// TestEveryRequestGoesThroughFetch type-checks every non-test file in the
// module and fails on any HTTP request built or sent outside fetch.
// Identifiers are judged by the object they resolve to, so an aliased import
// or a method value is caught the same as a direct call.
//
// Files are selected by the running platform's build constraints, so the
// Windows-only install file is read on the Windows runner.
func TestEveryRequestGoesThroughFetch(t *testing.T) {
	root, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	scan, err := scanModuleForRequests(root)
	if err != nil {
		t.Fatal(err)
	}
	if scan.inDoor == 0 {
		t.Fatalf("found no request inside %s, reading %s — the walk read nothing it "+
			"could judge rather than finding everything compliant", requestDoor, root)
	}
	if len(scan.faults) > 0 {
		t.Fatalf("a request was made outside %s:\n  %s", requestDoor, strings.Join(scan.faults, "\n  "))
	}
}

// TestTheRequestDoorCheckFailsOnABypass proves the check can fail, which is
// what makes its passing evidence of anything.
func TestTheRequestDoorCheckFailsOnABypass(t *testing.T) {
	cases := map[string]string{
		"aliased http import": `package p
import web "net/http"
func f() { _, _ = web.Get("https://example.com") }`,
		"client sent outside fetch": `package p
import "net/http"
func f(c *http.Client, r *http.Request) { _, _ = c.Do(r) }`,
	}
	for name, source := range cases {
		t.Run(name, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "p.go", source, 0)
			if err != nil {
				t.Fatal(err)
			}
			info, _, err := checkPackage(fset, "p", []*ast.File{file}, importer.Default())
			if err != nil {
				t.Fatal(err)
			}
			var scan requestScan
			scan.read(fset, "", []*ast.File{file}, info)
			if len(scan.faults) == 0 {
				t.Error("the bypass passed the check")
			}
		})
	}
}

type requestScan struct {
	faults []string
	inDoor int
}

// scanModuleForRequests checks the root package first, so a package importing
// it resolves to the copy already checked.
func scanModuleForRequests(root string) (requestScan, error) {
	var scan requestScan
	var directories []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := entry.Name()
		if entry.IsDir() && path != root &&
			(strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "testdata") {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			directories = append(directories, path)
		}
		return nil
	})
	if err != nil {
		return scan, err
	}
	sort.Strings(directories)

	fset := token.NewFileSet()
	local := moduleImporter{standard: importer.Default(), local: map[string]*types.Package{}}
	for _, directory := range directories {
		pkg, err := build.Default.ImportDir(directory, 0)
		var noGo *build.NoGoError
		if errors.As(err, &noGo) {
			continue
		}
		if err != nil {
			return scan, err
		}
		var files []*ast.File
		for _, name := range pkg.GoFiles {
			file, err := parser.ParseFile(fset, filepath.Join(directory, name), nil, 0)
			if err != nil {
				return scan, err
			}
			files = append(files, file)
		}
		relative, err := filepath.Rel(root, directory)
		if err != nil {
			return scan, err
		}
		importPath := modulePath
		if relative != "." {
			importPath += "/" + filepath.ToSlash(relative)
		}
		info, checked, err := checkPackage(fset, importPath, files, local)
		if err != nil {
			return scan, err
		}
		local.local[importPath] = checked
		scan.read(fset, root, files, info)
	}
	return scan, nil
}

func checkPackage(fset *token.FileSet, path string, files []*ast.File, imp types.Importer) (*types.Info, *types.Package, error) {
	info := &types.Info{Uses: map[*ast.Ident]types.Object{}}
	pkg, err := (&types.Config{Importer: imp}).Check(path, fset, files, info)
	return info, pkg, err
}

type moduleImporter struct {
	standard types.Importer
	local    map[string]*types.Package
}

func (m moduleImporter) Import(path string) (*types.Package, error) {
	if pkg, ok := m.local[path]; ok {
		return pkg, nil
	}
	return m.standard.Import(path)
}

// read counts each request inside fetch and records each one outside it as a fault.
func (s *requestScan) read(fset *token.FileSet, root string, files []*ast.File, info *types.Info) {
	for _, file := range files {
		for _, declaration := range file.Decls {
			enclosing := ""
			if function, ok := declaration.(*ast.FuncDecl); ok {
				enclosing = functionName(function)
			}
			ast.Inspect(declaration, func(node ast.Node) bool {
				identifier, ok := node.(*ast.Ident)
				if !ok || !startsRequest(info.Uses[identifier]) {
					return true
				}
				if enclosing == requestDoor {
					s.inDoor++
					return true
				}
				position := fset.Position(identifier.Pos())
				if relative, err := filepath.Rel(root, position.Filename); err == nil && root != "" {
					position.Filename = relative
				}
				where := enclosing
				if where == "" {
					where = "a package-level declaration"
				}
				s.faults = append(s.faults, fmt.Sprintf("%s: %s in %s", position, identifier.Name, where))
				return true
			})
		}
	}
}

func functionName(function *ast.FuncDecl) string {
	if function.Recv == nil || len(function.Recv.List) == 0 {
		return function.Name.Name
	}
	switch receiver := function.Recv.List[0].Type.(type) {
	case *ast.StarExpr:
		if named, ok := receiver.X.(*ast.Ident); ok {
			return "(*" + named.Name + ")." + function.Name.Name
		}
	case *ast.Ident:
		return receiver.Name + "." + function.Name.Name
	}
	return function.Name.Name
}

// startsRequest reports whether an object builds or sends an HTTP request.
func startsRequest(object types.Object) bool {
	if object == nil || object.Pkg() == nil || object.Pkg().Path() != "net/http" {
		return false
	}
	function, ok := object.(*types.Func)
	if !ok {
		return false
	}
	name := object.Name()
	receiver := function.Type().(*types.Signature).Recv()
	if receiver == nil {
		switch name {
		case "NewRequest", "NewRequestWithContext", "Get", "Head", "Post", "PostForm":
			return true
		}
		return false
	}
	switch receiverTypeName(receiver.Type()) {
	case "Client":
		switch name {
		case "Do", "Get", "Head", "Post", "PostForm":
			return true
		}
	case "Transport", "RoundTripper":
		return name == "RoundTrip"
	}
	return false
}

func receiverTypeName(receiver types.Type) string {
	if pointer, ok := receiver.(*types.Pointer); ok {
		receiver = pointer.Elem()
	}
	if named, ok := receiver.(*types.Named); ok {
		return named.Obj().Name()
	}
	return ""
}
