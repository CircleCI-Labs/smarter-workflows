// Package tracescript embeds the two shell steps the planner adds to traced
// jobs (ADR 3, spec "File formats" and "The algorithm", step 8):
//
//   - Reset runs right after checkout. Checkout itself reads every file, through
//     git's racy-index check. Reset sets every atime back to the epoch (0), so
//     the job's own reads show up as an atime above 0, and it records in
//     .git/smarter-trace-ready or .git/smarter-trace-unsupported whether atime
//     works on this filesystem.
//   - Script runs as a post-step and writes the job's trace TSV to
//     $SMARTER_TRACE_DIR (default /tmp/smarter/traces).
//
// Both scripts are POSIX sh. They never fail the job, they print one line and
// never print environment variables. The planner inlines each one as the
// command of a CircleCI run step, so they avoid what would break that: tabs,
// trailing spaces, non-ASCII bytes and the << and >> that CircleCI reads as
// parameter syntax (tracescript_test.go checks it). scripts/test-trace.sh tests
// what they do.
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
