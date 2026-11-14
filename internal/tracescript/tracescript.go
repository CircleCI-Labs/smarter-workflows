// Package tracescript embeds collect-trace.sh, the post-step the planner adds
// to traced jobs (ADR 3, spec "File formats").
package tracescript

import _ "embed"

// Script is the POSIX shell trace collector.
//
//go:embed collect-trace.sh
var Script string
