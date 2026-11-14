# Smarter Workflows

> CircleCI Labs experiment. Private preview, not supported.

A planner for CircleCI setup workflows. For each change it runs only the jobs that change can affect, and it proves its config against your guardrails (config policies) before it continues the pipeline.

- **Learns from your default branch.** Every full run there traces the repo files each job reads, and builds an *impact map*. Branches use the map: a job whose inputs a change doesn't touch is skipped.
- **Safe by construction.** It skips a job only when the map proves the change can't reach it. Mandatory jobs always run. Anything unusual means a full run. One `smarter-gate` job is the single required check, so skipped jobs never leave a PR waiting.
- **Native.** A setup workflow, the continuation API, config policies and orbs. The catalog is your own `continue.yml`, with no new schema. One Go binary, which also works as `circleci smarter`.
- **Guardrails first.** Every plan is checked locally against the same Rego bundle you can enforce org-wide. It repairs what it can (e.g. a missing security scan) and falls back to a full run otherwise.

Start in **shadow mode**: plan and report, but run everything. Switch to **select mode** when the reports convince you.

| | |
|---|---|
| Spec | [docs/spec/v0.1.md](docs/spec/v0.1.md) |
| Decisions | [docs/adr/](docs/adr/) |
| Glossary | [GLOSSARY.md](GLOSSARY.md) |
