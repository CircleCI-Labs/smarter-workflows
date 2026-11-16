package history

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maxRetries = 5

// Client calls the CircleCI API v2.
type Client struct {
	BaseURL    string
	Token      string
	HTTP       *http.Client
	MaxRetries int
}

// NewClient builds a client with a 30s per-request timeout unless HTTP is set.
func NewClient(baseURL, token string) *Client {
	return &Client{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		Token:      token,
		HTTP:       &http.Client{Timeout: 30 * time.Second},
		MaxRetries: maxRetries,
	}
}

func (c *Client) get(ctx context.Context, path string) ([]byte, error) {
	reqURL := c.BaseURL + "/" + strings.TrimPrefix(path, "/")
	var backoff time.Duration
	for attempt := 0; attempt < c.maxTries(); attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Circle-Token", c.Token)
		req.Header.Set("Accept", "application/json")

		resp, err := c.HTTP.Do(req)
		if err != nil {
			return nil, err
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}

		switch {
		case resp.StatusCode == http.StatusOK:
			return body, nil
		case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
			return nil, fmt.Errorf("API %s: unauthorized", path)
		case resp.StatusCode == http.StatusTooManyRequests:
			wait := retryAfter(resp.Header.Get("Retry-After"))
			if wait <= 0 {
				wait = backoffOrDefault(backoff, attempt)
			}
			backoff = wait
			if err := sleep(ctx, wait); err != nil {
				return nil, err
			}
		case resp.StatusCode >= 500:
			wait := backoffOrDefault(backoff, attempt)
			backoff = wait
			if err := sleep(ctx, wait); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("API %s: HTTP %d", path, resp.StatusCode)
		}
	}
	return nil, fmt.Errorf("API %s: gave up after retries", path)
}

func (c *Client) maxTries() int {
	if c.MaxRetries > 0 {
		return c.MaxRetries
	}
	return maxRetries
}

func retryAfter(h string) time.Duration {
	h = strings.TrimSpace(h)
	if h == "" {
		return 0
	}
	if sec, err := strconv.Atoi(h); err == nil {
		return time.Duration(sec) * time.Second
	}
	if t, err := http.ParseTime(h); err == nil {
		d := time.Until(t)
		if d < 0 {
			return 0
		}
		return d
	}
	return 0
}

func backoffOrDefault(prev time.Duration, attempt int) time.Duration {
	if prev > 0 {
		return prev * 2
	}
	return time.Duration(1<<attempt) * time.Second
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

type pipelinesPage struct {
	Items         []json.RawMessage `json:"items"`
	NextPageToken string            `json:"next_page_token"`
}

func pageURL(basePath, pageToken string) string {
	if pageToken == "" {
		return basePath
	}
	return basePath + "?page-token=" + url.QueryEscape(pageToken)
}

func (c *Client) Pipelines(ctx context.Context, projectSlug, pageToken string) (pipelinesPage, error) {
	path := pageURL("api/v2/project/"+projectSlug+"/pipeline", pageToken)
	data, err := c.get(ctx, path)
	if err != nil {
		return pipelinesPage{}, err
	}
	var page pipelinesPage
	if err := json.Unmarshal(data, &page); err != nil {
		return pipelinesPage{}, fmt.Errorf("decode pipelines: %w", err)
	}
	return page, nil
}

func (c *Client) Workflows(ctx context.Context, pipelineID string) (workflowPage, error) {
	var all []workflowItem
	token := ""
	for {
		path := pageURL("api/v2/pipeline/"+pipelineID+"/workflow", token)
		data, err := c.get(ctx, path)
		if err != nil {
			return workflowPage{}, err
		}
		var page workflowPage
		if err := json.Unmarshal(data, &page); err != nil {
			return workflowPage{}, fmt.Errorf("decode workflows: %w", err)
		}
		all = append(all, page.Items...)
		token = page.NextPageToken
		if token == "" {
			break
		}
	}
	return workflowPage{Items: all}, nil
}

func (c *Client) Jobs(ctx context.Context, workflowID string) (jobPage, error) {
	var all []jobItem
	token := ""
	for {
		path := pageURL("api/v2/workflow/"+workflowID+"/job", token)
		data, err := c.get(ctx, path)
		if err != nil {
			return jobPage{}, err
		}
		var page jobPage
		if err := json.Unmarshal(data, &page); err != nil {
			return jobPage{}, fmt.Errorf("decode jobs: %w", err)
		}
		all = append(all, page.Items...)
		token = page.NextPageToken
		if token == "" {
			break
		}
	}
	return jobPage{Items: all}, nil
}
