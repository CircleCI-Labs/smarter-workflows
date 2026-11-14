# Tools

Which tool answers which question, and how to call it. Tags such as [docs#md] are sources; the URLs are at the end of the file.
Never print environment variables, tokens or secrets while you run any of these.

## 1. Which tool for which question

| Question | Tool |
|---|---|
| Which jobs exist, and what do they persist, attach, use? | `circleci-smarter catalog` (§4) |
| Is the layout sound? | `circleci-smarter layout check` (§4) |
| Does it break a guardrail? | `circleci-smarter policy check` (§4) |
| Does it compile? | `circleci config validate FILE` (§5) or MCP `validate_config` (§3) |
| What does CircleCI document about X? | docs MCP `search_circle_ci_knowledge_sources` (§2) |
| How long do the jobs take? | CircleCI MCP `list_runs`, `list_run_workflows`, `list_workflow_jobs` (§3) |
| Which tests are flaky? | CircleCI MCP `list_flaky_tests` (§3) |
| What does an orb job take? | CircleCI MCP `get_orb_source` (§3) |

## 2. The docs MCP: `search_circle_ci_knowledge_sources`

- One argument, `query`: a complete sentence, for example "How does a job in a workflow wait for a job with a failed status?". It returns a fixed number of ranked chunks, each with its `source_url`, even when none is relevant. Check that a chunk answers the question before you use it.
- Search before you write a rule from memory, and cite the `source_url` you relied on.
- When a result is thin or ambiguous, fetch the live page. CircleCI serves every docs page as Markdown at `<page URL>/index.md`, for example `https://circleci.com/docs/reference/configuration-reference/index.md`. [docs#md] That page is about 140 KB: save it to a fresh scratch directory and read the section you need with `grep -n -A30`, instead of loading it whole.
- Keep every query generic: no customer, repository or internal names, tokens or file contents. Search for the error text or the key name.

## 3. The CircleCI MCP

Load the `write-config` skill first: `get_skill` with `skill: "write-config"`, then `reference: "references/orbs.md"`. It covers orbs, functions and the validate loop. This skill does not repeat it.

| Tool | Arguments | Use it to |
|---|---|---|
| `validate_config` | `config` (the whole file; required), `org`, `pipeline_parameters`, `pipeline_values`, `preview_next` | compile the file. Pass `org` when the config has private or URL orbs: without it those fail with false errors. Validate the whole file, not a fragment. |
| `get_orb_source` | `orb`, as `namespace/name@x.y.z` | read an orb job's parameters before you put it in an entry. Registry orbs only. An unknown key on an orb job can be silently ignored. |
| `list_functions`, `get_function` | `function` | find a setup step (Node, Go, Docker and so on) for a job. This is step-level work, not layout: leave it to the job's owner. |
| `list_runs` | `project` (`gh/org/repo` or a UUID), `branch`, `status`, `limit` | find green runs of the default branch |
| `list_run_workflows` | `run` (a run UUID) | get the workflow ids of a run |
| `list_workflow_jobs` | `workflow` (a workflow UUID) | get each job's `name`, `type`, `outcome`, `started_at`, `ended_at` |
| `get_job` | `job` (a job UUID) | get step-level timings and exit codes. The output is large: use it only to find which step makes one job slow. |
| `list_flaky_tests` | `project`, `sort`, `limit` | find tests that failed and then passed in the same pipeline: the candidates for automatic reruns |

Durations:
1. `list_runs` with `branch` set to the default branch, `status: "success"`, `limit: 3`.
2. For each run, `list_run_workflows`, then `list_workflow_jobs` for the workflow you are laying out.
3. A job's duration is `ended_at` minus `started_at`. Take the median per job name over the runs.
4. Use the numbers in shaping-patterns.md §5. If there are no runs, say so and lay the workflow out without durations.

## 4. The local CLI: `circleci-smarter`

`catalog` and `layout check` are being built at the same time as this skill. What follows is the agreed contract; `circleci-smarter <command> --help` wins if the two differ. The field names of the JSON and the finding codes belong to the commands, so this skill does not list them (unverified).

### `catalog`

```
circleci-smarter catalog --config .circleci/continue.yml [--map impact-map.json] [--workflow NAME]
```

| Flag | Meaning |
|---|---|
| `--config F` | the project's full pipeline config |
| `--map M` | the impact map, when one exists (the file `plan --map` reads) |
| `--workflow W` | the workflow to inventory, when the config has several |

It prints a JSON inventory of the jobs on stdout. Read one real output before you wire anything. It is the only source of job names: if a job you need is not in it, stop and report that; do not invent it.

### `layout check`

```
circleci-smarter layout check --config F [--catalog C] [--policy-bundle D] [--mandatory LIST] [--restricted-contexts LIST]
```

| Flag | Meaning |
|---|---|
| `--config F` | the config that holds your layout (jobs and workflows) |
| `--catalog C` | the inventory JSON from `catalog`, to compare your layout with |
| `--policy-bundle D` | the guardrail directory (`policies`) |
| `--mandatory LIST` | comma-separated mandatory job names: the `required_job_names` setting of the bundle |
| `--restricted-contexts LIST` | comma-separated restricted context names: the keys of `context_restrictions` in the bundle |

It prints JSON findings; each has a `code`. Read the JSON, not only the exit status. Fix the cause of every finding and run it again until the list is empty. The table at the end of this section maps a finding's topic to a reference.

### `policy check`

```
circleci-smarter policy check --config F --bundle policies --format json
```

Prints `{"status": ..., "violations": [...]}`; each violation has `rule` and `message` (and `require_jobs` when selecting a job fixes it). Exit status: 0 for a pass or soft failures only, 1 for an error, 2 for a usage error, 3 for a hard violation. The settings of the bundle (mandatory jobs, restricted contexts, resource classes) are the constants at the top of `policies/guardrails.rego`.

### Reading a finding

| The finding is about | Usual cause | Fix | Read |
|---|---|---|---|
| a `requires` name with no entry, a cycle, a duplicate name | a typo, a renamed job, one job called twice | use the entry's name; add `name:` | requires-and-statuses.md §1 |
| a job that cannot run | every requirement is filtered out | give it requirements that run, or matching filters | requires-and-statuses.md §3 |
| an approval and the job behind it | different filters, or another requirement on the job | same filters; the job requires only the approval | requires-and-statuses.md §3 |
| a restricted context | no approval upstream, no branch limit, or the wrong job | approval upstream; `filters.branches.only` in map form | conditions.md §4, shaping-patterns.md §6 |
| a missing mandatory job | absent, filtered, or inside a job group | a plain entry, unfiltered, no requires | SKILL.md rules |
| the gate | missing, filtered, or requiring something behind the approval | shaping-patterns.md §3 | |
| workspace producer and consumer | the consumer is not downstream, or two paths collide | data-flow.md §3 | |
| a status requirement | a non-success status on a check | requires-and-statuses.md §2 | |

Never clear a finding by weakening the layout: do not drop a mandatory job, an approval, a context restriction or a branch filter, and do not edit `policies/`. If a finding stays after three repairs, stop and report it.

## 5. `circleci config validate`

```
circleci config validate path/to/config.yml [--org gh/myorg] [--json]   # CLI 1.6.0
```

- It compiles the whole file the way the platform does, and uses the network. Pass the org flag for private or URL orbs: it is `--org` in CLI 1.6.0 and `--org-slug` or `--org-id` in older references, so check `circleci config validate --help`. [cli#config]
- `circleci config process FILE` prints the compiled config: use it to see how a matrix or a job group expands.
- A green result means the file compiles. It does not mean the layout is right: it accepts an approval that shares a name with a job, a `condition:` key at the workflow level, an expression inside a `when` step, and a filtered-out approval that leaves a job ungated. Run the layout and guardrail checks as well.

[docs#md]: https://circleci.com/docs/llms.txt
[cli#config]: https://circleci-public.github.io/circleci-cli/#circleci-config-command
