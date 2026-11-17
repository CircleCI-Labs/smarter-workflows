package analyze

import (
	"io/fs"
	"os"
	"path/filepath"
)

func statRepo(repoRoot, rel string) (fs.FileInfo, error) {
	p := filepath.Join(repoRoot, filepath.FromSlash(rel))
	return os.Stat(p)
}

func fileExists(repoRoot, rel string) bool {
	st, err := statRepo(repoRoot, rel)
	return err == nil && !st.IsDir()
}

func dirExists(repoRoot, rel string) bool {
	st, err := statRepo(repoRoot, rel)
	return err == nil && st.IsDir()
}

func walkRepoTree(repoRoot string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(repoRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "node_modules" {
				return filepath.SkipDir
			}
		}
		rel, err := filepath.Rel(repoRoot, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		paths = append(paths, rel)
		return nil
	})
	return paths, err
}
