package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

const (
	defaultModel     = "claude-haiku-5-5"
	defaultMaxTokens = 16000
	priceInPerM      = 0.10
	priceOutPerM     = 0.50
)

// BatchBuildFunc rebuilds the user message for a job subset (e.g. after max_tokens split).
type BatchBuildFunc func(jobs []BatchJob) (Batch, error)

// ClassifierConfig tunes the Anthropic classifier.
type ClassifierConfig struct {
	Model          string
	MaxInputTokens int
	BatchSize      int
	Timeout        time.Duration
	RepoRoot       string
	BaseURL        string
	APIKey         string
	DryRunDir      string
	JobNames       map[string]struct{}
	BuildBatch     BatchBuildFunc
}

// UsageStats aggregates token use and cost.
type UsageStats struct {
	Calls        int
	InputTokens  int64
	OutputTokens int64
}

// EstimatedCostUSD returns USD estimate from usage (Haiku 5.5 ≤100k input tier).
func (u UsageStats) EstimatedCostUSD() float64 {
	return float64(u.InputTokens)/1e6*priceInPerM + float64(u.OutputTokens)/1e6*priceOutPerM
}

// JobResult is validated classifier output for one job.
type JobResult struct {
	Inputs     []string
	Confidence float64
	Why        string
}

// Classifier calls the Anthropic Messages API with structured output.
type Classifier struct {
	client anthropic.Client
	cfg    ClassifierConfig
	Usage  UsageStats
}

// NewClassifier builds a client (reads ANTHROPIC_API_KEY when APIKey is empty).
func NewClassifier(cfg ClassifierConfig) *Classifier {
	opts := []option.RequestOption{
		option.WithRequestTimeout(90 * time.Second),
		option.WithMaxRetries(2),
	}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	if cfg.APIKey != "" {
		opts = append(opts, option.WithAPIKey(cfg.APIKey))
	}
	return &Classifier{client: anthropic.NewClient(opts...), cfg: cfg}
}

// ClassifyJobs runs batches and returns validated results per job name.
func (c *Classifier) ClassifyJobs(ctx context.Context, system string, batches []Batch) (map[string]JobResult, []string, error) {
	out := map[string]JobResult{}
	var warnings []string
	for _, batch := range batches {
		res, warn, err := c.classifyBatch(ctx, system, batch, false)
		if err != nil {
			return out, warnings, err
		}
		warnings = append(warnings, warn...)
		for k, v := range res {
			out[k] = v
		}
	}
	return out, warnings, nil
}

// Batch is one model request worth of jobs and trimmed file contents.
type Batch struct {
	Jobs        []BatchJob
	UserMessage string
	RequestJSON []byte
}

// BatchJob is metadata for one job in a batch.
type BatchJob struct {
	Name  string
	Ref   string
	Steps []string
}

func (c *Classifier) classifyBatch(ctx context.Context, system string, batch Batch, alreadySplit bool) (map[string]JobResult, []string, error) {
	if c.cfg.DryRunDir != "" {
		if err := os.MkdirAll(c.cfg.DryRunDir, 0o755); err != nil {
			return nil, nil, err
		}
		name := fmt.Sprintf("batch-%d.json", time.Now().UnixNano())
		if err := os.WriteFile(filepath.Join(c.cfg.DryRunDir, name), batch.RequestJSON, 0o644); err != nil {
			return nil, nil, err
		}
		return map[string]JobResult{}, nil, nil
	}
	params := anthropic.MessageNewParams{
		Model:     c.cfg.Model,
		MaxTokens: defaultMaxTokens,
		System: []anthropic.TextBlockParam{{
			Text: system,
		}},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(batch.UserMessage)),
		},
		OutputConfig: anthropic.OutputConfigParam{
			Effort: anthropic.OutputConfigEffortLow,
			Format: anthropic.JSONOutputFormatParam{Schema: ResponseJSONSchema()},
		},
	}
	if err := c.fitTokenBudget(ctx, &params, batch.UserMessage); err != nil {
		return nil, nil, err
	}
	msg, err := c.client.Messages.New(ctx, params)
	c.Usage.Calls++
	if err != nil {
		return nil, nil, c.wrapAPIErr(err)
	}
	c.Usage.InputTokens += msg.Usage.InputTokens
	c.Usage.OutputTokens += msg.Usage.OutputTokens
	if msg.StopReason == anthropic.StopReasonMaxTokens {
		if alreadySplit || len(batch.Jobs) <= 1 || c.cfg.BuildBatch == nil {
			return nil, []string{"max_tokens stop"}, fmt.Errorf("ai: max_tokens stop")
		}
		mid := len(batch.Jobs) / 2
		leftB, err := c.cfg.BuildBatch(batch.Jobs[:mid])
		if err != nil {
			return nil, nil, err
		}
		rightB, err := c.cfg.BuildBatch(batch.Jobs[mid:])
		if err != nil {
			return nil, nil, err
		}
		left, w1, err := c.classifyBatch(ctx, system, leftB, true)
		if err != nil {
			return nil, w1, err
		}
		right, w2, err := c.classifyBatch(ctx, system, rightB, true)
		if err != nil {
			return nil, append(w1, w2...), err
		}
		for k, v := range right {
			left[k] = v
		}
		return left, append(w1, w2...), nil
	}
	if msg.StopReason == anthropic.StopReasonRefusal {
		return map[string]JobResult{}, []string{"model refusal for batch"}, nil
	}
	text := extractText(msg)
	var parsed ClassifierResponse
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		return nil, nil, fmt.Errorf("ai: invalid JSON: %w", err)
	}
	return validateResults(parsed, c.cfg.RepoRoot, c.cfg.JobNames)
}

