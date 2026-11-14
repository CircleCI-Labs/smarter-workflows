# Demo monorepo

A small polyglot monorepo that runs on Smarter Workflows. Every job does real
but fast work, so you can watch the planner pick jobs from a change.

```
services/api      Go, with a handler and a test
services/web      Node, tested with node:test
services/worker   Python, tested with unittest
libs/proto        schema.json, read by the api and the web tests
docs/             Markdown, checked for titles and links
infra/            Terraform files, checked for their shape
scripts/          what the lint, docs, infra, security and e2e jobs run
```

## The pipeline

`.circleci/config.yml` is the setup config. It builds the planner from this
repository, plans `.circleci/continue.yml` (workflow `demo`) and continues the
pipeline with the result.

| Job | Reads |
|---|---|
| `lint` | the source of the three services and the schema, not docs |
| `api-test` | the api and the schema |
| `web-test` | the web service and the schema |
| `web-bundle` | the web service (a job from the `circleci/node` orb) |
| `worker-test` | a matrix over two Python versions, which always runs |
| `docs-check` | `docs/` |
| `infra-validate` | `infra/` |
| `security-scan` | everything; it is mandatory, so it always runs |
| `build`, `e2e` | the api; `e2e` attaches the binary that `build` persists |
| `hold-deploy`, `deploy` | main only, after a person approves |

On `main` every job runs, and its file reads become the impact map. On a branch
only the jobs the change can reach run, plus `smarter-gate`.

The demo keeps its impact map under its own cache key prefix,
`smarter-demo-impact-v1`, because the repository's own pipeline shares the
project cache. The scenarios in `scripts/e2e/scenarios-demo.tsv` check what runs
for each kind of change.
