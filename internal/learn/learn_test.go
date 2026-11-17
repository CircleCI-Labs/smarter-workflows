package learn_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/CircleCI-Labs/smarter-workflows/internal/analyze"
	"github.com/CircleCI-Labs/smarter-workflows/internal/backtest"
	"github.com/CircleCI-Labs/smarter-workflows/internal/learn"
)

type fakeFetch struct {
	src string
}

func (f fakeFetch) Fetch(_ context.Context, _ string, _ []string, _ int, _ string, dest string) error {
	in, err := os.ReadFile(f.src)
	if err != nil {
		return err
	}
	return os.WriteFile(dest, in, 0o640)
}

func TestLearnWithHistoryFixture(t *testing.T) {
	root := filepath.Join("..", "..")
	t.Chdir(root)
	cfg := filepath.Join("internal", "backtest", "testdata", "param-gated", "config.yml")
	hist := filepath.Join("internal", "backtest", "testdata", "param-gated", "history.json")
	repoRoot := "."
	outDir := t.TempDir()
	learnedOut := filepath.Join(outDir, "learned.json")
	report := filepath.Join(outDir, "learn.md")
	res, err := learn.Run(context.Background(), learn.Options{
		RepoRoot:       repoRoot,
		ConfigPath:     cfg,
		Workflows:      []string{"ci"},
		HistoryPath:    hist,
		LearnedOutPath: learnedOut,
		ReportMD:       report,
		AIMode:         analyze.AIModeOff,
		Planner:        backtest.Options{Workflow: "ci", DefaultBranch: "main"},
	}, fakeFetch{src: hist}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if res.BacktestAfter == nil || res.BacktestAfter.Commits == 0 {
		t.Fatal("expected backtest commits")
	}
	if _, err := os.Stat(learnedOut); err != nil {
		t.Fatal(err)
	}
}
