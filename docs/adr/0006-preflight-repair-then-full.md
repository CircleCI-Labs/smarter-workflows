# 6. Every plan passes the guardrails before it continues: check, repair, or fall back

Date: 2026-11-13. Status: accepted.

## Context

CircleCI config policies also evaluate the continuation config. Server-side enforcement needs the Scale plan, and a continuation the server rejects fails the pipeline after the setup job has already spent its one continuation.

## Decision

1. The planner runs the project's policy bundle locally, against the config it generated, using `circle-policy-agent`.
2. Policies in the bundle tag violations the planner can fix with a machine-readable code, e.g. `[smarter:require-job=security-scan]`. The planner applies the fix and plans again, up to 3 times.
3. A violation it can't fix turns the plan into a full run, which is checked again.
4. If even the full config fails, the setup job fails with the violations listed, without continuing.
5. The same bundle can be pushed as org policy, limited to the projects that opt in, starting with soft-fail.

## Consequences

- Guardrails work on every plan, Scale or not. On Scale they are also enforced server-side.
- Policies can't read parameters as values, so the planner emits literal config and never adds parameters.
- Compose mode will reuse the same loop, with an agent doing the repair.
