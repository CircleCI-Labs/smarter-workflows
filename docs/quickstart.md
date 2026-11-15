---
page-title: Quickstart
page-audience: admin
page-description: Add Smarter Workflows to a CircleCI project in shadow mode, read the plan reports, and switch to select mode when they convince you.
---

# Quickstart

> [!NOTE]
> CircleCI Labs project, `0.x`, not supported by CircleCI support. [What is tested](../README.md#what-is-tested) shows what CI checks, and [Limitations](limitations.md) lists what v0.1 does not do yet.

The orb is not published yet. Once it is, adopt Smarter Workflows with one setup job from the `cci-labs/smarter-workflows` orb (see [orb examples](../orb/src/examples/) or the [README quickstart](../README.md#quickstart)). Until then, copy the inline setup job from this repository's [`.circleci/config.yml`](../.circleci/config.yml); it builds the planner from source and matches what the orb job does.

1. **Move your pipeline** (jobs, workflows, orbs, executors) to `.circleci/continue.yml`. That file is the catalog the planner picks from. Nothing else in it changes.
2. **Add a setup config** in `.circleci/config.yml` with `setup: true`, the orb, and one `smarter/plan` job (see below). Turn on setup workflows under Project Settings → Advanced, if they are not on.
3. **Start in shadow mode** (`mode: shadow`). Use `source: build` with the `go` executor until releases exist; then use `source: release` (the default) so the job downloads a signed binary.
4. **Push to the default branch.** The first full run traces every job and saves the impact map. On later pushes, open `smarter-plan/plan.md` in the plan job's artifacts.
5. **Require the planned workflow's check** in branch protection (or `smarter-gate` if you use per-job statuses). `smarter-gate` is a no-op job and costs no credits.
6. **Switch to select mode** (`mode: select`) when shadow reports look right.

```yaml
version: 2.1
setup: true
orbs:
  smarter: cci-labs/smarter-workflows@1.0.0
workflows:
  plan:
    jobs:
      - smarter/plan:
          config: .circleci/continue.yml
          workflow: ci
          mode: shadow
          mandatory: security-scan
          policy-bundle: policies
          executor: smarter/go
          source: build
          resource_class: medium
```

## Pipeline parameters

If the continuation config uses workflow `when`/`unless` on `pipeline.parameters.*`, pass a JSON file of the values the continuation will receive: set `parameters` on the `smarter/plan` job to that path (for example `.circleci/pipeline-parameters.json`). The planner uses them when selecting jobs and writes `/tmp/smarter/plan/parameters.json` for `continue`.

## Optional: guardrails

Copy [`policies/`](../policies/), edit the settings at the top of `guardrails.rego`, and pass `policy-bundle: policies` on the plan job (or `--policy-bundle policies` with an inline setup job). Every plan is then checked before it continues. On the Scale plan you can also push the same bundle as org policy:

- limit it to the projects that opt in;
- start with every rule in `soft_rules`;
- leave the `_test.rego` file out of the pushed directory.

Run the tests with `circleci policy test policies`.

## Several pipelines in one project

All pipelines of a project share its cache. Give each setup config its own `map-key-prefix` (default `smarter-impact-v1`) on the plan job. Restore keys match by prefix, so no prefix may start with another one followed by a dash: `smarter-impact-v1` would restore the maps of `smarter-impact-v1-demo`. `smarter-demo-impact-v1` is fine.

## Requirements

- **Linux jobs** (docker or machine) for tracing. Other jobs always run.
- **The default branch builds regularly.** Maps live in the project cache, so they expire with its retention.
- **Approved images, if your org enforces them.** Some orgs' config policies allow only exact image tags, so an image pinned by digest can be rejected.