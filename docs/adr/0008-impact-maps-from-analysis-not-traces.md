# 8. Impact maps from analysis, not traces

Date: 2026-11-16. Status: accepted.

## Context

Tracing needs a step injected into every job. It only works where file access times work and where the image has the tools. On a real 39-job project only 4 jobs produced complete traces: images without git, and runner or machine jobs, were never traced. Users will not edit their jobs before they know the tool is worth it. From v0.3 the impact map comes from `circleci-smarter analyze`: static analysis of the config and the repo, an optional AI classifier, and corrections mined from past runs. Tracing goes away completely, which also takes a lot of complexity with it. v0.x allows breaking changes; the trace flags simply disappear.

## Decision

1. The map comes from `circleci-smarter analyze`, from three sources:
   - static analysis of the config and the repo (run commands and the scripts they call, Go imports, Node and Python manifests, Dockerfile COPY, `working_directory`, orb parameters such as `app-dir`, cache-key checksum files, workspace data flow, path-filtering mappings);
   - an optional AI classifier, given the job definitions, the repo tree and the source contents, which returns inputs with a confidence;
   - corrections mined from past runs: a job that failed on a change its map could not see gains that change's paths, unless the failure was flaky.
2. The static part is computed fresh in the setup job. A *learned layer* (the AI inputs and the corrections) is refreshed by a weekly `learn` job.
3. No existing job is edited.
4. A job is skipped only when proven:
   - static analysis understood every step of the job: each command is a recognized tool, a benign builtin or a script it followed, and each input resolved to a path that exists at the revision. A step it does not understand (an unknown binary, make, an orb command outside the known table) makes the job unknown;
   - coverage that comes only from the AI needs a confidence of at least the threshold (0.8 by default), a job hash that matches the current job definition, and at least one valid input;
   - otherwise the job is unknown and runs.
5. Shadow mode first, and a backtest on real history before select mode.

## Consequences

- onboarding needs no job edits, and works on every executor and image;
- the map can be built and checked locally before anything changes in CI;
- static analysis misses dynamic inputs (paths built from environment variables, network fetches): the AI layer, the history corrections and shadow mode cover that;
- source contents go to the AI provider when the AI is on (see the threat model);
- the trace flags are gone in v0.3.