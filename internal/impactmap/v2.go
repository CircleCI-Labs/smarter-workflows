package impactmap

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

type jobWireV2 struct {
	FileIdx  string `json:"file_idx,omitempty"`
	DirIdx   string `json:"dir_idx,omitempty"`
	Complete bool   `json:"complete"`
	Nodes    int    `json:"nodes"`
}

type wireV2 struct {
	Version   int                   `json:"version"`
	Revision  string                `json:"revision"`
	CreatedAt string                `json:"created_at"`
	Method    string                `json:"method"`
	Files     []string              `json:"files"`
	Dirs      []string              `json:"dirs"`
	Jobs      map[string]*jobWireV2 `json:"jobs"`
}

func parseV2(raw []byte) (*Map, error) {
	var w wireV2
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, fmt.Errorf("impact map: %w", err)
	}
	if w.Version != Version {
		return nil, fmt.Errorf("impact map: unsupported version %d (want %d)", w.Version, Version)
	}
	if w.Jobs == nil {
		w.Jobs = map[string]*jobWireV2{}
	}
	if err := validateV2Tables(&w); err != nil {
		return nil, err
	}
	m := &Map{
		Version:   Version,
		Revision:  w.Revision,
		CreatedAt: w.CreatedAt,
		Method:    w.Method,
		Jobs:      make(map[string]*Job, len(w.Jobs)),
		fileTable: append([]string(nil), w.Files...),
		dirTable:  append([]string(nil), w.Dirs...),
	}
	if len(w.Jobs) > MaxMapJobs {
		return nil, fmt.Errorf("impact map: %d jobs, over the limit of %d", len(w.Jobs), MaxMapJobs)
	}
	for name, jw := range w.Jobs {
		if len(name) > MaxJobNameLen {
			return nil, fmt.Errorf("impact map: job name is %d characters, over the limit of %d", len(name), MaxJobNameLen)
		}
		j := &Job{mapRef: m}
		if jw != nil {
			j.Complete = jw.Complete
			j.Nodes = jw.Nodes
			var err error
			j.fileIdx, err = decodeIndexDeltas(jw.FileIdx, len(w.Files), MaxIndicesPerJob)
			if err != nil {
				return nil, fmt.Errorf("impact map: job %q files: %w", name, err)
			}
			j.dirIdx, err = decodeIndexDeltas(jw.DirIdx, len(w.Dirs), MaxIndicesPerJob)
			if err != nil {
				return nil, fmt.Errorf("impact map: job %q dirs: %w", name, err)
			}
		}
		m.Jobs[name] = j
	}
	return m, nil
}

func validateV2Tables(w *wireV2) error {
	if len(w.Files) > MaxUniqueFiles {
		return fmt.Errorf("impact map: %d file paths in the table, over the limit of %d", len(w.Files), MaxUniqueFiles)
	}
	if len(w.Dirs) > MaxUniqueDirs {
		return fmt.Errorf("impact map: %d dir paths in the table, over the limit of %d", len(w.Dirs), MaxUniqueDirs)
	}
	if !sort.StringsAreSorted(w.Files) {
		return fmt.Errorf("impact map: files table is not sorted")
	}
	if !sort.StringsAreSorted(w.Dirs) {
		return fmt.Errorf("impact map: dirs table is not sorted")
	}
	if dup := firstDuplicate(w.Files); dup != "" {
		return fmt.Errorf("impact map: duplicate file path %q in the table", dup)
	}
	if dup := firstDuplicate(w.Dirs); dup != "" {
		return fmt.Errorf("impact map: duplicate dir path %q in the table", dup)
	}
	for _, p := range w.Files {
		if _, err := CleanPath(p, false); err != nil {
			return fmt.Errorf("impact map: files table has unusable path %q", p)
		}
	}
	for _, p := range w.Dirs {
		if _, err := CleanPath(p, true); err != nil {
			return fmt.Errorf("impact map: dirs table has unusable path %q", p)
		}
	}
	return nil
}

func firstDuplicate(sorted []string) string {
	for i := 1; i < len(sorted); i++ {
		if sorted[i] == sorted[i-1] {
			return sorted[i]
		}
	}
	return ""
}

func indicesToPaths(indices []int, table []string) []string {
	out := make([]string, 0, len(indices))
	for _, i := range indices {
		out = append(out, table[i])
	}
	return out
}

func marshalV2JSON(m *Map) ([]byte, error) {
	files, dirs, fileOf, dirOf := buildPathTables(m)
	w := wireV2{
		Version:   Version,
		Revision:  m.Revision,
		CreatedAt: m.CreatedAt,
		Method:    m.Method,
		Files:     files,
		Dirs:      dirs,
		Jobs:      make(map[string]*jobWireV2, len(m.Jobs)),
	}
	for _, name := range sortedKeys(m.Jobs) {
		j := m.Jobs[name]
		if j == nil {
			j = &Job{}
		}
		fi, err := jobFileIndices(j, fileOf)
		if err != nil {
			return nil, err
		}
		di, err := jobDirIndices(j, dirOf)
		if err != nil {
			return nil, err
		}
		fenc, err := encodeIndexDeltas(fi)
		if err != nil {
			return nil, err
		}
		denc, err := encodeIndexDeltas(di)
		if err != nil {
			return nil, err
		}
		w.Jobs[name] = &jobWireV2{
			FileIdx:  fenc,
			DirIdx:   denc,
			Complete: j.Complete,
			Nodes:    j.Nodes,
		}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(w); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func jobFileIndices(j *Job, fileOf map[string]int) ([]int, error) {
	if len(j.fileIdx) > 0 {
		return j.fileIdx, nil
	}
	files, _ := cleanAll(j.Files, false)
	return pathsToIndices(files, fileOf), nil
}

func jobDirIndices(j *Job, dirOf map[string]int) ([]int, error) {
	if len(j.dirIdx) > 0 {
		return j.dirIdx, nil
	}
	dirs, _ := cleanAll(j.Dirs, true)
	return pathsToIndices(dirs, dirOf), nil
}

func buildPathTables(m *Map) (files, dirs []string, fileOf, dirOf map[string]int) {
	fset := map[string]struct{}{}
	dset := map[string]struct{}{}
	if len(m.fileTable) > 0 {
		for _, p := range m.fileTable {
			fset[p] = struct{}{}
		}
	}
	if len(m.dirTable) > 0 {
		for _, p := range m.dirTable {
			dset[p] = struct{}{}
		}
	}
	for _, j := range m.Jobs {
		if j == nil {
			continue
		}
		for _, p := range j.Files {
			if c, err := CleanPath(p, false); err == nil {
				fset[c] = struct{}{}
			}
		}
		for _, p := range j.Dirs {
			if c, err := CleanPath(p, true); err == nil {
				dset[c] = struct{}{}
			}
		}
		if j.mapRef != nil {
			for _, i := range j.fileIdx {
				fset[j.mapRef.fileTable[i]] = struct{}{}
			}
			for _, i := range j.dirIdx {
				dset[j.mapRef.dirTable[i]] = struct{}{}
			}
		}
	}
	files = sortedKeys(fset)
	dirs = sortedKeys(dset)
	fileOf = make(map[string]int, len(files))
	dirOf = make(map[string]int, len(dirs))
	for i, p := range files {
		fileOf[p] = i
	}
	for i, p := range dirs {
		dirOf[p] = i
	}
	return files, dirs, fileOf, dirOf
}

func pathsToIndices(paths []string, table map[string]int) []int {
	out := make([]int, 0, len(paths))
	for _, p := range paths {
		if i, ok := table[p]; ok {
			out = append(out, i)
		}
	}
	return out
}
