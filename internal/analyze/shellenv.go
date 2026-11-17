package analyze

import (
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

func (sw *shellWalk) initEnv() {
	if sw.env == nil {
		sw.env = map[string]string{}
	}
}

func (sw *shellWalk) captureAssigns(assigns []*syntax.Assign, wds wdSet) {
	sw.initEnv()
	for _, a := range assigns {
		if a == nil || a.Naked || a.Name == nil || a.Value == nil {
			continue
		}
		name := a.Name.Value
		if v, ok := sw.evalAssignValue(a.Value, wds); ok {
			sw.env[name] = v
		} else {
			delete(sw.env, name)
		}
	}
}

func (sw *shellWalk) evalAssignValue(w *syntax.Word, wds wdSet) (string, bool) {
	s, ok := sw.expandWord(w, wds)
	if !ok || s == "" {
		return "", false
	}
	return s, true
}

func (sw *shellWalk) expandWord(w *syntax.Word, wds wdSet) (string, bool) {
	if w == nil {
		return "", false
	}
	var b strings.Builder
	dyn := false
	for _, p := range w.Parts {
		switch x := p.(type) {
		case *syntax.Lit:
			b.WriteString(x.Value)
		case *syntax.SglQuoted:
			b.WriteString(x.Value)
		case *syntax.DblQuoted:
			for _, dp := range x.Parts {
				switch d := dp.(type) {
				case *syntax.Lit:
					b.WriteString(d.Value)
				case *syntax.SglQuoted:
					b.WriteString(d.Value)
				case *syntax.ParamExp:
					if v, ok := sw.expandParam(d, wds); ok {
						b.WriteString(v)
					} else {
						dyn = true
					}
				default:
					dyn = true
				}
			}
		case *syntax.ParamExp:
			if v, ok := sw.expandParam(x, wds); ok {
				b.WriteString(v)
			} else {
				dyn = true
			}
		case *syntax.CmdSubst:
			if v, ok := sw.evalCmdSubst(x, wds); ok {
				b.WriteString(v)
			} else {
				dyn = true
			}
		default:
			dyn = true
		}
	}
	if dyn {
		return "", false
	}
	s := filepath.ToSlash(strings.TrimSpace(b.String()))
	if s == "" {
		return "", false
	}
	return s, true
}

func (sw *shellWalk) scriptDir(wds wdSet) string {
	if sw.scriptRel != "" {
		d := filepath.Dir(filepath.FromSlash(sw.scriptRel))
		if d == "." {
			return pickWD(wds)
		}
		return filepath.ToSlash(d)
	}
	return pickWD(wds)
}

func (sw *shellWalk) evalCmdSubst(cs *syntax.CmdSubst, wds wdSet) (string, bool) {
	if cs == nil || len(cs.Stmts) == 0 {
		return "", false
	}
	if sw.scriptRel != "" && len(cs.Stmts) == 1 {
		if bc, ok := cs.Stmts[0].Cmd.(*syntax.BinaryCmd); ok && bc.Op == syntax.AndStmt {
			xcd, xok := bc.X.Cmd.(*syntax.CallExpr)
			ypwd, yok := bc.Y.Cmd.(*syntax.CallExpr)
			if xok && yok && litWord(xcd.Args[0]) == "cd" && litWord(ypwd.Args[0]) == "pwd" {
				if target, ok := sw.expandWord(xcd.Args[1], wds); ok {
					rel := resolveWD(sw.repoRoot, pickWD(wds), target)
					return rel, true
				}
				return ".", true
			}
		}
	}
	if len(cs.Stmts) == 1 {
		if c, ok := cs.Stmts[0].Cmd.(*syntax.CallExpr); ok && len(c.Args) > 0 {
			if litWord(c.Args[0]) == "git" && len(c.Args) >= 2 {
				sub := litWord(c.Args[1])
				if sub == "rev-parse" && len(c.Args) >= 3 && litWord(c.Args[2]) == "--show-toplevel" {
					return ".", true
				}
			}
			if litWord(c.Args[0]) == "dirname" && len(c.Args) >= 2 {
				w0 := litWord(c.Args[1])
				if w0 == `"$0"` || w0 == "$0" {
					return sw.scriptDir(wds), true
				}
				if sw.scriptRel != "" {
					if arg, ok := sw.expandWord(c.Args[1], wds); ok {
						return filepath.ToSlash(filepath.Dir(arg)), true
					}
				}
			}
		}
	}
	if st := cs.Stmts[0]; st.Cmd != nil {
		if bc, ok := st.Cmd.(*syntax.BinaryCmd); ok && bc.Op == syntax.AndStmt {
			if cd, ok := bc.X.Cmd.(*syntax.CallExpr); ok && litWord(cd.Args[0]) == "cd" && len(cd.Args) >= 2 {
				target, ok := sw.expandWord(cd.Args[1], wds)
				if !ok {
					return "", false
				}
				if y, ok := bc.Y.Cmd.(*syntax.CallExpr); ok && litWord(y.Args[0]) == "pwd" {
					rel := resolveWD(sw.repoRoot, pickWD(wds), target)
					return rel, true
				}
			}
		}
	}
	return "", false
}

func (sw *shellWalk) resolveExecutable(w *syntax.Word, wd string) (string, bool) {
	s, ok := sw.expandWord(w, wdSet{wd: {}})
	if !ok {
		return "", false
	}
	s = normalizePathToken(s)
	if s == "" || strings.Contains(s, "$") {
		return "", false
	}
	rel := resolveWD(sw.repoRoot, wd, s)
	if fileExists(sw.repoRoot, rel) {
		return filepath.ToSlash(rel), true
	}
	return "", false
}