---
page-title: Limitations
page-audience: admin
page-description: What v0.1 does not do yet, and why each limit is safe: where the planner cannot decide, the job runs.
---

# Limitations (v0.1)

What v0.1 does not do yet. Each item is safe: where the planner cannot decide, the job runs.

- **One planned workflow.** Other workflows in `continue.yml` pass through with the same meaning. YAML anchors, aliases and merge keys are expanded everywhere, and formatting such as indentation may change.
- **The gate is a no-op job**, so it runs no steps and cannot check anything itself. It only passes when the jobs it requires pass. `--gate-image` no longer picks its image (the gate has none): it is a deprecated alias of `--map-image`, the image of the impact-map job.
- **Job groups are one opaque unit.** An entry that names a group under `job-groups` always runs, with everything it requires, and is not traced. Its `serial-group` and the group are kept as written. The planner and `layout check` do not look inside a group: a restricted context used by a job inside one is not seen, and a job outside the group cannot require a job inside it.
- **A release job** (`type: release`) is never traced. It stays when a job it requires runs, and then so does everything it requires.
- **Matrix jobs** always run.
- **Orb jobs** are traced only with `--trace-orb-jobs`: it adds a `checkout` and a reset as pre-steps, so it only suits orb jobs that check out the repo. Without it they always run.
- **A `checkout` inside a command** gets no reset, so that job's trace comes out incomplete and the job always runs.
- **Expression filters** (string filters) are reported, and left out of the gate.
- **Workflow `when`/`unless`** are evaluated only when every value is known from `--parameters`, `--branch`, `--tag` and `--trigger-type`. Unsupported forms still mean a full run. When the condition is false, the workflow is not pruned and gets no gate (CircleCI skips it).
- **A job that requires only filtered-out jobs by a status** (`- deploy: failed`, with `deploy` filtered out on the branch) may or may not run, so the plan cannot leave it out or drop its requirement: it is a full run.
- **Inputs from outside the repo** are not traced: the network, time, secrets, images. Full runs on the default branch, mandatory jobs and `--extra-inputs` cover them.
- **The impact map lives in the project cache.** Any branch can write a cache entry, so a forged map could skip jobs. Mandatory jobs and guardrails do not depend on the map. Signing maps on the default branch is planned.
- **Setup job time.** The plan job takes about a minute here, because it builds the planner from source. A released binary would make it a few seconds.
- **The report's job count** includes jobs that filters exclude on the branch.
- **Planned next:**
  - history mining and learned rules;
  - an optional model decider (paths only by default);
  - compose mode (agentic);
  - Chunk sidecar integration (the same impact map, in the inner loop);
  - releases and a published orb.
