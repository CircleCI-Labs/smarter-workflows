package ai

// ClassifierResponse is the structured output schema for job inputs.
type ClassifierResponse struct {
	Jobs []ClassifierJob `json:"jobs"`
}

// ClassifierJob is one job in the classifier response.
type ClassifierJob struct {
	Name       string   `json:"name"`
	Inputs     []string `json:"inputs"`
	Confidence float64  `json:"confidence"`
	Why        string   `json:"why"`
}

// ResponseJSONSchema is the API output_config.format schema (no dynamic map keys).
func ResponseJSONSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"jobs": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"name":       map[string]any{"type": "string"},
						"inputs":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						"confidence": map[string]any{"type": "number"},
						"why":        map[string]any{"type": "string"},
					},
					"required": []any{"name", "inputs", "confidence", "why"},
				},
			},
		},
		"required": []any{"jobs"},
	}
}
