package analyze

import (
	"fmt"
	"strings"
)

// JobSummaries maps each job name to a short input provenance line for plan reports.
func JobSummaries(rep *Report) map[string]string {
	if rep == nil {
		return nil
	}
	out := make(map[string]string, len(rep.Jobs))
	for _, j := range rep.Jobs {
		out[j.Name] = FormatInputSummary(j)
	}
	return out
}

// FormatInputSummary turns one analyze job report into "N paths from …".
func FormatInputSummary(j JobReport) string {
	n := 0
	for _, paths := range j.InputsBySignal {
		n += len(paths)
	}
	if n == 0 {
		return "0 paths"
	}
	keys := sortedKeysReport(j.InputsBySignal)
	var labels []string
	for _, k := range keys {
		labels = append(labels, shortenSignalLabel(k))
	}
	return fmt.Sprintf("%d paths from %s", n, strings.Join(labels, ", "))
}

func shortenSignalLabel(label string) string {
	switch {
	case strings.HasPrefix(label, SignalGoImports):
		if i := strings.Index(label, "("); i > 0 {
			return "go imports"
		}
		return "go imports"
	case strings.HasPrefix(label, SignalScript):
		if i := strings.Index(label, "("); i > 0 && strings.HasSuffix(label, ")") {
			return label[strings.Index(label, "(")+1 : len(label)-1]
		}
		return "scripts"
	case strings.HasPrefix(label, SignalRunCommand):
		return "run commands"
	case strings.HasPrefix(label, SignalAI):
		return "AI classifier"
	case strings.HasPrefix(label, SignalHistory):
		return "history corrections"
	default:
		return label
	}
}
