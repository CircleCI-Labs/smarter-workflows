// Package pipelineparams reads pipeline parameter values for plan and continue.
package pipelineparams

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

// Load reads a JSON object from path. Numbers are kept as json.Number.
func Load(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var params map[string]any
	if err := dec.Decode(&params); err != nil {
		return nil, fmt.Errorf("%s: pipeline parameters must be a JSON object: %v", path, err)
	}
	if dec.More() {
		return nil, fmt.Errorf("%s: pipeline parameters must be a single JSON object", path)
	}
	return params, nil
}
