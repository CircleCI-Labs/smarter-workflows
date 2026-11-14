# Compose eval

Can an agent with the [workflow composer skill](../../skills/circleci-workflow-composer/SKILL.md) assemble CircleCI jobs into a correct workflow? Each case gives it the demo's job definitions ([jobs.yml](jobs.yml), the demo's `continue.yml` without its workflows) and a goal. It answers with a complete config: the jobs as given, plus the `workflows:` it composed.

[check.sh](check.sh) then judges each answer, with no model involved:

1. `circleci config validate`;
2. `circleci-smarter layout check`, with mandatory `security-scan` and restricted context `deploy`;
3. `circleci-smarter policy check` against `policies/`;
4. the case's own assertions (which jobs must be in it, and in what order).

| Case | Goal |
|---|---|
| [1](case-1-full.md) | compose the full workflow for main and branches, from scratch |
| [2](case-2-selected.md) | compose the smallest correct workflow for a set of selected jobs |
| [3](case-3-repair.md) | repair a broken layout ([case-3-broken.yml](case-3-broken.yml)) from the checker's findings |
