package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// validOutputPath rejects paths the CLI must not write to.
func validOutputPath(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("path is empty")
	}
	if strings.ContainsRune(path, 0) {
		return fmt.Errorf("path contains a null byte")
	}
	return nil
}

// writeOutputFile creates parent directories and writes data with the given mode.
func writeOutputFile(path string, data []byte, mode os.FileMode) error {
	if err := validOutputPath(path); err != nil {
		return fmt.Errorf("output path: %w", err)
	}
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return err
		}
	}
	return os.WriteFile(path, data, mode)
}
