# Limitations (v0.1)

What v0.1 does not do yet. Each item is safe: where the planner can't decide, the job runs.

- **One planned workflow.** Other workflows in `continue.yml` pass through untouched.
- **Matrix jobs** always run.
- **Orb jobs** are traced only with `--trace-orb-jobs`: it adds a `checkout` and a reset as pre-steps, so it only suits orb jobs that check out the repo. Without it they always run.
- **A `checkout` inside a command** gets no reset, so that job's trace comes out incomplete and the job always runs.
- **Expression filters** (string filters) are reported, and left out of the gate.
- **Inputs from outside the repo** aren't traced: the network, time, secrets, images. Full runs on the default branch, mandatory jobs and `--extra-inputs` cover them.
- **The impact map lives in the project cache.** Any branch can write a cache entry, so a forged map could skip jobs. Mandatory jobs and guardrails don't depend on the map. Signing maps on the default branch is planned.
- **Setup job time.** The plan job takes about a minute here, because it builds the planner from source. A released binary would make it a few seconds.
- **The report's job count** includes jobs that filters exclude on the branch.
- **Planned next:**
  - history mining and learned rules;
  - an optional model decider (paths only by default);
  - compose mode (agentic);
  - Chunk sidecar integration (the same impact map, in the inner loop);
  - releases and a published orb.
