package history

import (
	"context"
	"testing"

	"github.com/CircleCI-Labs/smarter-workflows/internal/diff"
	"github.com/CircleCI-Labs/smarter-workflows/internal/diff/difftest"
)

func TestHistoricalBaseMergedPullRequest(t *testing.T) {
	h, featureTip := buildMergeScenario(t)
	ctx := context.Background()

	base, err := h.historicalBase(ctx, featureTip)
	if err != nil {
		t.Fatal(err)
	}
	if base == "" || base == featureTip {
		t.Fatalf("base = %q", base)
	}
	changed, err := h.changedFiles(ctx, base, featureTip)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) == 0 {
		t.Fatal("expected non-empty diff for merged feature tip")
	}
}

func TestHistoricalBaseUnmergedBranch(t *testing.T) {
	h, _ := buildMergeScenario(t)
	ctx := context.Background()
	tip := difftest.RunGit(t, h.G.Dir, nil, "rev-parse", "side-tip")

	base, err := h.historicalBase(ctx, tip)
	if err != nil {
		t.Fatal(err)
	}
	want := difftest.RunGit(t, h.G.Dir, nil, "merge-base", "origin/main", "side-tip")
	if base != want {
		t.Fatalf("base = %q want merge-base %q", base, want)
	}
}

func TestHistoricalBaseMainCommit(t *testing.T) {
	h, _ := buildMergeScenario(t)
	ctx := context.Background()
	mainHead := difftest.RunGit(t, h.G.Dir, nil, "rev-parse", "main")
	parent := difftest.RunGit(t, h.G.Dir, nil, "rev-parse", "main^")

	base, err := h.historicalBase(ctx, mainHead)
	if err != nil {
		t.Fatal(err)
	}
	if base != parent {
		t.Fatalf("base = %q want parent %q", base, parent)
	}
}

func TestHistoricalBaseRename(t *testing.T) {
	repo := difftest.New(t)
	configureTestRepo(t, repo.Dir)
	setupOriginMain(t, repo, repo.Commit("start"))
	repo.Commit("add file")
	repo.Write("old/name.txt", "x")
	repo.Commit("touch")
	repo.Remove("old/name.txt")
	repo.Write("new/name.txt", "x")
	head := repo.Commit("done")
	setupOriginMain(t, repo, head)
	difftest.RunGit(t, repo.Dir, nil, "update-ref", "refs/remotes/origin/main", head)

	h := &Repo{G: diff.Git{Dir: repo.Dir}}
	ctx := context.Background()
	base, err := h.historicalBase(ctx, head)
	if err != nil {
		t.Fatal(err)
	}
	if base == "" {
		t.Fatal("expected base")
	}
	changed, err := h.changedFiles(ctx, base, head)
	if err != nil {
		t.Fatal(err)
	}
	var delOld, addNew bool
	for _, c := range changed {
		switch {
		case c.Path == "old/name.txt" && c.Status == "D":
			delOld = true
		case c.Path == "new/name.txt" && c.Status == "A":
			addNew = true
		}
	}
	if !delOld || !addNew {
		t.Fatalf("rename changes = %#v", changed)
	}
}

func configureTestRepo(t *testing.T, dir string) {
	t.Helper()
	difftest.RunGit(t, dir, nil, "config", "user.email", "author@example.com")
	difftest.RunGit(t, dir, nil, "config", "user.name", "Test Author")
}

func buildMergeScenario(t *testing.T) (*Repo, string) {
	t.Helper()
	repo := difftest.New(t)
	configureTestRepo(t, repo.Dir)
	root := repo.Commit("root")
	setupOriginMain(t, repo, root)
	repo.Write("README", "base\n")
	main1 := repo.Commit("main-1")
	setupOriginMain(t, repo, main1)
	difftest.RunGit(t, repo.Dir, nil, "branch", "feature")
	difftest.RunGit(t, repo.Dir, nil, "checkout", "feature")
	repo.Write("feature.txt", "work\n")
	featureTip := repo.Commit("feature work")
	difftest.RunGit(t, repo.Dir, nil, "checkout", "main")
	difftest.RunGit(t, repo.Dir, nil, "merge", "--no-ff", "-m", "Merge feature", "feature")
	mainHead := repo.Git("rev-parse", "HEAD")
	setupOriginMain(t, repo, mainHead)

	difftest.RunGit(t, repo.Dir, nil, "branch", "side")
	difftest.RunGit(t, repo.Dir, nil, "checkout", "side")
	repo.Write("side.txt", "side\n")
	difftest.RunGit(t, repo.Dir, nil, "add", "-A")
	_ = repo.Commit("side work")
	difftest.RunGit(t, repo.Dir, nil, "update-ref", "refs/heads/side-tip", "HEAD")
	difftest.RunGit(t, repo.Dir, nil, "checkout", "main")

	return &Repo{G: diff.Git{Dir: repo.Dir}}, featureTip
}
