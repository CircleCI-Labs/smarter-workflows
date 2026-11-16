package impactmap

import "testing"

func TestNormalizeDirAndRootListing(t *testing.T) {
	j := &Job{Dirs: []string{"./"}, Complete: true}
	if !j.ListsDir(".") || !j.ListsDir("./") {
		t.Fatal("want ./ normalized to root listing")
	}
	if !j.CoversPath("internal/diff/git.go") {
		t.Fatal("repository root should cover nested paths")
	}
	jMix := &Job{Dirs: []string{".", "docs"}, Files: []string{"README.md"}, Complete: true}
	if !jMix.CoversPath("CHANGELOG.md") || !jMix.CoversPath("docs/x.md") {
		t.Fatal("root among inputs should cover any path")
	}
	j2 := &Job{Files: []string{"internal/diff/git.go"}, Complete: true}
	if !j2.CoversPath("internal/diff/git.go") || j2.CoversPath("docs/x.md") {
		t.Fatal("explicit file should not cover unrelated paths")
	}
}

func TestCoversPathRecursiveDir(t *testing.T) {
	j := &Job{Dirs: []string{"services/web/src"}, Complete: true}
	if !j.CoversPath("services/web/src/nested/new.ts") {
		t.Fatal("input dir should cover nested paths")
	}
	if j.CoversPath("services/api/main.go") {
		t.Fatal("sibling dir should not match")
	}
	jDel := &Job{Dirs: []string{"scripts"}, Complete: true}
	if !jDel.CoversPath("scripts/x.sh") {
		t.Fatal("deleted file under input dir should still match")
	}
}
