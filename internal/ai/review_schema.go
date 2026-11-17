package ai

// ReviewResponse is structured output for plan-time AI review.
type ReviewResponse struct {
	Add []ReviewAdd `json:"add"`
}

// ReviewAdd names one job the model wants to add to the plan.
type ReviewAdd struct {
	Name       string  `json:"name"`
	Why        string  `json:"why"`
	Confidence float64 `json:"confidence"`
}

// ReviewJSONSchema is the API schema for plan-time review (add-only).
func ReviewJSONSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"add": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"name":       map[string]any{"type": "string"},
						"why":        map[string]any{"type": "string"},
						"confidence": map[string]any{"type": "number"},
					},
					"required": []any{"name", "why", "confidence"},
				},
			},
		},
		"required": []any{"add"},
	}
}
