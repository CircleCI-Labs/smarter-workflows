# 5. A Go binary that is also a `circleci` CLI extension

Date: 2026-11-13. Status: accepted.

## Context

The tool has to be easy to adopt in production if product teams like it. The `circleci` CLI dispatches `circleci <name>` to a binary named `circleci-<name>`; Smarter Testing's `testsuite` is one such extension. CircleCI's policy engine is a public Go library (`circle-policy-agent`), so a Go binary can evaluate policies exactly the way the service does.

## Decision

Ship a single static Go binary, `circleci-smarter`, that can be called directly or as `circleci smarter`. It embeds the trace collector script, and links `circle-policy-agent` for the pre-flight. The orb downloads a pinned, checksummed release. Until releases exist, it builds from source.

## Consequences

- One language and one binary, with no runtime dependencies in the planner job.
- The trace collector stays a small POSIX shell script, so jobs need no extra binary.
