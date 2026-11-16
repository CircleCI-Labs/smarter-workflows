# Security policy

## Reporting a vulnerability

**Do not open a public issue or pull request for a security vulnerability.**

Email [security@circleci.com](mailto:security@circleci.com), whether the problem is in Smarter Workflows or in the CircleCI platform. CircleCI's security team receives the report. The project sets no response target, and CircleCI has no bounty program ([process](https://circleci.com/security/)).

For a sensitive report, encrypt the message with the key at <https://circleci.com/.well-known/pgp-key.txt>.

Include the steps to reproduce, the release or commit, and the planner version (`circleci-smarter version`).

## Supported versions

Smarter Workflows is at version `0.x`. Fixes land on `main` and ship in the next release. The code and the design have not been independently reviewed or penetration-tested.

## Support

Smarter Workflows is a CircleCI Labs project. CircleCI support does not cover it, and field engineers handle bugs and feature requests on a best-effort basis, with no SLA. Use [GitHub issues](https://github.com/CircleCI-Labs/smarter-workflows/issues) for problems that are not security vulnerabilities.

## What the planner does with your credentials and your repository

[Limitations](docs/limitations.md) lists the known gaps in detail.

- The continuation key (`CIRCLE_CONTINUATION_KEY`) is read only by `circleci-smarter continue`. It never prints the key or the request body, only the status code.
- The impact map lists repo paths each job may read. It is built by `circleci-smarter analyze`, not by extra steps in customer jobs. Optional AI classification may send source contents to the provider; see the threat model.
- The impact map lives in the project cache, and any branch can write a cache entry. A forged map could make the planner skip jobs. Mandatory jobs and the guardrails do not depend on the map, and every default branch run is a full run. Signing the map on the default branch is planned.
- The guardrail bundle runs locally in the setup job, against the config that the planner generated. The same bundle can be enforced org-wide as a config policy.
