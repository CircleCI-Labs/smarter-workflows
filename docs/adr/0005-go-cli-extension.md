# 5. A Go binary that is also a `circleci` CLI extension

Date: 2026-11-13. Status: accepted.

## Context

The tool has to be easy to adopt in production if product teams like it. The `circleci` CLI dispatches `circleci <name>` to a binary named `circleci-<name>`; Smarter Testing's `testsuite` is one such extension. CircleCI's policy engine is a public Go library (`circle-policy-agent`), so a Go binary can evaluate policies exactly the way the service does.

## Decision

Ship a single static Go binary, `circleci-smarter`, that can be called directly or as `circleci smarter`. It links `circle-policy-agent` for the pre-flight. The orb downloads a pinned, checksummed release. Until releases exist, it builds from source.

## Consequences

- One language and one binary, with no runtime dependencies in the planner job.
- Impact maps are built by `analyze` in the setup job, not by steps injected into customer jobs (ADR 8).
