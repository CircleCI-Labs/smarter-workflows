# Case 1: compose the full workflow

Input: `examples/compose-eval/jobs.yml`, the demo's jobs with no workflows.

Write one workflow named `demo` that runs on every branch, and add it to a copy of `jobs.yml`:

- Run these jobs: lint, api-test, web-test, docs-check, infra-validate, security-scan, build, e2e, deploy.
- Add `worker-test` as a matrix over the parameter `python` with the values `3.12.16` and `3.13.18`.
- Add the orb job `node/run`, named `web-bundle`, with these parameters:
  - `executor: {name: node/default, tag: "24.22.1", resource_class: small}`
  - `app-dir: ~/project/examples/demo/services/web`
  - `npm-run: bundle`
- `e2e` attaches the workspace that `build` persists.
- `security-scan` is mandatory.
- `deploy` uses the restricted context `deploy`. It may run only on `main`, only after a manual approval, and only after every other job has passed.
- End with the gate `smarter-gate` (`type: no-op`), which waits for every check that runs on every branch, and for nothing behind the approval.
- Keep the critical path short: no edge without a reason.

Write the complete config to the output path you are given.
