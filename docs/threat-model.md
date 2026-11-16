# Threat model (v0.3)

Smarter Workflows runs inside a CircleCI project: setup jobs plan which jobs run on a branch, then continue the pipeline. This page is generic; it does not describe any one org’s policies.

## Assets

- **Repository source** — integrity and confidentiality as for any CI job.
- **Continuation config** — defines jobs after setup; may reference contexts and images.
- **Continuation key** — authorizes continuing the pipeline.
- **Learned layer** — JSON in the project cache (AI inputs and history corrections merged at plan time).
- **Plan and analyze reports** — job decisions, analyze completeness, optional AI review.
- **Policy bundle** — Rego guardrails before continuation.

## AI data flow (optional)

When AI is enabled (`learn`, `analyze --ai auto|on`, or `plan --ai-review`):

| Sent | Never sent |
| --- | --- |
| Job definitions (commands, images, working directories) | Paths matching deny rules (`.env*`, `*.pem`, `*.key`, `id_rsa*`, `.npmrc`, `.pypirc`, `.netrc`, …) |
| Repo tree and trimmed file text within token budget | Binaries and files over 256 KiB (listed in tree only) |
| Diffs for plan-time review (add-only) | Content matching deny rules |

Keys live in **CircleCI contexts** the operator attaches to `learn` or `plan`; they are not in the repo. The CLI never logs API keys.

## Learned layer and schedules

Only the **default-branch scheduled `learn` job** writes the learned layer to cache. Feature branches restore the newest matching prefix but do not run `learn` in the recommended layout, so PR content cannot change what gets skipped on other branches.

Plan-time AI review can only **add** jobs; it cannot remove static proof.

## Cache tampering

**Threat:** A branch restores an old or forged learned layer claiming jobs are complete when they are not.

**Mitigations:** Mandatory jobs always run. Jobs without static proof run. Malformed learned JSON is ignored (full run or warning). Restrict contexts (`smarter-workflows-ai`) so only trusted jobs fetch history or call AI.

**Residual risk:** Optional jobs could be skipped until recall checks fail; operators should read learn reports and use shadow mode first.

## Trust boundaries

| Actor | Trust | Reaches |
| --- | --- | --- |
| Scheduled learn on default branch | Semi-trusted | Writes learned layer to cache |
| Feature branch / fork PR | Untrusted for cache | May restore any cache entry |
| Planner binary | Trusted by operator | Reads config, git, cache; writes continuation |
| Anthropic (optional) | Third party | Sees only data the operator enables |
| Policy bundle | Trusted by operator | Evaluated locally |

## Other threats (unchanged)

- **Injection via generated YAML:** gate and key prefixes use safe identifier rules; customer catalog YAML is unchanged.
- **Continuation client:** HTTPS, no redirect follow, key redaction in errors.
- **Policy bundle loading:** walk stays under bundle root; symlinks skipped.

## Related docs

- [Limitations](limitations.md)
- [How it works](how-it-works.md)
- [Analyze](analyze.md)