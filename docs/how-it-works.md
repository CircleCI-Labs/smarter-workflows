# How Smarter Workflows works

## The loop

```
push to main ──► setup: plan job ──► full run, every job traced ──► smarter-impact-map ──► project cache
                                                                                                │
push to a branch ──► setup: plan job ◄── impact map for the merge-base (or the newest) ◄────────┘
                       │
                       ├─ changed files (merge-base..HEAD) × impact map → which jobs the change can reach
                       ├─ + mandatory jobs, + ancestors, + workspace consumers
                       ├─ guardrails pre-flight (policies/), repair or full run
                       └─ continue: pruned workflow + smarter-gate
```

1. **The default branch always gets a full run.** The planner adds two steps to each job:
   - a step right after `checkout` that resets file access times;
   - a post-step that records which tracked files the job read and which directories it listed.
   A last job, `smarter-impact-map`, merges these traces into the impact map and saves it to the project cache under the commit's SHA.
2. **A branch gets a plan.** The plan job:
   1. finds the merge-base with the default branch;
   2. restores the impact map for that commit, or the newest map;
   3. compares the changed files with what each job read.
3. **A job runs** when the change touches one of its inputs, or a directory it listed gains or loses a file. Mandatory jobs always run. So do:
   - the ancestors (`requires`) of every job that runs;
   - jobs that consume a workspace produced by a job that runs;
   - jobs with no trace.

   A job that your own `filters` keep off the branch, or whose required jobs they all keep off it, does not run there whatever the change is, even when it is mandatory. The plan leaves it out and says so in a warning. A requirement on a job that does not run is ignored, as in CircleCI, and the plan drops it.
4. **Guardrails.** The planner checks the config it generated against the Rego bundle in `policies/`, using CircleCI's own policy engine (`circle-policy-agent`).
   - If a mandatory job is missing, it adds the job and checks again.
   - Any other hard violation turns the plan into a full run.
5. **The gate.** Every planned workflow ends with `smarter-gate`, which waits for every selected job. Make it your only required status check, so a PR never waits on a skipped job.

## When it runs everything

- the default branch, tags and scheduled pipelines;
- a change under `.circleci/` (or another `--full-run-paths` glob);
- no impact map, or no merge-base;
- any planner error;
- a head commit that contains `[smarter full]`.

## Modes

| Mode | What runs | Use it for |
|---|---|---|
| `shadow` | everything; the plan is only reported | onboarding: see what select mode would skip |
| `select` | only the selected jobs + `smarter-gate` | after shadow mode convinces you |

Each plan job stores `smarter-plan/plan.md` and `plan.json` as artifacts: every job, run or skip, and the reason.

## Results on this repository (2026-11-14)

From the first real impact map:
- No job reads the docs.
- `orb-validate` reads exactly the orb, its validation script and the two CI configs.
- `go-test` reads 106 files.

| Branch change | Select mode runs | Why |
|---|---|---|
| docs only | 1 of 8 jobs (`security-scan`, mandatory) + gate | nothing else reads `docs/` |
| one Go package (`internal/report`) | 5 of 8 | go-test, go-lint and build read it; e2e-plan consumes build's workspace; security-scan is mandatory |
| `.circleci/continue.yml` | all 8 | the pipeline definition changed |

The docs-only branch in select mode spent 46 s of job time, against 280 s for the full run, about 84% less compute.

## Read more

- [Quickstart](quickstart.md)
- [Limitations](limitations.md)
- [Spec](spec/v0.1.md)
- [Decisions](adr/)
