# Requires and statuses

How one job waits for another. Tags such as [cfg#requires] are sources; the URLs are at the end of the file.
`(compiler-checked)` means confirmed with `circleci config validate` or `config process` (CLI 1.6.0).
`(heuristic)` means advice, not a documented rule.

## 1. Plain requires

- Jobs start together unless `requires` says otherwise. A job with several requirements starts when every one has reached an allowed status; the default is `success`. [cfg#requires] [wf#flex]
- An item names a workflow entry: its `name:` if it has one, else the job name. A matrix is required by its alias (§4), a job group by its group name (§5).
- Call one job twice only with distinct `name:` values. Without them, a `requires` on that job fails ("which is the name of 2 other jobs", compiler-checked). [cfg#name]
- The compiler rejects a name that no entry of the workflow answers to, a job that requires itself, and a cycle (compiler-checked).
- It does not reject an approval whose name is also a job under `jobs:`. The docs say the name must be unique, and the steps of such a job never run. [wf#approval]
- A misspelled key on a local job (`requirez`, `filter`) fails with "Unexpected argument(s)" (compiler-checked). An orb job can accept an unknown key and ignore it: read the orb with `get_orb_source` first (see tools.md).

## 2. Status forms

| Write | The job starts when the dependency ends as | Use for |
|---|---|---|
| `- build` | `success` | normal order |
| `- deploy: failed` | `failed` | notify |
| `- deploy: [failed, canceled]` | any listed status | rollback |
| `- deploy: terminal` | any end state: success, failed, canceled, unauthorized, not_run | cleanup |

```yaml
workflows:
  ci:
    jobs:
      - build
      - deploy:
          requires:
            - build
      - cleanup:
          requires:
            - deploy: terminal
      - notify-failure:
          requires:
            - deploy: failed
      - rollback:
          requires:
            - deploy: [failed, canceled]
```

- Only `success`, `failed`, `canceled`, `unauthorized`, `not_run` and `terminal` exist. The compiler rejects `skipped` and `running` (compiler-checked). [cfg#requires]
- A job waits for all its requirements, and each must reach one of its own listed statuses. The two items `- a: failed` and `- b: failed` therefore need both jobs to fail, not either. For "either", use one notify job per dependency. [wf#flex]
- Use a non-success status only on cleanup, notify and rollback jobs. Never on a check, a build or a deploy.
- `unauthorized` means the job's restricted context refused the person who triggered the workflow or approved the job. A cleanup that must run in that case needs `terminal`, or `unauthorized` in its list. [ctx#approve]
- `not_run` is the status of a job that was skipped, for example by a serial group's order protection. [serial#order]
- The gate never requires a job by a non-success status (see shaping-patterns.md §3).

## 3. Filtered requirements

The rule, verbatim: "When jobs you list as dependencies do not execute (due to filters, for example), CircleCI ignores them as dependencies for other jobs. But if filters exclude all dependencies of a job, that job doesn't execute either." [cfg#requires]

| Job C requires | On a branch where | C runs? |
|---|---|---|
| A, B | A is filtered out, B runs | yes, after B |
| A | A is filtered out | no: all its dependencies are filtered out |
| A, B | A and B are filtered out | no |
| `A: failed` | A is filtered out | unclear: do not rely on it (the v0.1 planner cannot decide it either, `docs/limitations.md`) |

The effect climbs the graph: a job whose requirements are all excluded does not run, so the jobs behind it lose a requirement in turn.

### The approval footgun

An approval that a branch filter removes is ignored like any other filtered dependency. The job behind it keeps its other requirements and runs without approval.

```yaml
# UNSAFE: only the approval is limited to main.
workflows:
  ci:
    jobs:
      - build
      - hold-deploy:
          type: approval
          requires: [build]
          filters:
            branches:
              only: main
      - deploy:
          requires: [hold-deploy, build]
```

On any other branch `hold-deploy` does not run, CircleCI ignores it, and `deploy` runs as soon as `build` passes.
The mirror image is just as wrong: filter only the job and the approval holds the workflow on a branch where nothing can follow it. A held workflow is not terminal and CircleCI cancels it after 90 days. [wf#states]

```yaml
# SAFE: the approval and the job behind it carry the same filters, and the job
# lists nothing that runs on other branches.
workflows:
  ci:
    jobs:
      - build
      - hold-deploy:
          type: approval
          requires: [build]
          filters:
            branches:
              only: main
      - deploy:
          requires: [hold-deploy]
          filters:
            branches:
              only: main
```

Rules:
- An approval and every job that requires it get the same filters. A stricter job leaves the approval holding the workflow on branches the job never runs on.
- A job behind an approval requires the approval and nothing else, unless the other job is also upstream of the approval.
- A job that requires `X: failed` or `X: terminal` gets the same filters as X.
- A job that must always run (the mandatory scan, the gate's anchor job) has no filters and no `requires`, or it requires only unfiltered jobs. (heuristic, from the rule above)
- A job that runs on a tag needs a tag filter, and so does every job it requires, directly or not. [cfg#tags] [wf#tags]

## 4. Matrix jobs

| You want | Write | Compiles to (compiler-checked) |
|---|---|---|
| every cell | `requires: [build]` (the job name, or the matrix `alias`) | every cell |
| every cell, cells named with a template | the same: the alias defaults to the job name | every cell |
| one cell | `requires: [build-v0.1]` | that cell |
| cell to cell between two matrices | `name: build-v<< matrix.version >>` on both, and `requires: [build-v<< matrix.version >>]` in the second | pairs |

```yaml
jobs:
  build:
    parameters:
      version:
        type: string
    docker:
      - image: cimg/base:2026.11
    steps:
      - run: echo "<< parameters.version >>"

workflows:
  ci:
    jobs:
      - build:
          name: build-v<< matrix.version >>
          matrix:
            alias: all-builds
            parameters:
              version: ["0.1", "0.2"]
      - package:
          requires:
            - all-builds
```

- `exclude:` removes combinations from the matrix. [cfg#matrix]
- Parameter values in a matrix are quoted strings when the job parameter is a string.
- The v0.1 planner always runs matrix entries: do not count on pruning them (`docs/limitations.md`).

## 5. Job groups

A job group is a named set of jobs, defined once under the top-level `job-groups:` and invoked like a job. [cfg#job-groups]

```yaml
job-groups:
  release-set:
    jobs:
      - deploy
      - smoke-test:
          requires: [deploy]

workflows:
  ci:
    jobs:
      - build
      - release-set:
          requires: [build]
      - notify:
          requires: [release-set]
```

- Downstream jobs require the group's name, never a job inside it. A job inside a group must not require a job of a different group. [serial#errors]
- The group expands inline with a no-op start job and a no-op end job. Jobs that require the group are rewired to the end job, which by default runs only if every leaf job succeeded. To react to another outcome, give `success` and/or `not_run` next to the group name. [serial#expansion]
- The invocation takes `requires`, `filters`, `name:` and `serial-group`. It cannot take `matrix`, `override-with`, `context`, `pre-steps` or `post-steps` (the compiler rejects `context`, compiler-checked). A group cannot nest another group, take `parameters`, or share its name with a job. Invoke it twice only with distinct `name:` values. [serial#errors]
- Keep approvals outside the group: inside a serialized group an approval would hold the organization-wide lock until someone clicks. (heuristic, from "no other serial group with the same key can start until all jobs in the group complete") [serial#job-groups]
- Keep restricted-context jobs and mandatory jobs out of groups. The guardrail bundle reads only the entries of a workflow's `jobs`, so it cannot see inside a group (`policies/guardrails.rego`).

## 6. What the v0.1 planner makes of each form

| Form | Planner |
|---|---|
| `requires` by success | understood; every ancestor of a selected job is kept |
| a requirement on a job that does not run | dropped from the pruned config, as CircleCI ignores it |
| a job whose requirements are all excluded | does not run, whatever else holds |
| `requires` by another status | the job is "unknown"; the gate does not wait for it |
| matrix entry | always runs |

Source: `docs/spec/v0.1.md` (steps 4 to 7), `docs/limitations.md`.

## Checklist

- [ ] Every `requires` item is an entry name of this workflow, a matrix alias or a job-group name.
- [ ] No cycle, no self-requirement, no job called twice without distinct `name:` values.
- [ ] For every filtered job, you know what its dependents do on the branches that filter it out.
- [ ] Every approval has the filters of the jobs behind it, and the jobs behind it require nothing that runs elsewhere.
- [ ] Non-success statuses only on cleanup, notify and rollback jobs, with the dependency's filters.

[cfg#requires]: https://circleci.com/docs/reference/configuration-reference/#requires
[cfg#name]: https://circleci.com/docs/reference/configuration-reference/#name
[cfg#matrix]: https://circleci.com/docs/reference/configuration-reference/#matrix
[cfg#job-groups]: https://circleci.com/docs/reference/configuration-reference/#job-groups
[cfg#tags]: https://circleci.com/docs/reference/configuration-reference/#tags
[wf#flex]: https://circleci.com/docs/guides/orchestrate/workflows/#flexible-job-dependency
[wf#approval]: https://circleci.com/docs/guides/orchestrate/workflows/#holding-a-workflow-for-a-manual-approval
[wf#states]: https://circleci.com/docs/guides/orchestrate/workflows/#states
[wf#tags]: https://circleci.com/docs/guides/orchestrate/workflows/#executing-workflows-for-a-git-tag
[ctx#approve]: https://circleci.com/docs/guides/security/contexts/#approve-jobs-that-use-restricted-contexts
[serial#order]: https://circleci.com/docs/guides/orchestrate/controlling-serial-execution-across-your-organization/#job-skipped-unexpectedly
[serial#job-groups]: https://circleci.com/docs/guides/orchestrate/controlling-serial-execution-across-your-organization/#job-groups
[serial#expansion]: https://circleci.com/docs/guides/orchestrate/controlling-serial-execution-across-your-organization/#how-job-group-expansion-works
[serial#errors]: https://circleci.com/docs/guides/orchestrate/controlling-serial-execution-across-your-organization/#job-group-compile-errors
