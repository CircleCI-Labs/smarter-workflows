# 1. Select jobs, not tests

Date: 2026-11-13. Status: accepted.

## Context

Picking tests is already well served: CircleCI Smarter Testing, and the test impact tools from several other vendors. What nobody covers well on CircleCI is which *jobs* and *workflows* a change needs, together with their order, approvals, filters and contexts.

## Decision

Smarter Workflows decides at the level of jobs and workflows. Inside a job it runs, test selection is left to Smarter Testing or to whatever tool the job already uses.

## Consequences

- The two layers compose: Smarter Workflows skips whole jobs, and Smarter Testing trims the tests inside the jobs that run.
- The unit of everything here (impact map, plan, report) is a workflow job, never a test.
