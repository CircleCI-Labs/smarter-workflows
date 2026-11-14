# Quickstart (private preview)

The orb isn't published yet. Until it is, copy the setup job from this repo's [`.circleci/config.yml`](../.circleci/config.yml); it builds the planner from source.

1. **Move your pipeline** (jobs, workflows, orbs, executors) to `.circleci/continue.yml`. That file is the catalog the planner picks from. Nothing else in it changes.
2. **Add the setup config** in `.circleci/config.yml`: `setup: true`, one workflow, one `plan` job. The job runs:
   - `circleci-smarter base`, to get the merge-base;
   - two `restore_cache` steps for the impact map;
   - `circleci-smarter plan --config .circleci/continue.yml --workflow <your workflow> --mode shadow --mandatory <jobs>`;
   - `store_artifacts` for `/tmp/smarter/plan`;
   - `circleci-smarter continue --config /tmp/smarter/continue.yml`.
3. **Turn on setup workflows** under Project Settings → Advanced, if they aren't on.
4. **Push to the default branch.** The first full run traces every job and saves the first impact map.
5. **Push branches** and read `smarter-plan/plan.md` in each plan job's artifacts. Shadow mode still runs everything.
6. **Pick the required check** in your branch protection rules: the planned workflow's check (GitHub App projects report one check per workflow), or `smarter-gate` if you use per-job statuses.
7. **Switch to select mode** (`--mode select`) when the shadow reports convince you.

## Optional: guardrails

Copy [`policies/`](../policies/), edit the settings at the top of `guardrails.rego`, and pass `--policy-bundle policies` to `plan`. Every plan is then checked before it continues. On the Scale plan you can also push the same bundle as org policy:
- limit it to the projects that opt in;
- start with every rule in `soft_rules`;
- leave the `_test.rego` file out of the pushed directory.

Run the tests with `circleci policy test policies`.

## Several pipelines in one project

All pipelines of a project share its cache. Give each setup config its own `--map-key-prefix` (default `smarter-impact-v1`) and use the same prefix in its `restore_cache` keys. Restore keys match by prefix, so no prefix may start with another one followed by a dash: `smarter-impact-v1` would restore the maps of `smarter-impact-v1-demo`. `smarter-demo-impact-v1` is fine.

## Requirements

- **Linux jobs** (docker or machine) for tracing. Other jobs always run.
- **The default branch builds regularly.** Maps live in the project cache, so they expire with its retention.
- **Approved images, if your org enforces them.** This repo's own org rejects digest-pinned language images, for example.
