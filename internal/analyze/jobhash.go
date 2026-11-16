package analyze

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/CircleCI-Labs/smarter-workflows/internal/config"
)

func jobHash(cfg *config.Config, entry *config.Entry) (string, error) {
	mat, err := cfg.JobHashMaterial(entry)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(canonicalize(mat))
	if err != nil {
		return "", fmt.Errorf("job hash: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func canonicalize(v any) any {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make(map[string]any, len(keys))
		for _, k := range keys {
			out[k] = canonicalize(x[k])
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = canonicalize(e)
		}
		return out
	default:
		return v
	}
}
