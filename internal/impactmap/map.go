// Package impactmap holds the impact map: for each job, the repo files it read
// and the directories it listed during the last traced full run (ADR 3). It
// parses the trace TSV files the collector writes, merges them into one map,
// and loads the map for the planner.
package impactmap

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
)

// Version is the only map format version this package reads and writes.
const Version = 1

// Bounds on hostile or accidental oversized maps (a parse error means a full run).
const (
	MaxMapFileBytes = 16 << 20
	MaxMapJobs      = 4096
	MaxPathsPerJob  = 100_000
	MaxJobNameLen   = 256
)

// MethodATime is the only trace method v0.1 knows: file access times.
const MethodATime = "atime"

// Map is the impact map JSON document (spec, "File formats").
type Map struct {
	Version   int             `json:"version"`
	Revision  string          `json:"revision"`
	CreatedAt string          `json:"created_at"`
	Method    string          `json:"method"`
	Jobs      map[string]*Job `json:"jobs"`

	// Problems lists oddities Parse found and repaired by marking a job
	// incomplete, so the planner can report them. It is not part of the file.
	Problems []string `json:"-"`
}

// Job is what one job read. Files and Dirs are the union over the job's nodes,
// sorted. Complete is true only if every node was supported.
type Job struct {
	Files    []string `json:"files"`
	Dirs     []string `json:"dirs"`
	Complete bool     `json:"complete"`
	Nodes    int      `json:"nodes"`

	files map[string]struct{}
	dirs  map[string]struct{}
}

// ReadsFile reports whether the job read the repo-relative file p.
func (j *Job) ReadsFile(p string) bool {
	if j.files == nil {
		j.files = toSet(j.Files)
	}
	_, ok := j.files[p]
	return ok
}

// ListsDir reports whether the job listed the repo-relative directory d ("."
// is the repository root).
func (j *Job) ListsDir(d string) bool {
	if j.dirs == nil {
		j.dirs = toSet(j.Dirs)
	}
	_, ok := j.dirs[d]
	return ok
}

func toSet(list []string) map[string]struct{} {
	s := make(map[string]struct{}, len(list))
	for _, v := range list {
		s[v] = struct{}{}
	}
	return s
}

// Parse decodes and validates an impact map. A wrong version or malformed JSON
// is an error, which the planner treats as "no usable map". A job with an
// unusable path is kept but marked incomplete, so that it always runs.
func Parse(data []byte) (*Map, error) {
	var m Map
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("impact map: %w", err)
	}
	if m.Version != Version {
		return nil, fmt.Errorf("impact map: unsupported version %d (want %d)", m.Version, Version)
	}
	if m.Jobs == nil {
		m.Jobs = map[string]*Job{}
	}
	if err := validateMapBounds(&m); err != nil {
		return nil, err
	}
	for _, name := range sortedKeys(m.Jobs) {
		j := m.Jobs[name]
		if j == nil {
			j = &Job{}
			m.Jobs[name] = j
		}
		files, bad := cleanAll(j.Files, false)
		dirs, badDirs := cleanAll(j.Dirs, true)
		if bad > 0 || badDirs > 0 {
			j.Complete = false
			m.Problems = append(m.Problems, fmt.Sprintf("impact map: job %q has %d unusable paths and is treated as incomplete", name, bad+badDirs))
		}
		j.Files, j.Dirs = files, dirs
		j.files, j.dirs = nil, nil
	}
	return &m, nil
}

// Load reads and parses the map at filePath.
func Load(filePath string) (*Map, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return nil, err
	}
	if info.Size() > MaxMapFileBytes {
		return nil, fmt.Errorf("impact map: file is %d bytes, over the limit of %d", info.Size(), MaxMapFileBytes)
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

func validateMapBounds(m *Map) error {
	if len(m.Jobs) > MaxMapJobs {
		return fmt.Errorf("impact map: %d jobs, over the limit of %d", len(m.Jobs), MaxMapJobs)
	}
	for name, j := range m.Jobs {
		if len(name) > MaxJobNameLen {
			return fmt.Errorf("impact map: job name is %d characters, over the limit of %d", len(name), MaxJobNameLen)
		}
		if j == nil {
			continue
		}
		n := len(j.Files) + len(j.Dirs)
		if n > MaxPathsPerJob {
			return fmt.Errorf("impact map: job %q has %d paths, over the limit of %d", name, n, MaxPathsPerJob)
		}
	}
	return nil
}

// Marshal encodes the map as indented JSON with a trailing newline.
func (m *Map) Marshal() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// cleanAll normalises a path list, dropping and counting the unusable ones.
func cleanAll(list []string, dirs bool) ([]string, int) {
	out := make([]string, 0, len(list))
	bad := 0
	seen := make(map[string]struct{}, len(list))
	for _, p := range list {
		c, err := CleanPath(p, dirs)
		if err != nil {
			bad++
			continue
		}
		if _, dup := seen[c]; dup {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	sort.Strings(out)
	return out, bad
}

// CleanPath normalises a repo-relative path: no "./" prefix, no trailing or
// doubled slashes. It rejects absolute paths, paths that leave the repository,
// and empty paths. When dir is true, "." (the root) is accepted.
func CleanPath(p string, dir bool) (string, error) {
	if p == "" || strings.ContainsRune(p, 0) {
		return "", fmt.Errorf("unusable path %q", p)
	}
	if strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("absolute path %q", p)
	}
	c := path.Clean(p)
	if c == ".." || strings.HasPrefix(c, "../") {
		return "", fmt.Errorf("path %q leaves the repository", p)
	}
	if c == "." && !dir {
		return "", fmt.Errorf("path %q is not a file", p)
	}
	return c, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
