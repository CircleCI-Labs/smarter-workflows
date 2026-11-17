package analyze

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func (e *Engine) scanGoPathLiteralsInDirs(repoRoot string, dirs []string, into *InputSet) {
	modRoot := repoRoot
	if st, err := statRepo(repoRoot, "go.mod"); err == nil && !st.IsDir() {
		modRoot = repoRoot
	}
	seen := map[string]struct{}{}
	for _, d := range dirs {
		d = filepath.ToSlash(d)
		if d == "" || d == "." {
			d = "."
		}
		if _, ok := seen[d]; ok {
			continue
		}
		seen[d] = struct{}{}
		abs := filepath.Join(repoRoot, filepath.FromSlash(d))
		entries, err := os.ReadDir(abs)
		if err != nil {
			continue
		}
		for _, ent := range entries {
			if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".go") {
				continue
			}
			path := filepath.Join(abs, ent.Name())
			e.scanGoFilePathLiterals(repoRoot, modRoot, path, into)
		}
	}
}

func (e *Engine) scanGoFilePathLiterals(repoRoot, modRoot, absPath string, into *InputSet) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, absPath, nil, 0)
	if err != nil {
		return
	}
	fileDir, _ := filepath.Rel(repoRoot, filepath.Dir(absPath))
	fileDir = filepath.ToSlash(fileDir)
	modRel, _ := filepath.Rel(repoRoot, modRoot)
	modRel = filepath.ToSlash(modRel)
	consts := map[string]string{}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if len(vs.Values) <= i {
					break
				}
				if s, ok := stringExpr(vs.Values[i], consts); ok {
					consts[name.Name] = s
				}
			}
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if join, ok := joinStrings(call, consts); ok {
			e.tryAddResolvedGoPath(repoRoot, fileDir, modRel, join, into)
			return true
		}
		if lit, ok := pathIOCallArg(call, consts); ok {
			e.tryAddResolvedGoPath(repoRoot, fileDir, modRel, lit, into)
		}
		return true
	})
}

func strconvUnquote(s string) (string, error) {
	return strconv.Unquote(s)
}

func stringExpr(e ast.Expr, consts map[string]string) (string, bool) {
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind != token.STRING {
			return "", false
		}
		s, err := strconvUnquote(x.Value)
		return s, err == nil
	case *ast.Ident:
		s, ok := consts[x.Name]
		return s, ok
	default:
		return "", false
	}
}

// literalLooksLikePath reports whether a bare string literal (not already inside a
// path IO call) should be treated as a filesystem path.
func literalLooksLikePath(p string) bool {
	if strings.Contains(p, "/") {
		return true
	}
	if i := strings.LastIndex(p, "."); i > 0 {
		return true
	}
	return false
}

func joinStrings(call *ast.CallExpr, consts map[string]string) (string, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || (pkg.Name != "filepath" && pkg.Name != "path") || sel.Sel.Name != "Join" {
		return "", false
	}
	var parts []string
	for _, arg := range call.Args {
		s, ok := stringExpr(arg, consts)
		if !ok {
			return "", false
		}
		parts = append(parts, s)
	}
	return filepath.ToSlash(filepath.Join(parts...)), true
}

var goPathIOFns = map[string][]string{
	"os":     {"ReadFile", "Open", "OpenFile", "ReadDir", "Stat", "Lstat"},
	"ioutil": {"ReadFile"},
	"fs":     {"ReadFile", "ReadDir"},
}

func pathIOCallArg(call *ast.CallExpr, consts map[string]string) (string, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	names, ok := goPathIOFns[pkg.Name]
	if !ok {
		return "", false
	}
	found := false
	for _, n := range names {
		if sel.Sel.Name == n {
			found = true
			break
		}
	}
	if !found || len(call.Args) == 0 {
		return "", false
	}
	arg0 := call.Args[0]
	if sub, ok := arg0.(*ast.CallExpr); ok {
		if join, ok := joinStrings(sub, consts); ok {
			return join, true
		}
	}
	return stringExpr(arg0, consts)
}

func (e *Engine) tryAddResolvedGoPath(repoRoot, fileDir, modRel, p string, into *InputSet) {
	tryAddResolvedLiteralPath(repoRoot, fileDir, modRel, p, into, InputNote{Signal: SignalGoImports, Detail: "literal"})
}