func (c *Classifier) fitTokenBudget(ctx context.Context, params *anthropic.MessageNewParams, user string) error {
	if c.cfg.MaxInputTokens <= 0 {
		return nil
	}
	msg := user
	for round := 0; round < 3; round++ {
		params.Messages = []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(msg)),
		}
		sys := ""
		if len(params.System) > 0 {
			sys = params.System[0].Text
		}
		res, err := c.client.Messages.CountTokens(ctx, anthropic.MessageCountTokensParams{
			Model:    params.Model,
			Messages: params.Messages,
			System: anthropic.MessageCountTokensParamsSystemUnion{
				OfString: anthropic.String(sys),
			},
		})
		if err != nil {
			return err
		}
		if int(res.InputTokens) <= c.cfg.MaxInputTokens {
			return nil
		}
		cut := len(msg) * 3 / 4
		if cut < 1024 {
			return fmt.Errorf("ai: prompt exceeds token budget")
		}
		msg = msg[:cut]
	}
	return fmt.Errorf("ai: prompt exceeds token budget after trim")
}

func extractText(msg *anthropic.Message) string {
	var b strings.Builder
	for _, block := range msg.Content {
		if block.Type == "text" {
			b.WriteString(block.AsText().Text)
		}
	}
	return b.String()
}

func validateResults(parsed ClassifierResponse, repoRoot string, allowed map[string]struct{}) (map[string]JobResult, []string, error) {
	out := map[string]JobResult{}
	var warns []string
	for _, j := range parsed.Jobs {
		if allowed != nil {
			if _, ok := allowed[j.Name]; !ok {
				warns = append(warns, fmt.Sprintf("unknown job %q", j.Name))
				continue
			}
		}
		conf := j.Confidence
		if conf < 0 || conf > 1 {
			warns = append(warns, fmt.Sprintf("job %q: confidence out of range", j.Name))
			conf = 0
		}
		var paths []string
		invalid := 0
		for _, p := range j.Inputs {
			p = strings.TrimSpace(filepath.ToSlash(p))
			if p == "" || strings.Contains(p, "..") || !filepath.IsLocal(p) || filepath.IsAbs(p) {
				invalid++
				continue
			}
			abs := filepath.Join(repoRoot, filepath.FromSlash(p))
			if st, err := os.Stat(abs); err != nil {
				invalid++
				continue
			} else if st.IsDir() {
				paths = append(paths, p)
			} else {
				paths = append(paths, p)
			}
		}
		if len(j.Inputs) > 0 && invalid > len(j.Inputs)/2 {
			conf = 0
			warns = append(warns, fmt.Sprintf("job %q: majority of AI paths invalid", j.Name))
		}
		out[j.Name] = JobResult{Inputs: paths, Confidence: conf, Why: j.Why}
	}
	return out, warns, nil
}

func (c *Classifier) wrapAPIErr(err error) error {
	var apierr *anthropic.Error
	if errors.As(err, &apierr) {
		switch apierr.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return fmt.Errorf("ai: API key rejected")
		case http.StatusBadRequest:
			return fmt.Errorf("ai: request rejected: %s", apierr.Error())
		}
	}
	return err
}

// APIKeyPresent checks env for AI credentials without reading values into logs.
func APIKeyPresent() bool {
	_, a := os.LookupEnv("ANTHROPIC_API_KEY")
	_, b := os.LookupEnv("ANTHROPIC_AUTH_TOKEN")
	return a || b
}
