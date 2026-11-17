package analyze

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CircleCI-Labs/smarter-workflows/internal/impactmap"
)

func TestSelfTruthCoverage(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	truth := loadTruth(t, filepath.Join("testdata", "self-truth.json"))
	m, _, _, err := Run(t.Context(), RunOptions{
		RepoRoot:   root,
		ConfigPath: filepath.Join(root, ".circleci/continue.yml"),
		Workflows:  []string{"ci"},
		AIMode:     AIModeOff,
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, tj := range truth.Jobs {
		if tj == nil || !tj.Complete {
			continue
		}
		ij := m.Jobs[name]
		if ij == nil || !ij.Complete {
			continue
		}
		var missing []string
		for _, f := range tj.FilePaths() {
			if truthFileExcluded(f) {
				continue
			}
			if _, err := os.Stat(filepath.Join(root, f)); err != nil {
				continue
			}
			if !truthCovered(ij, f) {
				missing = append(missing, f)
			}
		}
		if len(missing) > 0 {
			t.Fatalf("job %q complete but missing truth files %v", name, missing)
		}
	}
}

func TestDemoTruthCoverage(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	truth := loadTruth(t, filepath.Join("testdata", "demo-truth.json"))
	m, _, _, err := Run(t.Context(), RunOptions{
		RepoRoot:   root,
		ConfigPath: filepath.Join(root, "examples/demo/.circleci/continue.yml"),
		Workflows:  []string{"demo"},
		AIMode:     AIModeOff,
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, tj := range truth.Jobs {
		if tj == nil || !tj.Complete {
			continue
		}
		ij := m.Jobs[name]
		if ij == nil || !ij.Complete {
			continue
		}
		var missing []string
		for _, f := range tj.FilePaths() {
			if !truthCovered(ij, f) {
				missing = append(missing, f)
			}
		}
		if len(missing) > 0 {
			t.Fatalf("job %q complete but missing truth files %v", name, missing)
		}
	}
}

func loadTruth(t *testing.T, path string) *impactmap.Map {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	m, err := impactmap.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// truthFileExcluded lists git metadata paths omitted from self-repo acceptance
// (build jobs may touch .gitignore without affecting product inputs).
func truthFileExcluded(file string) bool {
	base := filepath.Base(file)
	return base == ".gitignore"
}

func truthCovered(j *impactmap.Job, file string) bool {
	if j.ReadsFile(file) {
		return true
	}
	for _, d := range j.DirPaths() {
		if file == d || strings.HasPrefix(file, d+"/") {
			return true
		}
	}
	return false
}

func TestDemoAnalyzeTruthAcceptanceReport(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	truth := loadTruth(t, filepath.Join("testdata", "demo-truth.json"))
	m, _, _, err := Run(t.Context(), RunOptions{
		RepoRoot:   root,
		ConfigPath: filepath.Join(root, "examples/demo/.circleci/continue.yml"),
		Workflows:  []string{"demo"},
		AIMode:     AIModeOff,
	})
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		Complete      bool `json:"complete"`
		TruthComplete bool `json:"truth_complete"`
		Covered       int  `json:"covered"`
		TruthFiles    int  `json:"truth_files"`
	}
	report := map[string]row{}
	for name, tj := range truth.Jobs {
		ij := m.Jobs[name]
		r := row{TruthComplete: tj != nil && tj.Complete, TruthFiles: len(tj.FilePaths())}
		if ij != nil {
			r.Complete = ij.Complete
			if tj != nil {
				for _, f := range tj.FilePaths() {
					if truthCovered(ij, f) {
						r.Covered++
					}
				}
			}
		}
		report[name] = r
	}
	b, _ := json.MarshalIndent(report, "", "  ")
	t.Logf("demo truth coverage:\n%s", b)
}
