---
page-title: Limitations
page-audience: admin
page-description: What v0.3 cannot see or prove, and why unsure cases always run.
---

# Limitations

Where the planner cannot decide, the job runs.

## Static analysis and dynamic inputs

Paths built from environment variables, runtime downloads, or opaque shell are invisible unless the learned layer or history adds them. **Make**, unknown binaries, and orb commands outside the known table mark a job **unknown** (it runs).

## Opaque or unfamiliar jobs

Orb jobs, self-hosted images, and steps the analyzer cannot follow stay unknown until AI or history completes the map. **Shadow mode** shows what select would do before you trust it.

## AI is optional

`learn` and `analyze` can run with `--ai off`. Without AI, coverage depends on static signals and history corrections only. Plan-time `--ai-review` adds jobs only; it never removes static proof.

## Learned layer timing

`plan` merges the learned layer from cache on every pipeline. Until the first scheduled `learn` completes, maps are **static only** (more conservative). First pipelines after onboarding may run more jobs than after learn.

## Costs

- **`learn`:** CircleCI API calls for history; optional Anthropic usage during classify (see the report’s token and cost lines).
- **`plan`:** Inline analyze each run (no AI by default); optional `--ai-review` calls Anthropic when enabled.

## Other v0.x limits (unchanged)

- One workflow is planned; others pass through.
- Matrix jobs and job groups always run as units.
- The learned layer is stored in the **project cache** (tamper risk; see [Threat model](threat-model.md)).
- Workflow `when`/`unless` need known parameter values (`--parameters`, branch, tag, trigger type).

Planned improvements remain in the README and ADRs (signing cached layers, richer orb coverage).