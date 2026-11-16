package analyze

import (
	"fmt"
	"path"
	"sort"

	"github.com/CircleCI-Labs/smarter-workflows/internal/impactmap"
)

// JobMetrics is per-job recall and precision against a traced map.
type JobMetrics struct {
	Job       string  `json:"job"`
	Traced    int     `json:"traced_files"`
	Covered   int     `json:"covered_files"`
	Inferred  int     `json:"inferred_files"`
	Recall    float64 `json:"recall"`
	Precision float64 `json:"precision"`
}

// EvalReport compares inferred and truth impact maps.
type EvalReport struct {
	Jobs           []JobMetrics `json:"jobs"`
	WeightedRecall float64      `json:"weighted_recall"`
	WeightedPrec   float64      `json:"weighted_precision"`
	MacroRecall    float64      `json:"macro_recall"`
	MacroPrecision float64      `json:"macro_precision"`
}

// Evaluate compares inferred against truth (traced) maps job by job. When only is
// non-empty, only those job names are scored (they must exist in both maps).
func Evaluate(inferred, truth *impactmap.Map, only []string) EvalReport {
	names := map[string]struct{}{}
	if len(only) > 0 {
		for _, n := range only {
			if _, ok := truth.Jobs[n]; !ok {
				continue
			}
			if _, ok := inferred.Jobs[n]; !ok {
				continue
			}
			names[n] = struct{}{}
		}
	} else {
		for n := range inferred.Jobs {
			if _, ok := truth.Jobs[n]; ok {
				names[n] = struct{}{}
			}
		}
	}
	var jobs []JobMetrics
	var tracedTotal, coveredTotal, inferredTotal, hitTotal int
	for _, name := range sortedKeys(names) {
		tj := truth.Jobs[name]
		ij := inferred.Jobs[name]
		m := JobMetrics{Job: name}
		var traced []string
		if tj != nil {
			traced = tj.FilePaths()
		}
		m.Traced = len(traced)
		var inferredFiles []string
		if ij != nil {
			inferredFiles = ij.FilePaths()
		}
		m.Inferred = len(inferredFiles)
		truthSet := fileSet(tj)
		for _, f := range traced {
			if mapCovers(ij, f) {
				m.Covered++
			}
		}
		for _, f := range inferredFiles {
			if _, ok := truthSet[f]; ok {
				hitTotal++
			}
		}
		if m.Traced > 0 {
			m.Recall = float64(m.Covered) / float64(m.Traced)
		}
		if m.Inferred > 0 {
			m.Precision = float64(countInTruth(inferredFiles, truthSet)) / float64(m.Inferred)
		}
		jobs = append(jobs, m)
		tracedTotal += m.Traced
		coveredTotal += m.Covered
		inferredTotal += m.Inferred
	}
	var macroR, macroP float64
	var nR, nP int
	for _, m := range jobs {
		if m.Traced > 0 {
			macroR += m.Recall
			nR++
		}
		if m.Inferred > 0 {
			macroP += m.Precision
			nP++
		}
	}
	rep := EvalReport{Jobs: jobs}
	if tracedTotal > 0 {
		rep.WeightedRecall = float64(coveredTotal) / float64(tracedTotal)
	}
	if inferredTotal > 0 {
		rep.WeightedPrec = float64(hitTotal) / float64(inferredTotal)
	}
	if nR > 0 {
		rep.MacroRecall = macroR / float64(nR)
	}
	if nP > 0 {
		rep.MacroPrecision = macroP / float64(nP)
	}
	return rep
}

func mapCovers(m *impactmap.Job, file string) bool {
	if m == nil {
		return false
	}
	if m.ReadsFile(file) {
		return true
	}
	d := path.Dir(file)
	for {
		if m.ListsDir(d) {
			return true
		}
		if d == "." {
			break
		}
		d = path.Dir(d)
	}
	return false
}

func fileSet(j *impactmap.Job) map[string]struct{} {
	s := map[string]struct{}{}
	if j == nil {
		return s
	}
	for _, f := range j.FilePaths() {
		s[f] = struct{}{}
	}
	return s
}

func countInTruth(files []string, truth map[string]struct{}) int {
	n := 0
	for _, f := range files {
		if _, ok := truth[f]; ok {
			n++
		}
	}
	return n
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// FormatEval writes a human-readable evaluation summary.
func FormatEval(r EvalReport) string {
	var b []byte
	b = append(b, fmt.Sprintf("weighted recall %.1f%% (%d jobs)\n", r.WeightedRecall*100, len(r.Jobs))...)
	b = append(b, fmt.Sprintf("weighted precision %.1f%%\n", r.WeightedPrec*100)...)
	for _, m := range r.Jobs {
		b = append(b, fmt.Sprintf("  %s: recall %.0f%% (%d/%d) precision %.0f%%\n",
			m.Job, m.Recall*100, m.Covered, m.Traced, m.Precision*100)...)
	}
	return string(b)
}
