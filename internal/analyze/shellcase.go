package analyze

import "mvdan.cc/sh/v3/syntax"

func (sw *shellWalk) caseClause(c *syntax.CaseClause, wds wdSet) wdSet {
	if c == nil {
		return wds
	}
	word, ok := sw.expandCaseWord(c.Word, wds)
	if !ok {
		out := wds.copy()
		for _, item := range c.Items {
			out.merge(sw.stmts(item.Stmts, wds.copy()))
		}
		return out
	}
	for _, item := range c.Items {
		if caseItemMatches(item, word) {
			return sw.stmts(item.Stmts, wds.copy())
		}
	}
	return wds
}

func (sw *shellWalk) expandCaseWord(w *syntax.Word, wds wdSet) (string, bool) {
	if w == nil {
		return "", false
	}
	if s, ok := sw.expandWord(w, wds); ok {
		return s, true
	}
	if onlyEmptyDefaultParam(w) {
		return "", true
	}
	return "", false
}

func caseItemMatches(item *syntax.CaseItem, word string) bool {
	if item == nil {
		return false
	}
	for _, p := range item.Patterns {
		pat := casePatternLit(p)
		switch pat {
		case "*":
			return true
		case "|":
			continue
		default:
			if pat == word {
				return true
			}
		}
	}
	return false
}

func onlyEmptyDefaultParam(w *syntax.Word) bool {
	if w == nil || len(w.Parts) != 1 {
		return false
	}
	pe, ok := w.Parts[0].(*syntax.ParamExp)
	if !ok || pe.Param == nil || pe.Exp == nil {
		return false
	}
	if pe.Exp.Op != syntax.DefaultUnsetOrNull && pe.Exp.Op != syntax.DefaultUnset {
		return false
	}
	return pe.Exp.Word == nil || litWord(pe.Exp.Word) == ""
}

func casePatternLit(w *syntax.Word) string {
	if w == nil {
		return ""
	}
	if s := w.Lit(); s != "" {
		return s
	}
	return litWord(w)
}
