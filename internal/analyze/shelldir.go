package analyze

import "mvdan.cc/sh/v3/syntax"

func dirnameArgIsZero(w *syntax.Word) bool {
	if w == nil {
		return false
	}
	for _, p := range w.Parts {
		switch x := p.(type) {
		case *syntax.DblQuoted:
			for _, dp := range x.Parts {
				if pe, ok := dp.(*syntax.ParamExp); ok && pe.Param != nil && (pe.Param.Value == "0" || pe.Param.Value == "BASH_SOURCE") {
					return true
				}
			}
		case *syntax.ParamExp:
			if x.Param != nil && (x.Param.Value == "0" || x.Param.Value == "BASH_SOURCE") {
				return true
			}
		case *syntax.CmdSubst:
			if len(x.Stmts) == 1 {
				if ce, ok := x.Stmts[0].Cmd.(*syntax.CallExpr); ok && litWord(ce.Args[0]) == "dirname" && len(ce.Args) >= 2 {
					w0 := litWord(ce.Args[1])
					if w0 == "$0" || w0 == `"$0"` {
						return true
					}
				}
			}
		}
	}
	return false
}
