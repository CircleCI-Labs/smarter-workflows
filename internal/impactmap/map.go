// Package impactmap holds the impact map: for each job, the repo paths the
// planner treats as its inputs. It reads and writes v1 JSON and v2 gzip maps;
// it does not parse trace TSV.
package impactmap

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Version is the impact map format version map build writes (v2).
const Version = 2

// VersionV1 is the legacy JSON format without path tables.
const VersionV1 = 1

// On-disk names for cached maps.
const (
	MapFilenameV2    = "impact-map.json.gz"
	MapFilenameV1    = "impact-map.json"
	DefaultMapPath   = "/tmp/smarter/" + MapFilenameV2
	DefaultMapPathV1 = "/tmp/smarter/" + MapFilenameV1
)

// Bounds on hostile or accidental oversized maps (a parse error means a full run).
const (
	// v1: uncompressed JSON on disk.
	MaxMapFileBytes = 16 << 20

	// v2: gzip on disk and decompressed JSON payload.
	MaxMapCompressedBytes   = 64 << 20
	MaxMapDecompressedBytes = 256 << 20
	MaxDecompressRatio      = 32

	MaxMapJobs       = 4096
	MaxUniqueFiles   = 200_000
	MaxUniqueDirs    = 50_000
	MaxIndicesPerJob = 100_000
	MaxPathsPerJob   = MaxIndicesPerJob
	MaxJobNameLen    = 256
)

var gzipEpoch = time.Time{}

// DefaultMapMethod is the method label written into synthetic and benchmark maps.
const DefaultMapMethod = "static"

// Map is the impact map in memory (spec, "File formats").
type Map struct {
	Version   int             `json:"version"`
	Revision  string          `json:"revision"`
	CreatedAt string          `json:"created_at"`
	Method    string          `json:"method"`
	Jobs      map[string]*Job `json:"jobs"`

	// v2 path tables; set when a v2 map is parsed.
	fileTable []string
	dirTable  []string

	// Problems lists oddities Parse found and repaired by marking a job
	// incomplete, so the planner can report them. It is not part of the file.
	Problems []string `json:"-"`
}

// Job is what one job read. Files and Dirs are the union over the job's nodes,
// sorted. Complete is true only if every node was supported.
//
// Maps parsed from v2 keep indices into the map's tables until something asks
// for the path slices (catalog, tests).
type Job struct {
	Files    []string `json:"files"`
	Dirs     []string `json:"dirs"`
	Complete bool     `json:"complete"`
	Nodes    int      `json:"nodes"`

	fileIdx []int
	dirIdx  []int
	mapRef  *Map

	files map[string]struct{}
	dirs  map[string]struct{}
}

// ReadsFile reports whether the job read the repo-relative file p.
func (j *Job) ReadsFile(p string) bool {
	j.ensureFileSet()
	_, ok := j.files[p]
	return ok
}

// ListsDir reports whether the job listed the repo-relative directory d ("."
// is the repository root).
func (j *Job) ListsDir(d string) bool {
	j.ensureDirSet()
	_, ok := j.dirs[d]
	return ok
}

// FileCount is how many files the job read.
func (j *Job) FileCount() int {
	if len(j.fileIdx) > 0 {
		return len(j.fileIdx)
	}
	return len(j.Files)
}

// DirCount is how many directories the job listed.
func (j *Job) DirCount() int {
	if len(j.dirIdx) > 0 {
		return len(j.dirIdx)
	}
	return len(j.Dirs)
}

// FilePaths returns the sorted file paths the job read.
func (j *Job) FilePaths() []string {
	if len(j.Files) > 0 {
		return j.Files
	}
	if j.mapRef == nil || len(j.fileIdx) == 0 {
		return nil
	}
	return indicesToPaths(j.fileIdx, j.mapRef.fileTable)
}

// DirPaths returns the sorted directories the job listed.
func (j *Job) DirPaths() []string {
	if len(j.Dirs) > 0 {
		return j.Dirs
	}
	if j.mapRef == nil || len(j.dirIdx) == 0 {
		return nil
	}
	return indicesToPaths(j.dirIdx, j.mapRef.dirTable)
}

