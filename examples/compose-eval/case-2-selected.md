# Case 2: compose the smallest correct workflow for selected jobs

Input: `examples/compose-eval/jobs.yml`.

A change on the branch `feature/greeting` touched `examples/demo/services/api/handler.go`. The planner selected these jobs: lint, api-test, build, e2e, security-scan, and the `worker-test` matrix (`python`: `3.12.16`, `3.13.18`).

Write one workflow named `demo` with exactly those jobs, plus whatever approvals, no-op coordinators and gate a correct layout needs. Add it to a copy of `jobs.yml`.

- `security-scan` is mandatory.
- `e2e` attaches the workspace that `build` persists.
- End with the gate `smarter-gate` (`type: no-op`), which waits for every selected job.
- Don't add `deploy`: it wasn't selected, and it runs on `main` only.

Write the complete config to the output path you are given.
