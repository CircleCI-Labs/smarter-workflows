// Package planner decides which jobs of a workflow a change can reach, and
// writes the continuation config that carries the decision out (spec, "The
// algorithm"). It never asks git or the filesystem directly: the caller hands
// it the config, the impact map and a Repo.
package planner

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// Mode says what the planner does with its plan.
type Mode string

const (
	// ModeShadow plans and reports, but keeps every job.
	ModeShadow Mode = "shadow"
	// ModeSelect removes the jobs the plan skips.
	ModeSelect Mode = "select"
)

// TraceMode says whether to add the trace steps.
type TraceMode string

const (
	// TraceAuto traces full runs on the default branch.
	TraceAuto TraceMode = "auto"
	// TraceOn always traces.
	TraceOn TraceMode = "on"
	// TraceOff never traces.
	TraceOff TraceMode = "off"
)

const (
	// DefaultGateJob is the name of the gate job.
	DefaultGateJob = "smarter-gate"
	// DefaultMapImage is the image of the impact-map job: a pinned monthly
	// cimg/base tag. The gate is a no-op job and has no image.
	DefaultMapImage = "cimg/base:2026.11"
	// DefaultFullRunPaths makes a change to the pipeline definition a full run.
	DefaultFullRunPaths = ".circleci/**"
	// ImpactMapJob is the name of the job that merges the traces into the map.
	ImpactMapJob = "smarter-impact-map"
	// DefaultMapKeyPrefix starts the cache key the map job saves the impact
	// map under: <prefix>-<checksum of the commit>.
	DefaultMapKeyPrefix = "smarter-impact-v1"
	// TraceDir is where the traced jobs leave their traces (in the workspace).
	TraceDir = "/tmp/smarter"
)

// Options are the planner settings: the `plan` flags (spec, CLI contract).
type Options struct {
	Mode     Mode
	Workflow string

	Branch        string
	Tag           string
	DefaultBranch string
	TriggerType   string

	// Base and Head are commit SHAs. Base is empty when there is no diff base,
	// and NoBaseReason then says why.
	Base         string
	Head         string
	NoBaseReason string

	ForceFull    bool
	Mandatory    []string
	FullRunPaths []string
	ExtraInputs  map[string][]string
	IgnorePaths  []string

	Trace        TraceMode
	TraceOrbJobs bool
	// TraceReset adds the atime reset step after checkout in traced jobs
	// (spec step 8). It is on by default, because checkout itself reads every
	// file; turn it off for tests.
	TraceReset bool

	GateJob string
	// MapImage is the docker image of the impact-map job (step 8.3).
	MapImage string

	// MapKeyPrefix starts the cache key of the saved impact map. Pipelines of
	// one project share its cache, so each one needs its own prefix, and its
	// setup job restores the map with the same one.
	MapKeyPrefix string

	// Version is the planner's own version, written into the map job.
	Version string
	// BinPath is where the map job finds the planner binary: the path it ran
	// from in the setup job, which the install step caches.
	BinPath string
}

// DefaultOptions returns the defaults of the spec's CLI table.
func DefaultOptions() Options {
	return Options{
		Mode:          ModeShadow,
		DefaultBranch: "main",
		FullRunPaths:  []string{DefaultFullRunPaths},
		Trace:         TraceAuto,
		TraceReset:    true,
		GateJob:       DefaultGateJob,
		MapImage:      DefaultMapImage,
		MapKeyPrefix:  DefaultMapKeyPrefix,
		Version:       "dev",
		BinPath:       "circleci-smarter",
	}
}

var jobName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// mapKeyPrefixRE is what a cache key prefix may look like: the characters of a
// job name, so it needs no quoting and cannot hold a `{{ }}` template.
var mapKeyPrefixRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// Validate checks the settings. Its errors are usage errors.
func (o Options) Validate() error {
	switch o.Mode {
	case ModeShadow, ModeSelect:
	default:
		return fmt.Errorf("mode must be shadow or select, not %q", o.Mode)
	}
	switch o.Trace {
	case TraceAuto, TraceOn, TraceOff:
	default:
		return fmt.Errorf("trace must be auto, on or off, not %q", o.Trace)
	}
	if !jobName.MatchString(o.GateJob) {
		return fmt.Errorf("gate job name %q is not usable", o.GateJob)
	}
	if strings.TrimSpace(o.MapImage) == "" || strings.ContainsAny(o.MapImage, " \t\r\n") {
		return fmt.Errorf("map image %q is not usable", o.MapImage)
	}
	if !mapKeyPrefixRE.MatchString(o.MapKeyPrefix) {
		return fmt.Errorf("map key prefix %q is not usable: start with a letter or digit, then use letters, digits, . _ or -", o.MapKeyPrefix)
	}
	if err := validateGlobs(o.FullRunPaths); err != nil {
		return fmt.Errorf("full-run paths: %w", err)
	}
	if err := validateGlobs(o.IgnorePaths); err != nil {
		return fmt.Errorf("ignore paths: %w", err)
	}
	for job, globs := range o.ExtraInputs {
		if err := validateGlobs(globs); err != nil {
			return fmt.Errorf("extra inputs of %q: %w", job, err)
		}
	}
	return nil
}

func validateGlobs(globs []string) error {
	for _, g := range globs {
		if !doublestar.ValidatePattern(normGlob(g)) {
			return fmt.Errorf("bad glob %q", g)
		}
	}
	return nil
}

// normGlob trims blanks and a leading "./".
func normGlob(g string) string {
	g = strings.TrimSpace(g)
	for strings.HasPrefix(g, "./") {
		g = strings.TrimPrefix(g, "./")
	}
	return g
}

// matchAny returns the first glob that matches the repo-relative path.
func matchAny(globs []string, path string) (string, bool) {
	for _, g := range globs {
		if ok, err := doublestar.Match(normGlob(g), path); err == nil && ok {
			return g, true
		}
	}
	return "", false
}

// SplitList splits a comma-separated list. Commas inside {braces} belong to
// the glob, so "a/**/*.{go,mod},b" is two items. Blank items are dropped.
func SplitList(s string) []string {
	var out []string
	depth, start := 0, 0
	flush := func(end int) {
		if item := strings.TrimSpace(s[start:end]); item != "" {
			out = append(out, item)
		}
	}
	for i, r := range s {
		switch r {
		case '{':
			depth++
		case '}':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				flush(i)
				start = i + 1
			}
		}
	}
	flush(len(s))
	return out
}

// ParseExtraInputs parses "job=glob|glob,job2=glob" into declared inputs per
// job.
func ParseExtraInputs(s string) (map[string][]string, error) {
	out := map[string][]string{}
	for _, item := range SplitList(s) {
		job, globs, ok := strings.Cut(item, "=")
		job = strings.TrimSpace(job)
		if !ok || job == "" {
			return nil, fmt.Errorf("extra input %q: want job=glob|glob", item)
		}
		var list []string
		for _, g := range strings.Split(globs, "|") {
			if g = strings.TrimSpace(g); g != "" {
				list = append(list, g)
			}
		}
		if len(list) == 0 {
			return nil, fmt.Errorf("extra input %q: no globs", item)
		}
		out[job] = append(out[job], list...)
	}
	return out, nil
}
