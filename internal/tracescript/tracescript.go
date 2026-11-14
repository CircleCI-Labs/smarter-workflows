// Package tracescript embeds the two shell steps the planner adds to traced
// jobs (ADR 3, spec "File formats" and "The algorithm", step 8):
//
//   - Reset runs right after checkout. Checkout itself reads every file, through
//     git's racy-index check. Reset sets atimes back below mtimes, so the job's
//     own reads show up. It records whether atime works on this filesystem.
//   - Script runs as a post-step and writes the job's trace TSV.
package tracescript

import _ "embed"

// Reset is the post-checkout atime reset step.
//
//go:embed reset-atime.sh
var Reset string

// Script is the POSIX shell trace collector.
//
//go:embed collect-trace.sh
var Script string
