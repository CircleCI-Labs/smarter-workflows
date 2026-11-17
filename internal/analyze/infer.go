package analyze

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	"github.com/CircleCI-Labs/smarter-workflows/internal/backtest"
	"github.com/CircleCI-Labs/smarter-workflows/internal/config"
	"github.com/CircleCI-Labs/smarter-workflows/internal/impactmap"
)

// Options tune a full analyze run.
type Options struct {
	RepoRoot   string
	ConfigPath string
	Workflow   string
	Revision   string
	History    string
	LLMResult  string
	// LLMOnly skips static inference; LLMResult must be set.
	LLMOnly bool
}

// LLMJobInputs is the JSON shape models return for analyze --llm-result.
type LLMJobInputs struct {
	Inputs     []string `json:"inputs"`
	Confidence float64  `json:"confidence"`
	Why        string   `json:"why"`
}

// LLMResultFile is the top-level LLM JSON file.
type LLMResultFile struct {
	Jobs map[string]LLMJobInputs `json:"jobs"`
}

// Correction describes a history-driven path addition.
type Correction struct {
	Job      string `json:"job"`
	Path     string `json:"path"`
	Commit   string `json:"commit"`
	Pipeline string `json:"pipeline,omitempty"`
}

// Build runs static inference (and optional LLM merge and history correction).
func Build(opts Options) (*impactmap.Map, []Correction, error) {
	data, err := os.ReadFile(opts.ConfigPath)
	if err != nil {
		return nil, nil, err
	}
	cfg, err := config.Parse(data)
	if err != nil {
		return nil, nil, err
	}
	wf, err := cfg.Workflow(opts.Workflow)
	if err != nil {
		return nil, nil, err
	}
	jobInputs := map[string]*InputSet{}
	proofs := map[string]*JobProof{}
	for _, e := range wf.Entries {
		name := e.Name
		if name == "" {
			name = e.Ref
		}
		jobInputs[name] = newInputSet()
		proofs[name] = &JobProof{}
	}
	if !opts.LLMOnly {
		eng := NewEngine(opts.RepoRoot)
		for _, e := range wf.Entries {
			name := e.Name
			if name == "" {
				name = e.Ref
			}
			var in *InputSet
			var proof *JobProof
			in, proof = eng.InferJob(cfg, e)
			jobInputs[name] = in
			proofs[name] = proof
		}
	} else if opts.LLMResult == "" {
		return nil, nil, fmt.Errorf("analyze: --llm-only requires --llm-result")
	}
	if opts.LLMResult != "" {
		if err := mergeLLM(opts.RepoRoot, opts.LLMResult, jobInputs); err != nil {
			return nil, nil, err
		}
	}
	var corrections []Correction
	if opts.History != "" {
		corrections, err = applyHistory(opts.RepoRoot, opts.History, jobInputs)
		if err != nil {
			return nil, nil, err
		}
	}
	rev := opts.Revision
	if rev == "" {
		rev = "inference"
	}
	m := &impactmap.Map{
		Version:   impactmap.Version,
		Revision:  rev,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Method:    MethodInference,
		Jobs:      map[string]*impactmap.Job{},
	}
	for _, e := range wf.Entries {
		name := e.Name
		if name == "" {
			name = e.Ref
		}
		in := jobInputs[name]
		complete := proofs[name].StaticComplete()
		hash, _ := jobHash(cfg, e)
		j := &impactmap.Job{
			Files:    in.sortedFiles(),
			Dirs:     in.sortedDirs(),
			Complete: complete,
			Nodes:    1,
			JobHash:  hash,
		}
		m.Jobs[name] = j
	}
	return m, corrections, nil
}

func mergeLLM(repoRoot, path string, jobs map[string]*InputSet) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var llm LLMResultFile
	if err := json.Unmarshal(data, &llm); err != nil {
		return fmt.Errorf("llm result: %w", err)
	}
	for name, spec := range llm.Jobs {
		in, ok := jobs[name]
		if !ok {
			in = newInputSet()
			jobs[name] = in
		}
		for _, g := range spec.Inputs {
			g = trimInputGlob(g)
			if hasGlobMeta(g) {
				in.addDir(g)
			} else if strings.HasSuffix(g, "/") {
				in.addDir(strings.TrimSuffix(g, "/"))
			} else {
				in.addPath(repoRoot, ".", g)
			}
		}
	}
	return nil
}

func trimInputGlob(g string) string {
	for _, sfx := range []string{"/**", "/*"} {
		if strings.HasSuffix(g, sfx) {
			g = strings.TrimSuffix(g, sfx)
		}
	}
	return g
}

func hasGlobMeta(g string) bool {
	return strings.Contains(g, "*") || strings.Contains(g, "?")
}

// applyHistory adds dirs for failed jobs where changes were outside inferred inputs.
func applyHistory(repoRoot, historyPath string, jobs map[string]*InputSet) ([]Correction, error) {
	f, err := os.Open(historyPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	history, err := backtest.DecodeHistory(f)
	if err != nil {
		return nil, err
	}
	var out []Correction
	for _, rec := range history {
		for _, j := range rec.Jobs {
			if j.Flaky || j.Status != "failed" {
				continue
			}
			in := jobs[j.Name]
			if in == nil {
				continue
			}
			for _, ch := range rec.Changed {
				if ch.Status == "D" {
					continue
				}
				if coversChange(in, ch.Path) {
					continue
				}
				unionPath(repoRoot, in, ch.Path, InputNote{Signal: SignalHistory, Detail: rec.Commit})
				unionPath(repoRoot, in, pathDir(ch.Path), InputNote{Signal: SignalHistory, Detail: rec.Commit})
				out = append(out, Correction{
					Job:      j.Name,
					Path:     ch.Path,
					Commit:   rec.Commit,
					Pipeline: rec.Commit,
				})
			}
		}
	}
	return out, nil
}

func coversChange(in *InputSet, path string) bool {
	if _, ok := in.files[path]; ok {
		return true
	}
	d := pathDir(path)
	if _, ok := in.dirs[d]; ok {
		return true
	}
	for dir := range in.dirs {
		if strings.HasPrefix(path, dir+"/") || dir == "." {
			return true
		}
	}
	return false
}

func pathDir(p string) string {
	d := path.Dir(p)
	if d == "" {
		return "."
	}
	return d
}
