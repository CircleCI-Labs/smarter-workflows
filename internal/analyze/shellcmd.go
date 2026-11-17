package analyze

import "mvdan.cc/sh/v3/syntax"

// commandVarUnset is true when the command word is only an unset positional or env reference (e.g. "$bin").
func commandVarUnset(w *syntax.Word, sw *shellWalk) bool {
	if w == nil || len(w.Parts) != 1 {
		return false
	}
	pe, ok := w.Parts[0].(*syntax.ParamExp)
	if !ok || pe.Param == nil || pe.Length || pe.Exp != nil {
		return false
	}
	_, ok = sw.paramValue(pe.Param.Value)
	return !ok
}