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
   - jobs with no trace;
   - job groups: an entry that names a group under `job-groups` runs as one unit, since v0.1 does not look inside it.

   Some entries run no steps: an approval (`type: approval`, on the entry or in the job's definition), a no-op job (`type: no-op`) and a release job (`type: release`). They are never traced and have no inputs, so the jobs around them decide. An approval or a no-op job stays when a job that runs requires it. A release job stays when a job it requires runs: it tracks that job's release. A no-op job that nothing requires, such as an `all-tests-passed` that you use as your required status check, always stays: it waits only for the jobs that stay, or for `smarter-gate` when none of the jobs it waited for does, so it never passes before the selected jobs have.

   A job that your own `filters` keep off the branch, or whose required jobs they all keep off it, does not run there whatever the change is, even when it is mandatory. The plan leaves it out and says so in a warning. A requirement on a job that does not run is ignored, as in CircleCI, and the plan drops it.
4. **Guardrails.** The planner checks the config it generated against the Rego bundle in `policies/`, using CircleCI's own policy engine (`circle-policy-agent`).
   - If a mandatory job is missing, it adds the job and checks again.
   - Any other hard violation turns the plan into a full run.
5. **The gate.** Every planned workflow ends with `smarter-gate`, which waits for every selected job. The workflow always runs, even when only the gate is left.
   - The gate is a no-op job (`smarter-gate: {type: no-op}`): no executor and no steps, so it costs no credits and starts no container. It is CircleCI's [gatekeeper pattern](https://circleci.com/docs/guides/orchestrate/orchestration-cookbook/#simplify-github-required-status-checks-with-no-op-jobs) for one required status check. It succeeds when every job it requires succeeds.
   - CircleCI's GitHub App reports one check per workflow (e.g. `ci - <definition>`): make that check required.
   - If you use per-job status checks, require `smarter-gate`.
   Either way, a PR never waits on a skipped job.

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

## Tools for agents

Two commands let an agent (or a person) read a pipeline and check a layout of it, with no plan and no impact map needed:

- `circleci-smarter catalog --config .circleci/continue.yml --map impact-map.json` prints the jobs of a workflow as JSON: each one's kind, executor, contexts, filters, requirements, workspace use and, with a map, how much of the repository it read.
- `circleci-smarter layout check --config composed.yml --catalog catalog.json --policy-bundle policies --mandatory security-scan --restricted-contexts deploy` checks how the jobs of a workflow wait for each other, and reports each problem with a code and a hint on how to fix it: a `requires` that points nowhere, a cycle, a workspace attached before it is persisted, a restricted context with no approval before it, a missing mandatory job, a gate that is not a no-op job or does not wait for every job that runs, and an approval that a branch filter can leave out. It exits 3 when there are errors. A plan the planner writes has a no-op gate and no structural problem that its input did not have.

The [spec](spec/v0.1.md#catalog) has the formats.

## Read more

- [Quickstart](quickstart.md)
- [Limitations](limitations.md)
- [Spec](spec/v0.1.md)
- [Decisions](adr/)
