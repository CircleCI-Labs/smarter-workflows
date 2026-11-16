package analyze

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/CircleCI-Labs/smarter-workflows/internal/ai"
	"github.com/CircleCI-Labs/smarter-workflows/internal/config"
	"github.com/CircleCI-Labs/smarter-workflows/internal/impactmap"
)

// AIMode controls classifier usage.
type AIMode string

const (
	AIModeAuto   AIMode = "auto"
	AIModeOn     AIMode = "on"
	AIModeOff    AIMode = "off"
	AIModeDryRun AIMode = "dry-run"
)

// RunOptions configures analyze.Run.
type RunOptions struct {
	RepoRoot         string
	ConfigPath       string
	Workflows        []string
	Revision         string
	HistoryPath      string
	LearnedPath      string
	LearnedOutPath   string
	AIMode           AIMode
	Model            string
	MinConfidence    float64
	AIMaxInputTokens int
	AIBatchSize      int
	AITimeout        time.Duration
	MapOut           string
	ReportMD         string
	ReportJSON       string
	TruthPath        string
}

// Run builds an impact map, optional learned file, and report.
func Run(ctx context.Context, opts RunOptions) (*impactmap.Map, *Report, *Learned, error) {
	if opts.RepoRoot == "" {
		opts.RepoRoot = "."
	}
	if opts.ConfigPath == "" {
		opts.ConfigPath = filepath.Join(opts.RepoRoot, ".circleci/continue.yml")
	}
	if opts.MinConfidence <= 0 {
		opts.MinConfidence = 0.8
	}
	if opts.Model == "" {
		opts.Model = "claude-haiku-5-5"
	}
	if opts.AIMaxInputTokens <= 0 {
		opts.AIMaxInputTokens = 90000
	}
	if opts.AIBatchSize <= 0 {
		opts.AIBatchSize = 20
	}
	if opts.AITimeout <= 0 {
		opts.AITimeout = 5 * time.Minute
	}
	rev := opts.Revision
	if rev == "" {
		rev = gitHEAD(opts.RepoRoot)
	}
	data, err := os.ReadFile(opts.ConfigPath)
	if err != nil {
		return nil, nil, nil, err
	}
	cfg, err := config.Parse(data)
	if err != nil {
		return nil, nil, nil, err
	}
	workflows := opts.Workflows
	if len(workflows) == 0 {
		workflows = cfg.Workflows()
	}
	prevLearned, err := loadLearned(opts.LearnedPath)
	if err != nil {
		return nil, nil, nil, err
	}
	jobInputs := map[string]*InputSet{}
	jobMeta := map[string]jobRunMeta{}
	eng := NewEngine(opts.RepoRoot)
	for _, wfName := range workflows {
		wf, err := cfg.Workflow(wfName)
		if err != nil {
			return nil, nil, nil, err
		}
		for _, e := range wf.Entries {
			name := entryName(e)
			if e.Approval || e.Kind == config.KindNoOp || e.Kind == config.KindApproval {
				jobInputs[name] = newInputSet()
				hash, _ := jobHash(cfg, e)
				jobMeta[name] = jobRunMeta{hash: hash, entry: e, workflow: wfName, proof: &JobProof{}}
				continue
			}
			in, proof := eng.InferJob(cfg, e)
			hash, herr := jobHash(cfg, e)
			if herr != nil {
				return nil, nil, nil, herr
			}
			jobInputs[name] = in
			jobMeta[name] = jobRunMeta{hash: hash, entry: e, workflow: wfName, proof: proof}
		}
	}
	applyLearnedInputs(opts.RepoRoot, prevLearned, jobMeta, jobInputs)
	var corrections []Correction
	if opts.HistoryPath != "" {
		corrections, err = applyHistory(opts.RepoRoot, opts.HistoryPath, jobInputs)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	report := &Report{PriceNote: "Haiku 5.5 ≤100k input tier; prices as of " + aiPriceAsOf()}
	var learnedOut *Learned
	aiEnabled := aiModeEnabled(opts.AIMode)
	aiWf := workflows[0]
	var classifier *ai.Classifier
	if aiEnabled || opts.AIMode == AIModeDryRun {
		classifier = ai.NewClassifier(ai.ClassifierConfig{
			Model:          opts.Model,
			MaxInputTokens: opts.AIMaxInputTokens,
			BatchSize:      opts.AIBatchSize,
			Timeout:        opts.AITimeout,
			RepoRoot:       opts.RepoRoot,
			DryRunDir:      dryRunDir(opts),
			JobNames:       jobNameSet(jobInputs),
			BuildBatch:     aiBatchBuilder(cfg, opts, aiWf),
		})
	}
	if aiEnabled {
		ctx, cancel := context.WithTimeout(ctx, opts.AITimeout)
		defer cancel()
		if err := runAIClassifier(ctx, classifier, cfg, opts, jobInputs, jobMeta, report); err != nil {
			if opts.AIMode == AIModeOn {
				return nil, nil, nil, err
			}
			report.IncompleteJobs++ // placeholder; fixed below
			_ = err
		}
	} else if opts.AIMode == AIModeDryRun {
		_ = runAIClassifier(ctx, classifier, cfg, opts, jobInputs, jobMeta, report)
	}
	learnedOut = buildLearned(opts, jobMeta, jobInputs, corrections, classifier)
	if prevLearned != nil {
		mergeLearnedForward(prevLearned, learnedOut)
	}
	m := assembleMap(opts.RepoRoot, rev, jobInputs, jobMeta, opts.MinConfidence, prevLearned, report)
	if opts.TruthPath != "" {
		if err := appendTruthReport(opts.TruthPath, m, report); err != nil {
			return nil, nil, nil, err
		}
	}
	if opts.MapOut != "" {
		if err := WriteMap(opts.MapOut, m); err != nil {
			return nil, nil, nil, err
		}
	}
	if opts.LearnedOutPath != "" {
		if err := writeLearned(opts.LearnedOutPath, learnedOut); err != nil {
			return nil, nil, nil, err
		}
	}
	if opts.ReportMD != "" {
		if err := WriteReportMarkdown(opts.ReportMD, report); err != nil {
			return nil, nil, nil, err
		}
	}
	if opts.ReportJSON != "" {
		if err := WriteReportJSON(opts.ReportJSON, report); err != nil {
			return nil, nil, nil, err
		}
	}
	return m, report, learnedOut, nil
}

type jobRunMeta struct {
	hash     string
	entry    *config.Entry
	workflow string
	proof    *JobProof
	ai       *ai.JobResult
}

func entryName(e *config.Entry) string {
	if e.Name != "" {
		return e.Name
	}
	return e.Ref
}

func gitHEAD(repo string) string {
	cmd := exec.Command("git", "-C", repo, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func aiModeEnabled(mode AIMode) bool {
	switch mode {
	case AIModeOn:
		return true
	case AIModeAuto:
		return ai.APIKeyPresent()
	default:
		return false
	}
}

func dryRunDir(opts RunOptions) string {
	if opts.AIMode != AIModeDryRun {
		return ""
	}
	dir := "."
	if opts.ReportMD != "" {
		dir = filepath.Dir(opts.ReportMD)
	}
	return filepath.Join(dir, "ai-requests")
}

func aiPriceAsOf() string {
	return "2026-10"
}

func jobNameSet(jobs map[string]*InputSet) map[string]struct{} {
	out := map[string]struct{}{}
	for k := range jobs {
		out[k] = struct{}{}
	}
	return out
}

func applyLearnedInputs(repoRoot string, prev *Learned, meta map[string]jobRunMeta, jobs map[string]*InputSet) {
	if prev == nil {
		return
	}
	for name, job := range prev.Jobs {
		if job == nil {
			continue
		}
		in := jobs[name]
		if in == nil {
			continue
		}
		for _, c := range job.Corrections {
			unionPath(repoRoot, in, c.Path, InputNote{Signal: SignalHistory, Detail: c.Commit})
		}
		if job.AI != nil {
			for _, p := range job.AI.Inputs {
				unionPath(repoRoot, in, p, InputNote{Signal: SignalAI, Detail: ""})
			}
		}
		_ = meta[name]
	}
}

func unionPath(repoRoot string, in *InputSet, p string, note InputNote) {
	p = strings.TrimSuffix(p, "/")
	if p == "" || strings.Contains(p, "..") || !filepath.IsLocal(p) {
		return
	}
	in.addVerifiedPath(repoRoot, p, note)
}

func assembleMap(repoRoot, rev string, jobs map[string]*InputSet, meta map[string]jobRunMeta, minConf float64, prev *Learned, rep *Report) *impactmap.Map {
	m := &impactmap.Map{
		Version:   impactmap.Version,
		Revision:  rev,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Method:    MethodInference,
		Jobs:      map[string]*impactmap.Job{},
	}
	rep.CompleteJobs = 0
	rep.IncompleteJobs = 0
	rep.Jobs = nil
	for name, in := range jobs {
		jm := meta[name]
		hash := jm.hash
		staticOK := jm.proof != nil && jm.proof.StaticComplete()
		why := ""
		if !staticOK {
			if jm.proof != nil && jm.proof.IncompleteReason != "" {
				why = jm.proof.IncompleteReason
			} else {
				why = "static proof failed"
			}
		}
		aiOK := aiCompletes(name, hash, minConf, jm, prev)
		complete := staticOK || aiOK
		if complete && !staticOK {
			why = "completed via AI classifier"
		}
		j := &impactmap.Job{
			Files:    in.sortedFiles(),
			Dirs:     in.sortedDirs(),
			Complete: complete,
			Nodes:    1,
			JobHash:  hash,
		}
		m.Jobs[name] = j
		if complete {
			rep.CompleteJobs++
		} else {
			rep.IncompleteJobs++
		}
		rep.Jobs = append(rep.Jobs, buildJobReport(name, in, jm.proof, complete, why, hash))
	}
	return m
}

func aiCompletes(name, hash string, minConf float64, jm jobRunMeta, prev *Learned) bool {
	if jm.ai != nil && len(jm.ai.Inputs) > 0 && jm.ai.Confidence >= minConf {
		return true
	}
	if prev == nil {
		return false
	}
	lj, ok := prev.Jobs[name]
	if !ok || lj == nil || lj.AI == nil {
		return false
	}
	if lj.JobHash != hash || lj.AI.Confidence < minConf {
		return false
	}
	return len(lj.AI.Inputs) > 0
}

func buildLearned(opts RunOptions, meta map[string]jobRunMeta, jobs map[string]*InputSet, corr []Correction, clf *ai.Classifier) *Learned {
	l := &Learned{
		Version:   learnedVersion,
		Revision:  opts.Revision,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Model:     opts.Model,
		Jobs:      map[string]*LearnedJob{},
	}
	now := l.CreatedAt
	for name, in := range jobs {
		m := meta[name]
		lj := &LearnedJob{JobHash: m.hash}
		for _, c := range corr {
			if c.Job == name {
				lj.Corrections = append(lj.Corrections, LearnedCorrection{Path: c.Path, Commit: c.Commit, LearnedAt: now})
			}
		}
		l.Jobs[name] = lj
		_ = in
	}
	if clf != nil {
		// AI outputs filled by runAIClassifier via side channel if needed
	}
	return l
}

func aiBatchBuilder(cfg *config.Config, opts RunOptions, wfName string) ai.BatchBuildFunc {
	return func(jobs []ai.BatchJob) (ai.Batch, error) {
		pkg, err := BuildLLMPrompt(opts.RepoRoot, opts.ConfigPath, wfName)
		if err != nil {
			return ai.Batch{}, err
		}
		_ = cfg
		_ = jobs
		reqJSON, err := json.Marshal(pkg)
		if err != nil {
			return ai.Batch{}, err
		}
		return ai.Batch{Jobs: jobs, UserMessage: string(reqJSON), RequestJSON: reqJSON}, nil
	}
}

func runAIClassifier(ctx context.Context, clf *ai.Classifier, cfg *config.Config, opts RunOptions, jobs map[string]*InputSet, jobMeta map[string]jobRunMeta, rep *Report) error {
	if clf == nil {
		return nil
	}
	wfs := opts.Workflows
	if len(wfs) == 0 {
		wfs = cfg.Workflows()
	}
	if len(wfs) == 0 {
		return fmt.Errorf("analyze: no workflows in config")
	}
	wfName := wfs[0]
	pkg, err := BuildLLMPrompt(opts.RepoRoot, opts.ConfigPath, wfName)
	if err != nil {
		return err
	}
	system := pkg.Instructions
	reqJSON, err := json.Marshal(pkg)
	if err != nil {
		return err
	}
	batch := ai.Batch{Jobs: nil, UserMessage: string(reqJSON), RequestJSON: reqJSON}
	results, _, err := clf.ClassifyJobs(ctx, system, []ai.Batch{batch})
	if err != nil {
		return err
	}
	rep.AICalls = clf.Usage.Calls
	rep.InputTokens = clf.Usage.InputTokens
	rep.OutputTokens = clf.Usage.OutputTokens
	rep.EstimatedCostUSD = clf.Usage.EstimatedCostUSD()
	for name, res := range results {
		in := jobs[name]
		if in == nil {
			continue
		}
		meta := jobMeta[name]
		meta.ai = &res
		jobMeta[name] = meta
		for _, p := range res.Inputs {
			unionPath(opts.RepoRoot, in, p, InputNote{Signal: SignalAI, Detail: fmtWhy(res.Why, res.Confidence)})
		}
	}
	return nil
}

func fmtWhy(why string, conf float64) string {
	return fmt.Sprintf("confidence %.2f: %s", conf, why)
}
