// Package deps pins the shared dependencies until the packages that use them
// land. Delete it once nothing else needs it.
package deps

import (
	_ "github.com/CircleCI-Public/circle-policy-agent/cpa"
	_ "github.com/bmatcuk/doublestar/v4"
	_ "go.yaml.in/yaml/v3"
)
