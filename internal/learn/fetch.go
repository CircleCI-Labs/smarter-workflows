package learn

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/CircleCI-Labs/smarter-workflows/internal/history"
)

// HistoryFetcher loads or downloads pipeline history JSON.
type HistoryFetcher interface {
	Fetch(ctx context.Context, projectSlug string, workflows []string, limit int, repoDir string, dest string) error
}

// LiveFetcher uses the CircleCI API (token from the environment).
type LiveFetcher struct {
	Host string
	Log  io.Writer
}

func (f LiveFetcher) Fetch(ctx context.Context, projectSlug string, workflows []string, limit int, repoDir string, dest string) error {
	token := os.Getenv("CIRCLE_TOKEN")
	if token == "" {
		token = os.Getenv("CIRCLECI_TOKEN")
	}
	if token == "" {
		return fmt.Errorf("CIRCLE_TOKEN or CIRCLECI_TOKEN is required to fetch history")
	}
	base := f.Host
	if base == "" {
		base = os.Getenv("CIRCLECI_HOST")
	}
	if base == "" {
		base = "https://circleci.com"
	}
	client := history.NewClient(base, token)
	if len(workflows) != 1 {
		return fmt.Errorf("live fetch: want one workflow per call")
	}
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()
	stats, err := history.Fetch(ctx, client, history.FetchOptions{
		ProjectSlug: projectSlug,
		Workflow:    workflows[0],
		Limit:       limit,
		RepoDir:     repoDir,
	}, out, f.Log)
	if err != nil {
		return err
	}
	if stats.Written == 0 {
		return fmt.Errorf("history fetch wrote no commits")
	}
	return nil
}
