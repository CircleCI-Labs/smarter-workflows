package analyze

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

const learnedVersion = 1

// Learned is the versioned cache of slow-to-recompute analyze outputs.
type Learned struct {
	Version   int                    `json:"version"`
	Revision  string                 `json:"revision"`
	CreatedAt string                 `json:"created_at"`
	Model     string                 `json:"model,omitempty"`
	Jobs      map[string]*LearnedJob `json:"jobs"`
}

// LearnedJob holds per-job AI output, corrections, and flaky hint.
type LearnedJob struct {
	JobHash     string              `json:"job_hash"`
	AI          *LearnedAI          `json:"ai"`
	Corrections []LearnedCorrection `json:"corrections,omitempty"`
	Flaky       bool                `json:"flaky,omitempty"`
}

// LearnedAI is stored AI classifier output for one job.
type LearnedAI struct {
	Inputs     []string `json:"inputs"`
	Confidence float64  `json:"confidence"`
	Why        string   `json:"why"`
}

// LearnedCorrection is one history-mined path addition.
type LearnedCorrection struct {
	Path      string `json:"path"`
	Commit    string `json:"commit"`
	LearnedAt string `json:"learned_at"`
}

// TryLoadLearned loads a learned layer. A missing or invalid file yields a
// warning string and no error so plan can continue without it.
func TryLoadLearned(path string) (*Learned, string, error) {
	if path == "" {
		return nil, "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "learned layer not found at " + path, nil
		}
		return nil, "", err
	}
	var l Learned
	if err := json.Unmarshal(data, &l); err != nil {
		return nil, "learned layer at " + path + " is not valid JSON", nil
	}
	if l.Jobs == nil {
		l.Jobs = map[string]*LearnedJob{}
	}
	return &l, "", nil
}

func loadLearned(path string) (*Learned, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var l Learned
	if err := json.Unmarshal(data, &l); err != nil {
		return nil, fmt.Errorf("learned: %w", err)
	}
	if l.Jobs == nil {
		l.Jobs = map[string]*LearnedJob{}
	}
	return &l, nil
}

func writeLearned(path string, l *Learned) error {
	if path == "" || l == nil {
		return nil
	}
	l.Version = learnedVersion
	if l.CreatedAt == "" {
		l.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o640)
}

func mergeLearnedForward(prev *Learned, fresh *Learned) {
	if prev == nil || fresh == nil {
		return
	}
	for name, job := range prev.Jobs {
		if job == nil {
			continue
		}
		dst, ok := fresh.Jobs[name]
		if !ok {
			fresh.Jobs[name] = job
			continue
		}
		dst.Corrections = dedupeCorrections(append(dst.Corrections, job.Corrections...))
		if dst.AI == nil && job.AI != nil {
			dst.AI = job.AI
		}
	}
}

func dedupeCorrections(in []LearnedCorrection) []LearnedCorrection {
	seen := map[string]struct{}{}
	var out []LearnedCorrection
	for _, c := range in {
		key := c.Path + "\x00" + c.Commit
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, c)
	}
	return out
}
