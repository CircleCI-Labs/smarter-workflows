# 4. The catalog is native CircleCI config

Date: 2026-11-13. Status: accepted.

## Context

The first idea was a new `catalog.yml` with metadata on each job. That is one more format to learn and keep in sync. The jobs, commands, executors and orbs a team uses already live in its CircleCI config, or in orbs it shares.

## Decision

- **Select mode** (v0.1): the catalog is the project's own full continuation config (`.circleci/continue.yml`). The planner prunes those workflows and keeps the order, approvals, filters and contexts the team wrote.
- **Planner settings** (mode, mandatory jobs, full-run paths, extra inputs) are parameters of the orb job, not a new file.
- **Shared catalogs** are orbs, pinned to exact versions: the only job-level units that cannot change underneath you.
  - URL orbs are allowed only when pinned to a commit SHA.
  - CircleCI functions are step-level units, so they are building blocks for compose mode, not jobs.
- **Compose mode** (later): an agent wires jobs from a catalog orb (jobs, commands, executors) into workflows, and the guardrails bound what it can produce.

## Consequences

- Adoption means moving the pipeline into `continue.yml` and adding one setup job. No new schema.
- What the planner generates reads like the team's own config, minus the skipped jobs, plus the gate job.
