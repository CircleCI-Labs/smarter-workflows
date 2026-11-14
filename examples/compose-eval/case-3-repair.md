# Case 3: repair a broken layout

Input: `examples/compose-eval/case-3-broken.yml`, the demo's jobs with a broken workflow `demo`.

1. Run `circleci-smarter layout check` on the file, with `--mandatory security-scan --restricted-contexts deploy --policy-bundle policies`.
2. Run `circleci config validate` on it.
3. Fix every problem they report, and run both again until they pass.

Never fix a problem by removing a mandatory job, an approval, a context or a branch filter, and never edit `policies/`.

Write the complete repaired config to the output path you are given. List each problem and its fix.
