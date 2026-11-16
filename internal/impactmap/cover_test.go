package impactmap

import "testing"

func TestNormalizeDirAndRootListing(t *testing.T) {
	j := &Job{Dirs: []string{"./"}, Complete: true}
	if !j.ListsDir(".") || !j.ListsDir("./") {
		t.Fatal("want ./ normalized to root listing")
	}
	if !j.CoversPath("internal/diff/git.go") {
		t.Fatal("root-only listing should cover nested paths")
	}
	jLint := &Job{Dirs: []string{".", "docs"}, Files: []string{"README.md"}, Complete: true}
	if jLint.CoversPath("CHANGELOG.md") {
		t.Fatal("root among other dirs should not cover unrelated root files")
	}
	j2 := &Job{Files: []string{"internal/diff/git.go"}, Complete: true}
	if !j2.CoversPath("internal/diff/git.go") || j2.CoversPath("docs/x.md") {
		t.Fatal("explicit file should not cover unrelated paths")
	}
}
