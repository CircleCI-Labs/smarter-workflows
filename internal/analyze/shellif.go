package analyze

import "mvdan.cc/sh/v3/syntax"

func (sw *shellWalk) evalIfCondition(stmts []*syntax.Stmt, wds wdSet) (bool, bool) {
	if len(stmts) == 0 {
		return false, false
	}
	if len(stmts) == 1 && stmts[0] != nil {
		if tc, ok := stmts[0].Cmd.(*syntax.TestClause); ok {
			return sw.evalTestClause(tc, wds)
		}
		if ce, ok := stmts[0].Cmd.(*syntax.CallExpr); ok {
			return sw.evalBracketTest(ce, wds)
		}
	}
	return false, false
}

func (sw *shellWalk) evalTestClause(tc *syntax.TestClause, wds wdSet) (bool, bool) {
	if tc == nil {
		return false, false
	}
	switch x := tc.X.(type) {
	case *syntax.BinaryTest:
		return sw.evalBinaryTest(x, wds)
	case *syntax.UnaryTest:
		return sw.evalUnaryTest(x, wds)
	case *syntax.ParenTest:
		return sw.evalIfCondition([]*syntax.Stmt{{Cmd: &syntax.TestClause{X: x.X}}}, wds)
	case *syntax.Word:
		return sw.evalBracketTest(&syntax.CallExpr{Args: []*syntax.Word{x}}, wds)
	default:
		return false, false
	}
}

func (sw *shellWalk) evalBinaryTest(bt *syntax.BinaryTest, wds wdSet) (bool, bool) {
	if bt == nil {
		return false, false
	}
	lhs, lok := sw.testWordValue(bt.X, wds)
	rhs, rok := sw.testWordValue(bt.Y, wds)
	if !lok || !rok {
		return false, false
	}
	switch bt.Op.String() {
	case "==":
		return lhs == rhs, true
	case "!=":
		return lhs != rhs, true
	default:
		return false, false
	}
}

func (sw *shellWalk) evalUnaryTest(ut *syntax.UnaryTest, wds wdSet) (bool, bool) {
	if ut == nil {
		return false, false
	}
	v, ok := sw.testWordValue(ut.X, wds)
	if !ok && testWordIsEmptyDefault(ut.X) {
		v, ok = "", true
	}
	if !ok {
		return false, false
	}
	switch ut.Op.String() {
	case "-n":
		return v != "", true
	case "-z":
		return v == "", true
	default:
		return false, false
	}
}

func (sw *shellWalk) evalBracketTest(ce *syntax.CallExpr, wds wdSet) (bool, bool) {
	if ce == nil || len(ce.Args) == 0 {
		return false, false
	}
	name := litWord(ce.Args[0])
	if name == "[" || name == "test" {
		if len(ce.Args) < 5 {
			return false, false
		}
		lhs, lok := sw.expandWord(ce.Args[1], wds)
		if !lok {
			return false, false
		}
		op := litWord(ce.Args[2])
		rhs, rok := sw.expandWord(ce.Args[3], wds)
		if !rok {
			return false, false
		}
		switch op {
		case "=":
			return lhs == rhs, true
		case "!=":
			return lhs != rhs, true
		}
		return false, false
	}
	// unary: [ -n "$x" ]
	if len(ce.Args) >= 3 && litWord(ce.Args[1]) == "-n" {
		v, ok := sw.expandWord(ce.Args[2], wds)
		if ok {
			return v != "", true
		}
	}
	if len(ce.Args) >= 3 && litWord(ce.Args[1]) == "-z" {
		v, ok := sw.expandWord(ce.Args[2], wds)
		if ok {
			return v == "", true
		}
	}
	return false, false
}

func testWordIsEmptyDefault(te syntax.TestExpr) bool {
	w, ok := te.(*syntax.Word)
	if !ok {
		return false
	}
	return onlyEmptyDefaultParam(w)
}

func (sw *shellWalk) testWordValue(te syntax.TestExpr, wds wdSet) (string, bool) {
	if w, ok := te.(*syntax.Word); ok {
		return sw.expandWord(w, wds)
	}
	if bt, ok := te.(*syntax.BinaryTest); ok {
		if val, ok := sw.evalBinaryTest(bt, wds); ok {
			if val {
				return "1", true
			}
			return "", true
		}
	}
	return "", false
}
