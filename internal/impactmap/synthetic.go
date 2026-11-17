package impactmap

import (
	"fmt"
	"time"
)

// SyntheticMap builds a map for benchmarks and tests. Each job reads filesPerJob
// paths from a pool of totalFiles unique synthetic file paths.
func SyntheticMap(jobCount, totalFiles, filesPerJob int) (*Map, error) {
	if filesPerJob > totalFiles {
		return nil, fmt.Errorf("filesPerJob > totalFiles")
	}
	m := &Map{
		Version:   Version,
		Revision:  "bench0000000000000000000000000000000000",
		CreatedAt: time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
		Method:    DefaultMapMethod,
		Jobs:      make(map[string]*Job, jobCount),
	}
	for j := 0; j < jobCount; j++ {
		start := (j * 997) % totalFiles
		files := make([]string, 0, filesPerJob)
		for i := 0; i < filesPerJob; i++ {
			idx := (start + i*17) % totalFiles
			files = append(files, fmt.Sprintf("pkg/service%04d/module/file%05d.go", idx/100, idx))
		}
		m.Jobs[fmt.Sprintf("job-%04d", j)] = &Job{
			Files:    files,
			Dirs:     []string{"."},
			Complete: true,
			Nodes:    1,
		}
	}
	return m, nil
}
