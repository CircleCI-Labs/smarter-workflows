package planner

import (
	"regexp"
	"strings"

	"github.com/CircleCI-Labs/smarter-workflows/internal/config"
)

// tri is a three-valued answer: the planner says "unknown" when it cannot
// tell, and then leaves the job out of the gate.
type tri int

const (
	triNo tri = iota
	triUnknown
	triYes
)

// filterVerdict is step 5 for one entry on its own: does CircleCI run it for
// this branch or tag, judging by its filters alone?
//
// CircleCI semantics (config reference, "filters"):
//   - a value is an exact name, or /regex/ that must match the whole name;
//   - for branches, no filter means every branch; `only` must match and
//     `ignore` must not; a filter with neither key matches nothing;
//   - jobs do not run for a tag unless a tags filter says so;
//   - an expression string, or anything else unreadable, is unknown.
func filterVerdict(f config.Filters, branch, tag string) tri {
	if f.Expr {
		return triUnknown
	}
	switch {
	case branch != "" && tag != "":
		return triUnknown // both set: CircleCI's precedence rules apply; do not guess
	case tag != "":
		if f.Tags == nil {
			return triNo
		}
		return refVerdict(f.Tags, tag)
	case branch != "":
		if f.Branches == nil {
			return triYes
		}
		return refVerdict(f.Branches, branch)
	}
	return triUnknown
}

func refVerdict(r *config.RefFilter, name string) tri {
	if r.Dynamic {
		return triUnknown
	}
	if !r.HasOnly && !r.HasIgnore {
		return triNo
	}
	only, ignore := true, false
	if r.HasOnly {
		m, ok := matchRef(r.Only, name)
		if !ok {
			return triUnknown
		}
		only = m
	}
	if r.HasIgnore {
		m, ok := matchRef(r.Ignore, name)
		if !ok {
			return triUnknown
		}
		ignore = m
	}
	switch {
	case r.HasOnly && r.HasIgnore && only && ignore:
		return triUnknown // matches both lists: leave it to CircleCI
	case !only || ignore:
		return triNo
	}
	return triYes
}

// matchRef reports whether name matches any pattern. ok is false when a
// pattern is a regular expression that Go cannot read (CircleCI uses Java's
// dialect).
func matchRef(patterns []string, name string) (match, ok bool) {
	for _, p := range patterns {
		if len(p) >= 2 && strings.HasPrefix(p, "/") && strings.HasSuffix(p, "/") {
			re, err := regexp.Compile(`^(?:` + p[1:len(p)-1] + `)$`)
			if err != nil {
				return false, false
			}
			if re.MatchString(name) {
				return true, true
			}
			continue
		}
		if p == name {
			return true, true
		}
	}
	return false, true
}

// willRun is step 5 for the whole workflow: the verdict of every entry.
//
// It follows CircleCI's reference (`requires`): a requirement on a job that does
// not execute, a filter leaving it out for example, is ignored; but a job whose
// requirements are all filtered out does not execute either. So an entry runs
// only if its own filters allow it and, when it has requirements, at least one
// of them runs. That holds all the way up: the verdict of a requirement is its
// own.
func willRun(entries []*config.Entry, deps [][]int, unresolved []bool, branch, tag string) []tri {
	out := make([]tri, len(entries))
	done := make([]bool, len(entries))
	visiting := make([]bool, len(entries))
	var eval func(i int) tri
	eval = func(i int) tri {
		if done[i] {
			return out[i]
		}
		if visiting[i] { // a dependency cycle: the config is broken, do not guess
			return triUnknown
		}
		visiting[i] = true
		defer func() { visiting[i] = false }()

		e := entries[i]
		v := filterVerdict(e.Filters, branch, tag)
		if v != triNo && len(e.Requires) > 0 {
			switch {
			case e.StatusRequires || unresolved[i]:
				// A requirement on a status ("build: failed") can start the job
				// when the other job did not succeed, and an unresolved name could
				// be anything. Either way the job may or may not run.
				v = min(v, triUnknown)
			default:
				best := triNo
				for _, d := range deps[i] {
					best = max(best, eval(d))
				}
				v = min(v, best)
			}
		}
		out[i], done[i] = v, true
		return v
	}
	for i := range entries {
		eval(i)
	}
	return out
}
