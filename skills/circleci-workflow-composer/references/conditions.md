# Conditions

Where a condition can live, what it accepts, and how it meets `requires`. Tags such as [cfg#logic] are sources; the URLs are at the end of the file.
`(compiler-checked)` means confirmed with `circleci config validate` (CLI 1.6.0). `(heuristic)` means advice, not a documented rule.

## 1. Where conditions live

| Level | Key | Accepts | Use for |
|---|---|---|---|
| Workflow | `when`, `unless` | an expression or a logic statement | switch a whole workflow on branch, trigger type or parameter [cfg#when-wf] |
| Job in a workflow | `filters` as a string | an expression | switch a job on pipeline values and parameters [cfg#expression-filters] |
| Job in a workflow | `filters` as a map (`branches`, `tags`) | `only` and `ignore` lists, `/regex/` | the same by branch or tag; the docs call it deprecated [cfg#filters] |
| Step | `when:` on a `run` | `always`, `on_success`, `on_fail` | react to the job's status while it runs [cfg#when-attr] |
| Step | a `when` or `unless` step with `condition:` | a logic statement | include steps by parameter or branch [cfg#when-step] |
| Step | `teardown:` on a `run` | steps | work that must also run on cancel or timeout [cfg#teardown] |

- Expressions and logic statements are evaluated when the config is compiled, before the workflow runs. They read pipeline values and pipeline parameters only, never environment variables. [cfg#logic] [cook#when]
- Declare a pipeline parameter under the top-level `parameters:` before a condition uses it. [cook#when]
- `<< >>` wraps an expression only in fields that do not take one directly. In `filters`, `when` and `unless`, write the expression bare. [cfg#expressions]

## 2. Workflow `when` and `unless`

```yaml
workflows:
  ci:
    when: pipeline.trigger.type != "schedule"
    jobs:
      - build
  nightly:
    when: pipeline.trigger.type == "schedule"
    jobs:
      - full-suite
```

- A workflow with no `when` or `unless` runs in every pipeline. [cfg#when-wf]
- Never mix the two forms. `when: pipeline.event.name == "push" or << pipeline.parameters.deploy >>` is read as one string with template replacement, and the workflow is always created. Write a bare expression, or `<< pipeline.parameters.deploy >>` alone. [cook#when]
- At the workflow level the logic statement goes directly under `when:`. A `condition:` key belongs to `when` steps. The compiler accepts the wrong form (compiler-checked), so a green validation proves nothing here. [cfg#logic]
- Trigger-type values include `schedule`, `api` and `webhook`; some pipeline values exist only for some integrations, so check the availability table before using one. [pv#values]
- The v0.1 planner runs everything when the workflow it prunes has a `when` or `unless` that is not a literal boolean (`docs/spec/v0.1.md`, step 1). Put such conditions on separate workflows, not on the one the planner prunes. (heuristic)

## 3. Logic statements and expressions

| Logic statement | True if | Example |
|---|---|---|
| `and`, `or` | all, or any, arguments are truthy | `or: [<< pipeline.parameters.a >>, << pipeline.parameters.b >>]` |
| `not` | its one argument is not truthy | `not: << pipeline.parameters.dry-run >>` |
| `equal` | all arguments are equal | `equal: [main, << pipeline.git.branch >>]` |
| `matches` | `value` fully matches `pattern` (RE2/J) | `matches: { pattern: "^feature-.*$", value: << pipeline.git.branch >> }` |

- Falsy: false, null, 0, NaN, `""`, and a statement with no arguments. Everything else is truthy. A logic statement with an empty list is a validation error. The maximum depth is 100. [cfg#logic]
- `matches` needs a full match. Anchor the pattern with `^` and `$`. [cfg#logic]

| Expression operator | Meaning |
|---|---|
| `and`, `or` | short-circuit |
| `==`, `!=` | equality; across types `==` is false and `!=` is true |
| `starts-with` | string prefix; an error on a non-string |
| `matches` | `value matches /regex/`; an error on a non-string |
| `>=`, `>`, `<`, `<=` | numeric (`<=` compiles, compiler-checked; the docs table prints it as ⇐) |
| `not` | binds very tightly: write `not (a and b)`, because `not a and b` means `(not a) and b` |

- The docs recommend expressions over logic statements. The two forms are equivalent: `or: [equal: [main, << pipeline.git.branch >>], equal: [staging, << pipeline.git.branch >>]]` is `pipeline.git.branch == "main" or pipeline.git.branch == "staging"`. [cfg#logic]
- Useful pipeline values: `pipeline.git.branch` (empty on a tag), `pipeline.git.tag` (empty on a branch), `pipeline.trigger.type`, `pipeline.parameters.<name>`, `pipeline.number`, `pipeline.project.slug`. [pv#values]

## 4. Job filters

| Need | Style |
|---|---|
| A job with a restricted context (the guardrail bundle reads `filters.branches.only`) | map |
| A job the v0.1 planner must be able to evaluate | map |
| A switch on a parameter, a trigger type or several pipeline values | expression |
| Anything else | keep the style the config already uses |

The docs mark map filters deprecated and prefer expressions. [cfg#filters] Two local facts decide the table: the v0.1 planner reports an expression filter as unknown and leaves the job out of the gate (`docs/spec/v0.1.md`, steps 5 and 6), and the bundle rejects an expression where it wants `filters.branches.only` (tested against `policies/guardrails.rego`).

Map filters:
- `branches` and `tags` each take `only` and `ignore`, as a string or a list. A `/regex/` must match the whole name. `only` is checked before `ignore`. With neither key a job runs on every branch, and is skipped for every tag. [cfg#branches] [cfg#tags]
- No workflow runs for a tag unless a tag filter says so, and every job that a tag job requires needs a tag filter too. If a push carries both a branch and a tag, the branch filters win. [wf#tags] [wf#filters]
- Do not use the old `branches:` key at the job-definition level: workflows ignore it. [wf#branch]

```yaml
workflows:
  release:
    jobs:
      - build:
          filters:
            tags:
              only: /^v.*/
            branches:
              ignore: /.*/
      - publish:
          requires: [build]
          filters:
            tags:
              only: /^v.*/
            branches:
              ignore: /.*/
```

Expression filters:

```yaml
parameters:
  run-docs:
    type: boolean
    default: false

workflows:
  ci:
    jobs:
      - build
      - docs:
          filters: pipeline.git.branch starts-with "docs/" or pipeline.parameters.run-docs
```

## 5. Step conditions

| Want | Write |
|---|---|
| a step that runs even after a failure | `when: always` |
| a step that runs only after a failure | `when: on_fail` |
| the default | `when: on_success` |
| work at cancel or timeout | `teardown:` |
| steps included by parameter or branch | a `when` step with a logic statement |

```yaml
jobs:
  e2e:
    docker:
      - image: cimg/base:2026.11
    steps:
      - checkout
      - run: ./scripts/e2e.sh
      - run:
          name: Keep the logs
          command: tar czf /tmp/logs.tgz logs
          when: always
      - run:
          name: Tell the team
          command: ./scripts/notify.sh
          when: on_fail
      - when:
          condition:
            equal: [main, << pipeline.git.branch >>]
          steps:
            - run: ./scripts/publish-report.sh
```

- `store_artifacts` and `store_test_results` already run after a failed step. They and `when:` do not run if the job is cancelled or times out: use `teardown` for that. [cfg#when-attr]
- `teardown` accepts `run`, `save_cache`, `store_artifacts`, `store_test_results` and `persist_to_workspace`. It does not accept `restore_cache`, `attach_workspace`, a nested `teardown`, a background step, or reruns. [cfg#teardown]
- A `when` step takes a logic statement. The docs say expressions are not supported there. The compiler accepts one (compiler-checked), so do not rely on it. [cfg#when-step]
- These keys live in job definitions. Change them only for a job you own: a layout composer edits workflows.

## 6. Conditions and `requires`

| Situation | Result | Do |
|---|---|---|
| A required job is excluded by a condition | CircleCI ignores it as a dependency [cfg#requires] | fine when intended |
| Every requirement of a job is excluded | the job does not run [cfg#requires] | give a job that must always run no requirements, or unfiltered ones |
| The approval is filtered, the job behind it is not | the job runs unapproved | same filters on both |
| The job is filtered, its approval is not | the workflow is held for nothing | same filters on both |
| A job requires `X: failed` or `X: terminal` and X is excluded | unclear | same filters as X |
| A workflow's `when` is false | the workflow does not run | do not make its check the required one unless the condition is always true (heuristic) |
| A tag push | only jobs with tag filters run, and so must their requirements | filter the whole chain |

Details and the approval example: requires-and-statuses.md §3.

## 7. What the v0.1 planner makes of conditions

| Construct | Planner |
|---|---|
| map filter on `branches` or `tags` | evaluated; a job the filter keeps off the branch is left out, with a warning if it is mandatory |
| string (expression) filter | unknown: reported, and left out of the gate |
| workflow `when` or `unless` that is not a literal boolean | a full run |
| `requires` by a status other than success | the job is unknown; the gate does not wait for it |
| matrix entry | always runs |

Source: `docs/spec/v0.1.md` (steps 1, 3, 5, 6), `docs/limitations.md`.

[cfg#requires]: https://circleci.com/docs/reference/configuration-reference/#requires
[cfg#logic]: https://circleci.com/docs/reference/configuration-reference/#logic-statements
[cfg#expressions]: https://circleci.com/docs/reference/configuration-reference/#using-expressions-in-your-configuration
[cfg#expression-filters]: https://circleci.com/docs/reference/configuration-reference/#expression-based-job-filters
[cfg#filters]: https://circleci.com/docs/reference/configuration-reference/#jobfilters
[cfg#branches]: https://circleci.com/docs/reference/configuration-reference/#branches
[cfg#tags]: https://circleci.com/docs/reference/configuration-reference/#tags
[cfg#when-wf]: https://circleci.com/docs/reference/configuration-reference/#using-when-in-workflows
[cfg#when-attr]: https://circleci.com/docs/reference/configuration-reference/#the-when-attribute
[cfg#when-step]: https://circleci.com/docs/reference/configuration-reference/#the-when-step
[cfg#teardown]: https://circleci.com/docs/reference/configuration-reference/#teardown
[wf#filters]: https://circleci.com/docs/guides/orchestrate/workflows/#using-filters-in-your-workflows
[wf#branch]: https://circleci.com/docs/guides/orchestrate/workflows/#branch-level-job-execution
[wf#tags]: https://circleci.com/docs/guides/orchestrate/workflows/#executing-workflows-for-a-git-tag
[cook#when]: https://circleci.com/docs/guides/orchestrate/orchestration-cookbook/#combine-pipeline-parameters-with-event-filters-in-workflow-conditions
[pv#values]: https://circleci.com/docs/guides/orchestrate/pipeline-variables/#pipeline-values