func (j *Job) ensureFileSet() {
	if j.files != nil {
		return
	}
	if len(j.fileIdx) > 0 && j.mapRef != nil {
		j.files = make(map[string]struct{}, len(j.fileIdx))
		for _, i := range j.fileIdx {
			j.files[j.mapRef.fileTable[i]] = struct{}{}
		}
		return
	}
	j.files = toSet(j.Files)
}

func (j *Job) ensureDirSet() {
	if j.dirs != nil {
		return
	}
	if len(j.dirIdx) > 0 && j.mapRef != nil {
		j.dirs = make(map[string]struct{}, len(j.dirIdx))
		for _, i := range j.dirIdx {
			j.dirs[j.mapRef.dirTable[i]] = struct{}{}
		}
		return
	}
	j.dirs = toSet(j.Dirs)
}

func toSet(list []string) map[string]struct{} {
	s := make(map[string]struct{}, len(list))
	for _, v := range list {
		s[v] = struct{}{}
	}
	return s
}

// LoadCandidates returns paths to try when loading a map (v2 primary, v1 legacy).
func LoadCandidates(path string) []string {
	if path == "" {
		return nil
	}
	switch {
	case strings.HasSuffix(path, ".json.gz"):
		legacy := strings.TrimSuffix(path, ".json.gz") + ".json"
		return []string{path, legacy}
	case strings.HasSuffix(path, ".json"):
		modern := strings.TrimSuffix(path, ".json") + ".json.gz"
		return []string{modern, path}
	default:
		return []string{path}
	}
}

// Parse decodes and validates an impact map (v1 JSON or v2 JSON, optionally gzip).
func Parse(data []byte) (*Map, error) {
	raw := data
	if isGzip(data) {
		var err error
		raw, err = gunzipData(data)
		if err != nil {
			return nil, err
		}
	} else if len(data) > MaxMapFileBytes {
		return nil, fmt.Errorf("impact map: file is %d bytes, over the limit of %d", len(data), MaxMapFileBytes)
	}
	var peek struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(raw, &peek); err != nil {
		return nil, fmt.Errorf("impact map: %w", err)
	}
	switch peek.Version {
	case VersionV1:
		return parseV1(raw)
	case Version:
		return parseV2(raw)
	default:
		return nil, fmt.Errorf("impact map: unsupported version %d (want %d or %d)", peek.Version, VersionV1, Version)
	}
}

func parseV1(raw []byte) (*Map, error) {
	var m Map
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("impact map: %w", err)
	}
	if m.Version != VersionV1 {
		return nil, fmt.Errorf("impact map: unsupported version %d (want %d)", m.Version, VersionV1)
	}
	if m.Jobs == nil {
		m.Jobs = map[string]*Job{}
	}
	if err := validateMapBoundsV1(&m); err != nil {
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
	limit := int64(MaxMapFileBytes)
	if strings.HasSuffix(filePath, ".gz") || info.Size() >= 2 {
		limit = int64(MaxMapCompressedBytes)
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("impact map: file is %d bytes, over the limit of %d", info.Size(), limit)
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

func validateMapBoundsV1(m *Map) error {
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

// Marshal encodes the map as v2 JSON, gzip-compressed (spec v2).
func (m *Map) Marshal() ([]byte, error) {
	raw, err := marshalV2JSON(m)
	if err != nil {
		return nil, err
	}
	return gzipData(raw)
}

// MarshalV1 encodes the map as indented v1 JSON with a trailing newline.
func (m *Map) MarshalV1() ([]byte, error) {
	out := *m
	out.Version = VersionV1
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// MarshalJSONCanonical returns indented v2 JSON before compression, for tests.
func (m *Map) MarshalJSONCanonical() ([]byte, error) {
	raw, err := marshalV2JSON(m)
	if err != nil {
		return nil, err
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		return nil, err
	}
	pretty.WriteByte('\n')
	return pretty.Bytes(), nil
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

// ResolveMapPath returns the first existing candidate path, or the primary.
func ResolveMapPath(primary string) string {
	for _, p := range LoadCandidates(primary) {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return primary
}

// MapDir is the directory portion of DefaultMapPath.
func MapDir() string {
	return filepath.Dir(DefaultMapPath)
}
