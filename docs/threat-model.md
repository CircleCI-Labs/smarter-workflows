# Threat model (v0.1)

Smarter Workflows runs inside a customer’s CircleCI project: a setup workflow plans which jobs to run on a branch, then continues the pipeline. This page is generic; it does not describe any one org’s policies.

## Assets

- **Repository source** — integrity and confidentiality as for any CI job.
- **Continuation config** — defines the jobs that run after setup; may reference contexts and images.
- **Continuation key** — secret that authorizes continuing the pipeline (environment variable).
- **Impact map** — gzip JSON (v2) or legacy JSON (v1) in the project cache describing which files each job may read.
- **Plan report** — `plan.json` / `plan.md` with job decisions and diffs.
- **Policy bundle** — Rego guardrails evaluated before continuation.

## Trust boundaries

| Actor | Trust | Reaches |
| --- | --- | --- |
| Default-branch setup job | Semi-trusted | Writes the impact map to cache |
| Feature branch / fork PR | Untrusted for map | May restore a cache entry from any branch |
| Planner binary (setup job) | Trusted by operator | Reads map, config, git; writes continuation config |
| Optional AI classifier | Third party when enabled | Receives job definitions and source contents the operator allows |
| CircleCI continuation API | Trusted endpoint | Receives key + config over HTTPS |
| Policy bundle on disk | Trusted by operator | Evaluated locally; no upload in v0.1 |

## Threats and mitigations

### Forged or hostile impact map

**Threat:** A branch writes a cache entry claiming jobs did not read files they actually use, so the planner skips work.

**Mitigations:** Mandatory jobs always run. Guardrails do not depend on the map. Unusable paths mark a job incomplete (it runs). Oversized or malformed maps are rejected (full run). v2 bounds compressed and decompressed bytes, table sizes, job count, and per-job index counts at parse time (v1 keeps a 16 MiB cap). See ADR 7.

**Residual risk:** A plausible forged map can still skip optional jobs until signing on the default branch ([limitations](limitations.md)). Operators should treat map cache as hint data, not proof.

### Injection via config, map, or YAML emission

**Threat:** Job names, paths, or parameters break out of generated YAML or inlined shell.

**Mitigations:** Gate and map key prefixes match a safe identifier regex. Map job steps quote version and binary path for `/bin/sh`. Job names with expressions trigger a full run.

**Residual risk:** Customer-authored job definitions in the catalog are unchanged; only added smarter steps are constrained.

### Optional AI data flow

**Threat:** Source contents or job definitions sent to an AI provider leak secrets or expand the attack surface.

**Mitigations and operator choices:** AI is optional. Paths-only modes limit what is sent. Confidence and job-hash checks gate skips that rely on AI-only coverage (ADR 8). The analyze command documents what leaves the runner when AI is on.

**Residual risk:** Operators who enable full-source classification must treat the provider like any other subprocess with repo access. Details of the AI data flow are described alongside `circleci-smarter analyze`.

### Continuation client

**Threat:** Key or config leaks via logs, redirects, or error messages.

**Mitigations:** Errors name HTTP status only. Response body not logged. No redirect follow (key stays on intended host). HTTPS required except loopback. Config size capped at 8 MiB. Key redaction if ever present in an error string.

**Residual risk:** Operator misconfiguration (HTTP to a non-loopback host) is rejected at client construction.

### Policy bundle loading

**Threat:** Symlink or path traversal loads Rego from outside the bundle directory.

**Mitigations:** Walk stays under bundle root; symlinks skipped; `*_test.rego` ignored. Hostile YAML in config fails `Check` without executing customer code.

**Residual risk:** Evaluated config is still customer YAML interpreted by circle-policy-agent; bundle should be operator-maintained.

### CLI output paths

**Threat:** Odd `--out` / `--report-dir` values or world-readable continuation config.

**Mitigations:** Null-byte paths rejected. Continuation config written `0600`; plan report files `0640` in a `0750` directory; impact map `0640`.

**Residual risk:** Paths are absolute or relative as the operator gives them; the CLI does not sandbox writes outside the workspace.

## Related docs

- [Limitations](limitations.md) — forged map and other accepted gaps
- [How it works](how-it-works.md) — data flow
- [Spec v0.1](spec/v0.1.md) — formats and algorithm