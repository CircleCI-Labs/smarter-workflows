package backtest

import "testing"

func TestMergeParameters(t *testing.T) {
	base := map[string]any{"a": true, "b": 1}
	got := mergeParameters(base, map[string]any{"b": 2, "c": false})
	if got["a"] != true || got["b"] != 2 || got["c"] != false {
		t.Fatalf("merge = %#v", got)
	}
	if mergeParameters(nil, nil) != nil {
		t.Fatal("nil merge should be nil")
	}
}
