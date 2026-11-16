---
page-title: Quickstart
page-audience: admin
page-description: Adopt Smarter Workflows in shadow mode, refresh the learned layer on a schedule, and switch to select mode when reports agree.
---

# Quickstart

> [!NOTE]
> CircleCI Labs project, `0.x`, not supported by CircleCI support. [What is tested](../README.md#what-is-tested) and [Limitations](limitations.md) describe current coverage.

## Adoption path

1. **Trial `learn` locally** with a read-only CircleCI token or a recorded history file (`circleci-smarter learn --history FILE --learned-out …`). Read the report’s recall and incomplete jobs.
2. **Add the orb** with `plan` on every pipeline (shadow) and `learn` on a schedule. Attach a context with `CIRCLECI_TOKEN` and optional `ANTHROPIC_API_KEY`.
3. **Switch to select** when shadow `plan.md` and the weekly learn report agree.

### Schedule trigger

- **GitHub App projects:** Project Settings → **Triggers** → **Schedule** → add a weekly trigger on the default branch.
- **OAuth projects:** use [scheduled pipelines](https://circleci.com/docs/guides/orchestrate/schedule-triggers/) (pipeline parameter or trigger type `schedule`).

The orb example wires `learn` to `pipeline.trigger.type == "schedule"` and keeps `plan` on other triggers ([`shadow-learn-schedule.yml`](../orb/src/examples/shadow-learn-schedule.yml)).

## Setup config (orb)

```yaml
version: 2.1
setup: true
orbs:
  smarter: cci-labs/smarter-workflows@1.0.0
workflows:
  plan:
    when:
      not:
        equal: [schedule, << pipeline.trigger.type >>]
    jobs:
      - smarter/plan:
          config: .circleci/continue.yml
          workflow: ci
          mode: shadow
          mandatory: security-scan
          policy-bundle: policies
          ai-review: shadow
          executor: smarter/go
          source: build
          resource_class: medium
  refresh-learned:
    when:
      equal: [schedule, << pipeline.trigger.type >>]
    jobs:
      - smarter/learn:
          config: .circleci/continue.yml
          workflow: ci
          limit: 200
          context: [your-circleci-token-context]
          executor: smarter/go
          source: build
          resource_class: medium
```

Until the orb is published, copy the inline jobs from this repository’s [`.circleci/config.yml`](../.circleci/config.yml) (they build the planner from source).

## Catalog

1. Move jobs, workflows, orbs and executors to `.circleci/continue.yml`. That file is the catalog; nothing Smarter-specific is required inside it.
2. Turn on setup workflows under **Project Settings → Advanced**.

## Artifacts and branch protection

- Every `plan` run stores `smarter-plan/plan.md`, `plan.json`, and `analyze-report.md`.
- Every `learn` run stores `smarter-learn/learn.md` and `learn.json`, and updates the learned layer cache (`learned-key-prefix`, default `smarter-learned-v1`).
- Require the continued workflow’s GitHub check, or per-job `smarter-gate` if you use job checks.

## Pipeline parameters

If `continue.yml` uses workflow `when`/`unless` on `pipeline.parameters.*`, set the `parameters` job parameter to a JSON file of values. The planner writes `parameters.json` under the plan report for `continue`.

## Optional guardrails

Copy [`policies/`](../policies/), edit settings at the top of `guardrails.rego`, and set `policy-bundle: policies` on `plan`. Run `circleci policy test policies`.

## Several pipelines in one project

Share one project cache but give each setup config its own `learned-key-prefix`. No prefix may be a prefix of another (use `smarter-demo-learned-v1` beside `smarter-learned-v1`, not `smarter-learned-v1-demo`).