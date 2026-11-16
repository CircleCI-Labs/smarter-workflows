package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

const (
	reviewMaxTokens    = 4000
	reviewMaxInputTok  = 30000
	reviewDefaultModel = "claude-haiku-5-5"
	reviewSystemPrompt = "You review a CI plan. The plan skipped jobs that its impact map says are unreachable. Suggest jobs that should still run because the change could affect them. Return only jobs to add; never suggest removals."
)

// ReviewJob is one workflow job sent to the reviewer.
type ReviewJob struct {
	Name    string
	Summary string
	Inputs  string
}

// ReviewRequest is the plan context for one review call.
type ReviewRequest struct {
	ChangedFiles []string
	DiffHunks    string
	Skipped      []ReviewJob
	Selected     []ReviewJob
}

// ReviewResult is validated add-only output.
type ReviewResult struct {
	Add      []ReviewAdd
	Usage    UsageStats
	Warnings []string
}

// Reviewer calls Anthropic for plan-time review.
type Reviewer struct {
	client anthropic.Client
	cfg    ReviewerConfig
}

// ReviewerConfig tunes the reviewer client.
type ReviewerConfig struct {
	Model   string
	BaseURL string
	APIKey  string
	Timeout time.Duration
}

// NewReviewer builds a reviewer (reads API key from the environment when empty).
func NewReviewer(cfg ReviewerConfig) *Reviewer {
	if cfg.Model == "" {
		cfg.Model = reviewDefaultModel
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	opts := []option.RequestOption{
		option.WithRequestTimeout(cfg.Timeout),
		option.WithMaxRetries(1),
	}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	if cfg.APIKey != "" {
		opts = append(opts, option.WithAPIKey(cfg.APIKey))
	}
	return &Reviewer{client: anthropic.NewClient(opts...), cfg: cfg}
}

// Review runs one request and returns add suggestions.
func (r *Reviewer) Review(ctx context.Context, req ReviewRequest, allowed map[string]struct{}) (*ReviewResult, error) {
	ctx, cancel := context.WithTimeout(ctx, r.cfg.Timeout)
	defer cancel()
	user := buildReviewUser(req)
	params := anthropic.MessageNewParams{
		Model:     r.cfg.Model,
		MaxTokens: reviewMaxTokens,
		System: []anthropic.TextBlockParam{{
			Text: reviewSystemPrompt,
		}},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(user)),
		},
		OutputConfig: anthropic.OutputConfigParam{
			Effort: anthropic.OutputConfigEffortLow,
			Format: anthropic.JSONOutputFormatParam{Schema: ReviewJSONSchema()},
		},
	}
	if err := r.fitTokenBudget(ctx, &params, user); err != nil {
		return nil, err
	}
	msg, err := r.client.Messages.New(ctx, params)
	res := &ReviewResult{}
	res.Usage.Calls = 1
	if err != nil {
		return nil, wrapReviewErr(err)
	}
	res.Usage.InputTokens = msg.Usage.InputTokens
	res.Usage.OutputTokens = msg.Usage.OutputTokens
	if msg.StopReason == anthropic.StopReasonRefusal {
		res.Warnings = append(res.Warnings, "model refusal")
		return res, nil
	}
	if msg.StopReason == anthropic.StopReasonMaxTokens {
		return nil, fmt.Errorf("ai review: max_tokens stop")
	}
	text := extractText(msg)
	var parsed ReviewResponse
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		return nil, fmt.Errorf("ai review: invalid JSON: %w", err)
	}
	for _, a := range parsed.Add {
		if allowed != nil {
			if _, ok := allowed[a.Name]; !ok {
				res.Warnings = append(res.Warnings, fmt.Sprintf("unknown job %q ignored", a.Name))
				continue
			}
		}
		if a.Confidence < 0 || a.Confidence > 1 {
			res.Warnings = append(res.Warnings, fmt.Sprintf("job %q: confidence out of range", a.Name))
		}
		res.Add = append(res.Add, a)
	}
	return res, nil
}

func buildReviewUser(req ReviewRequest) string {
	var b strings.Builder
	b.WriteString("## Changed files\n\n")
	for _, f := range req.ChangedFiles {
		fmt.Fprintf(&b, "- %s\n", f)
	}
	if req.DiffHunks != "" {
		b.WriteString("\n## Unified diffs (trimmed)\n\n")
		b.WriteString(req.DiffHunks)
	}
	b.WriteString("\n## Skipped jobs\n\n")
	for _, j := range req.Skipped {
		fmt.Fprintf(&b, "### %s\n%s\nInputs: %s\n\n", j.Name, j.Summary, j.Inputs)
	}
	b.WriteString("## Selected jobs\n\n")
	for _, j := range req.Selected {
		fmt.Fprintf(&b, "### %s\n%s\nInputs: %s\n\n", j.Name, j.Summary, j.Inputs)
	}
	return b.String()
}

func (r *Reviewer) fitTokenBudget(ctx context.Context, params *anthropic.MessageNewParams, user string) error {
	msg := user
	for round := 0; round < 3; round++ {
		params.Messages = []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(msg)),
		}
		sys := ""
		if len(params.System) > 0 {
			sys = params.System[0].Text
		}
		res, err := r.client.Messages.CountTokens(ctx, anthropic.MessageCountTokensParams{
			Model:    params.Model,
			Messages: params.Messages,
			System: anthropic.MessageCountTokensParamsSystemUnion{
				OfString: anthropic.String(sys),
			},
		})
		if err != nil {
			return err
		}
		if int(res.InputTokens) <= reviewMaxInputTok {
			return nil
		}
		cut := len(msg) * 3 / 4
		if cut < 1024 {
			return fmt.Errorf("ai review: prompt exceeds token budget")
		}
		msg = msg[:cut]
	}
	return fmt.Errorf("ai review: prompt exceeds token budget after trim")
}

func wrapReviewErr(err error) error {
	var apierr *anthropic.Error
	if errors.As(err, &apierr) {
		switch apierr.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return fmt.Errorf("ai review: API key rejected")
		}
	}
	return err
}
