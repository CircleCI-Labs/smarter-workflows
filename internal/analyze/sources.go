package analyze

import "fmt"

// Signal names for analyze report provenance.
const (
	SignalRunCommand    = "run_command"
	SignalScript        = "followed_script"
	SignalGoImports     = "go_imports"
	SignalDockerCopy    = "dockerfile_copy"
	SignalOrbParam      = "orb_parameter"
	SignalCacheChecksum = "cache_checksum"
	SignalWorkingDir    = "working_directory"
	SignalWorkspace     = "workspace_upstream"
	SignalPathFilter    = "path_filtering"
	SignalAI            = "ai_classifier"
	SignalHistory       = "history_correction"
)

// InputNote records which config signal contributed a path.
type InputNote struct {
	Signal string `json:"signal"`
	Detail string `json:"detail,omitempty"`
}

type labeledInput struct {
	path  string
	isDir bool
	notes []InputNote
}

func (s *InputSet) note(path string, isDir bool, n InputNote) {
	key := path
	if isDir {
		key = "dir:" + path
	}
	s.notes[key] = appendUniqueNote(s.notes[key], n)
}

func appendUniqueNote(list []InputNote, n InputNote) []InputNote {
	for _, have := range list {
		if have.Signal == n.Signal && have.Detail == n.Detail {
			return list
		}
	}
	return append(list, n)
}

func (s *InputSet) noteGoImports(n int) {
	s.goPkg = n
}

func (s *InputSet) labeledInputs() []labeledInput {
	var out []labeledInput
	for f := range s.files {
		li := labeledInput{path: f, isDir: false, notes: s.notes[f]}
		if s.goPkg > 0 && len(li.notes) == 0 {
			li.notes = []InputNote{{Signal: SignalGoImports, Detail: fmtPkgCount(s.goPkg)}}
		}
		out = append(out, li)
	}
	for d := range s.dirs {
		key := "dir:" + d
		li := labeledInput{path: d, isDir: true, notes: s.notes[key]}
		if s.goPkg > 0 && len(li.notes) == 0 {
			li.notes = []InputNote{{Signal: SignalGoImports, Detail: fmtPkgCount(s.goPkg)}}
		}
		out = append(out, li)
	}
	return out
}

func fmtPkgCount(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d packages", n)
}
