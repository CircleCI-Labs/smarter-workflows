//go:build tools

package guard

// This file keeps circle-policy-agent in go.mod until the policy package
// imports it for real: `go mod tidy` drops a requirement that nothing imports,
// and it reads files behind any build tag. The tag keeps the policy engine out
// of this package's builds, so the planner binary stays small until it needs it.
import _ "github.com/CircleCI-Public/circle-policy-agent/cpa"
