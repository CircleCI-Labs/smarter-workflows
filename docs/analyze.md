---
page-title: Analyze
page-audience: admin
page-description: Build impact maps from CircleCI config and repository sources without runtime tracing.
---

# Analyze

`circleci-smarter analyze` infers which repository paths can reach each job. v0.3 uses this map instead of trace injection.

## What it reads

Static signals from the config and checkout: `run` commands and scripts they invoke, Node and Python manifests, Dockerfile `COPY`, `working_directory`, orb parameters such as `app-dir`, cache-key checksum files, workspace flow, path-filtering mappings, and Go imports (`go/parser`, including tests; module, `go.work`, `replace`, `//go:embed`, and `vendor/`).

## AI classifier

With `--ai auto` (default), analyze calls Anthropic when `ANTHROPIC_API_KEY` or `ANTHROPIC_AUTH_TOKEN` is set. Modes: `on` (failure stops the run), `off` (static and history only), `dry-run` (writes request JSON under `<report-dir>/ai-requests/` without sending).

The prompt sends job definitions, a repo tree, and trimmed file text within `--ai-max-input-tokens` (default 90k). Structured JSON is enforced with `output_config.format`.

**Never sent:** paths matching `.env*`, `*.pem`, `*.key`, `id_rsa*`, credential/secret filenames, `.npmrc`, `.pypirc`, `.netrc`, binaries, or files over 256 KiB (listed in the tree only).

## Learned layer

`--learned-out` writes version-1 JSON: per job `job_hash`, optional AI inputs with confidence, and history **corrections** (paths added when a non-flaky failure’s changed files were outside the map). `--learned` merges corrections forward and drops paths that no longer exist. AI may mark a job complete only when `job_hash` matches and `confidence >= --min-confidence`.

## Report

`--report` and `--report-json` list each job’s completeness, why it is incomplete, and inputs grouped by config signal. Totals include AI call counts, token use, and estimated cost (Haiku 5.5 tier; prices as of 2026-10).

## Run locally

Paths such as `--config` and `--history` are relative to `--repo` (the repository root you are analyzing). To analyze the demo config from this repository, run from the repo root:

```bash
circleci-smarter analyze --repo . --config examples/demo/.circleci/continue.yml --workflow demo --ai off
circleci-smarter analyze --repo . --ai off --report analyze-report.md
```

Map output defaults to `smarter-map.json` in the current working directory (where you run the command).

Developers can add recall/precision against a reference map with `--truth FILE` (omitted from routine operator docs).