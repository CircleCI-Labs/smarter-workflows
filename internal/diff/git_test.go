package diff_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/CircleCI-Labs/smarter-workflows/internal/diff"
	"github.com/CircleCI-Labs/smarter-workflows/internal/diff/difftest"
)

var ctx = context.Background()

func TestParseNameStatus(t *testing.T) {
	nul := func(parts ...string) string { return strings.Join(parts, "\x00") + "\x00" }
	tests := []struct {
		name    string
		in      string
		want    []diff.Change
		wantErr string
	}{
		{name: "empty", in: "", want: nil},
		{
			name: "added, modified, deleted",
			in:   nul("A", "new.go", "M", "dir/mod.go", "D", "gone.go"),
			want: []diff.Change{{"new.go", "A"}, {"dir/mod.go", "M"}, {"gone.go", "D"}},
		},
		{
			name: "a rename is the old path deleted plus the new path added",
			in:   nul("R087", "old/name.go", "new/name.go", "M", "other.go"),
			want: []diff.Change{{"old/name.go", "D"}, {"new/name.go", "A"}, {"other.go", "M"}},
		},
		{
			name: "a copy only adds the new path",
			in:   nul("C100", "src.go", "copy.go"),
			want: []diff.Change{{"copy.go", "A"}},
		},
		{
			name: "type change and unmerged count as modified",
			in:   nul("T", "link", "U", "conflict.go"),
			want: []diff.Change{{"link", "M"}, {"conflict.go", "M"}},
		},
		{
			name: "paths with spaces, quotes and newlines are kept verbatim",
			in:   nul("A", "a b/\"q\".txt", "M", "line\nbreak.txt"),
			want: []diff.Change{{"a b/\"q\".txt", "A"}, {"line\nbreak.txt", "M"}},
		},
		{name: "missing path", in: nul("M"), wantErr: "missing a path"},
		{name: "rename with one path", in: nul("R100", "only"), wantErr: "missing a path"},
		{name: "empty status", in: nul("", "x"), wantErr: "empty status"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := diff.ParseNameStatus(tc.in)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestChangesInARealRepo(t *testing.T) {
	r := difftest.New(t)
	r.Write("keep.txt", "keep\n")
	r.Write("mod.txt", "one\n")
	r.Write("gone.txt", "bye\n")
	r.Write("dir/old name.go", strings.Repeat("package x\n// a line of content\n", 20))
	r.Write("ünï/cødé.txt", "unicode\n")
	r.Write("typechange", "plain\n")
	base := r.Commit("base")

	r.Write("mod.txt", "two\n")
	r.Remove("gone.txt")
	r.Write("added.txt", "new\n")
	r.Remove("dir/old name.go")
	r.Write("dir2/new name.go", strings.Repeat("package x\n// a line of content\n", 20)+"// tweak\n")
	r.Write("ünï/cødé.txt", "unicode changed\n")
	r.Remove("typechange")
	if err := os.Symlink("keep.txt", filepath.Join(r.Dir, "typechange")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	head := r.Commit("head")

	got, err := diff.Git{Dir: r.Dir}.Changes(ctx, base, head)
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(got, func(i, j int) bool { return got[i].Path < got[j].Path })
	want := []diff.Change{
		{"added.txt", "A"},
		{"dir/old name.go", "D"},
		{"dir2/new name.go", "A"},
		{"gone.txt", "D"},
		{"mod.txt", "M"},
		{"typechange", "M"},
		{"ünï/cødé.txt", "M"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("changes:\n got %v\nwant %v", got, want)
	}
}

func TestChangesRejectsOptionLikeRevisions(t *testing.T) {
	r := difftest.New(t)
	r.Write("a", "a")
	head := r.Commit("c")
	g := diff.Git{Dir: r.Dir}
	for _, bad := range []string{"--output=/dev/null", "-p", "", "a\nb"} {
		if _, err := g.Changes(ctx, bad, head); err == nil {
			t.Errorf("Changes(%q, head) must fail", bad)
		}
		if _, err := g.Changes(ctx, head, bad); err == nil {
			t.Errorf("Changes(head, %q) must fail", bad)
		}
		if _, err := g.ResolveCommit(ctx, bad); err == nil {
			t.Errorf("ResolveCommit(%q) must fail", bad)
		}
	}
	if _, err := g.Changes(ctx, "does-not-exist", head); err == nil {
		t.Error("an unknown revision must fail")
	}
}

func TestResolveCommit(t *testing.T) {
	r := difftest.New(t)
	r.Write("a", "a")
	sha := r.Commit("c")
	got, err := diff.Git{Dir: r.Dir}.ResolveCommit(ctx, "HEAD")
	if err != nil || got != sha {
		t.Fatalf("ResolveCommit(HEAD) = %q, %v; want %q", got, err, sha)
	}
	if _, err := (diff.Git{Dir: r.Dir}).ResolveCommit(ctx, "nope"); err == nil {
		t.Fatal("want an error for an unknown revision")
	}
}

// remoteSetup builds an upstream repo with main = C1,C2 and feature = C1,F1,
// and returns it with the three SHAs.
func remoteSetup(t *testing.T) (up *difftest.Repo, c1, c2, f1 string) {
	t.Helper()
	up = difftest.New(t)
	up.Write("a.txt", "a")
	c1 = up.Commit("C1")
	up.Git("checkout", "-q", "-b", "feature")
	up.Write("f.txt", "f")
	f1 = up.Commit("F1")
	up.Git("checkout", "-q", "main")
	up.Write("b.txt", "b")
	c2 = up.Commit("C2")
	return up, c1, c2, f1
}

func TestBase(t *testing.T) {
	t.Run("merge base of a branch cloned together with main", func(t *testing.T) {
		up, c1, _, _ := remoteSetup(t)
		work := t.TempDir()
		difftest.RunGit(t, work, nil, "clone", "-q", up.Dir, "w")
		dir := filepath.Join(work, "w")
		difftest.RunGit(t, dir, nil, "checkout", "-q", "feature")
		got := diff.Git{Dir: dir}.Base(ctx, "main", "feature", "")
		if got.SHA != c1 {
			t.Fatalf("Base = %+v, want SHA %s", got, c1)
		}
	})

	t.Run("origin/main is fetched when the clone only has the branch", func(t *testing.T) {
		up, c1, _, _ := remoteSetup(t)
		work := t.TempDir()
		difftest.RunGit(t, work, nil, "clone", "-q", "--single-branch", "--branch", "feature", up.Dir, "w")
		dir := filepath.Join(work, "w")
		if out := difftest.RunGit(t, dir, nil, "branch", "-r", "--list", "origin/main"); out != "" {
			t.Fatalf("setup: origin/main already exists: %q", out)
		}
		got := diff.Git{Dir: dir}.Base(ctx, "main", "feature", "")
		if got.SHA != c1 {
			t.Fatalf("Base = %+v, want SHA %s", got, c1)
		}
	})

	t.Run("no base on the default branch or on a tag", func(t *testing.T) {
		up, _, _, _ := remoteSetup(t)
		g := diff.Git{Dir: up.Dir}
		if got := g.Base(ctx, "main", "main", ""); got.SHA != "" || !strings.Contains(got.Why, "default branch") {
			t.Fatalf("default branch: %+v", got)
		}
		if got := g.Base(ctx, "main", "feature", "v1.2.3"); got.SHA != "" || !strings.Contains(got.Why, "tag v1.2.3") {
			t.Fatalf("tag: %+v", got)
		}
	})

	t.Run("git failures mean no base", func(t *testing.T) {
		// No origin remote at all.
		r := difftest.New(t)
		r.Write("a", "a")
		r.Commit("c")
		got := diff.Git{Dir: r.Dir}.Base(ctx, "main", "feature", "")
		if got.SHA != "" || !strings.Contains(got.Why, "origin/main") {
			t.Fatalf("no origin: %+v", got)
		}
		// Not a git repository.
		got = diff.Git{Dir: t.TempDir()}.Base(ctx, "main", "feature", "")
		if got.SHA != "" || got.Why == "" {
			t.Fatalf("not a repo: %+v", got)
		}
		// An option-looking default branch is refused.
		got = diff.Git{Dir: r.Dir}.Base(ctx, "--upload-pack=x", "feature", "")
		if got.SHA != "" || !strings.Contains(got.Why, "bad revision") {
			t.Fatalf("option-like default branch: %+v", got)
		}
	})

	t.Run("unrelated histories have no merge base", func(t *testing.T) {
		up, _, _, _ := remoteSetup(t)
		work := t.TempDir()
		difftest.RunGit(t, work, nil, "clone", "-q", up.Dir, "w")
		dir := filepath.Join(work, "w")
		difftest.RunGit(t, dir, nil, "checkout", "-q", "--orphan", "island")
		difftest.RunGit(t, dir, nil, "rm", "-rfq", ".")
		if err := os.WriteFile(filepath.Join(dir, "island.txt"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		difftest.RunGit(t, dir, nil, "add", "-A")
		difftest.RunGit(t, dir, []string{"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.com"}, "commit", "-q", "-m", "island")
		got := diff.Git{Dir: dir}.Base(ctx, "main", "island", "")
		if got.SHA != "" || !strings.Contains(got.Why, "merge base") {
			t.Fatalf("Base = %+v", got)
		}
	})
}

func TestDirsExist(t *testing.T) {
	r := difftest.New(t)
	r.Write("a/b/c.txt", "c")
	r.Write("x/y.txt", "y")
	r.Write("we*rd/file.txt", "w")
	rev1 := r.Commit("one")
	r.Remove("x")
	r.Write("n/m.txt", "m")
	rev2 := r.Commit("two")

	g := diff.Git{Dir: r.Dir}
	check := func(rev string, dirs []string, want map[string]bool) {
		t.Helper()
		got, err := g.DirsExist(ctx, rev, dirs)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("rev %s\n got %v\nwant %v", rev[:7], got, want)
		}
	}
	dirs := []string{".", "a", "a/b", "a/b/c.txt", "x", "n", "nope", "we*rd", "we?rd", "a/b/nope"}
	check(rev1, dirs, map[string]bool{
		".": true, "a": true, "a/b": true, "a/b/c.txt": false, "x": true, "n": false, "nope": false,
		"we*rd": true, "we?rd": false, "a/b/nope": false,
	})
	check(rev2, dirs, map[string]bool{
		".": true, "a": true, "a/b": true, "a/b/c.txt": false, "x": false, "n": true, "nope": false,
		"we*rd": true, "we?rd": false, "a/b/nope": false,
	})
	check(rev1, nil, map[string]bool{})
	if _, err := g.DirsExist(ctx, "no-such-rev", []string{"a"}); err == nil {
		t.Fatal("an unknown revision must fail")
	}
}

func TestDirsExistManyDirectories(t *testing.T) {
	r := difftest.New(t)
	var dirs []string
	want := map[string]bool{}
	for i := 0; i < 450; i++ { // more than one ls-tree batch
		d := fmt.Sprintf("d%03d", i)
		r.Write(d+"/f.txt", "x")
		dirs = append(dirs, d, fmt.Sprintf("missing%03d", i))
		want[d] = true
		want[fmt.Sprintf("missing%03d", i)] = false
	}
	rev := r.Commit("many")
	got, err := diff.Git{Dir: r.Dir}.DirsExist(ctx, rev, dirs)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("batched lookups disagree (%d vs %d entries)", len(got), len(want))
	}
}

// A test repository must not get git's background maintenance: after a commit
// it would keep writing into the repository while the test removes it.
func TestRepoHasNoBackgroundGit(t *testing.T) {
	r := difftest.New(t)
	for key, want := range map[string]string{"maintenance.auto": "false", "gc.auto": "0", "gc.autodetach": "false"} {
		if got := r.Git("config", "--get", key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestHeadMessage(t *testing.T) {
	r := difftest.New(t)
	r.Write("a", "a")
	r.Commit("subject line\n\nbody text\n\nSmarter-Full: true")
	msg, err := diff.Git{Dir: r.Dir}.HeadMessage(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "subject line") || !diff.AsksForFullRun(msg) {
		t.Fatalf("message = %q", msg)
	}
	if _, err := (diff.Git{Dir: r.Dir}).HeadMessage(ctx, "-1"); err == nil {
		t.Fatal("an option-looking revision must fail")
	}
}

func TestAsksForFullRun(t *testing.T) {
	tests := []struct {
		name string
		msg  string
		want bool
	}{
		{"plain message", "fix a bug", false},
		{"tag in the subject", "fix a bug [smarter full]", true},
		{"tag is case-insensitive", "Fix [Smarter Full] please", true},
		{"tag in the body", "subject\n\nsome text [SMARTER FULL] more text", true},
		{"trailer", "subject\n\nSmarter-Full: true", true},
		{"trailer after other trailers", "subject\n\nbody\n\nSigned-off-by: A <a@example.com>\nSmarter-Full: true\n", true},
		{"trailer key is case-insensitive", "subject\n\nsmarter-full: TRUE", true},
		{"trailer with CRLF", "subject\r\n\r\nSmarter-Full: true\r\n", true},
		{"trailer false", "subject\n\nSmarter-Full: false", false},
		{"trailer only counts in the last paragraph", "subject\n\nSmarter-Full: true\n\nmore prose after it", false},
		{"similar words", "smarter workflows are full of ideas", false},
		{"empty", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := diff.AsksForFullRun(tc.msg); got != tc.want {
				t.Fatalf("AsksForFullRun(%q) = %v, want %v", tc.msg, got, tc.want)
			}
		})
	}
}
