**Smarter Workflows, select mode:** 7 of 9 jobs running. Full run: changed path .circleci/continue.yml matches the full-run path .circleci/**.

| Job | Run | Reason |
|---|---|---|
| lint | run | full run |
| security-scan | run | full run |
| api-test | run | full run |
| web-test | run | full run |
| build | run | full run |
| e2e | run | full run |
| docs | run | full run |
| hold-deploy | skip | does not run: its filters leave out branch feature |
| deploy | skip | does not run: its filters leave out branch feature |

**Changed files (1)**

- M .circleci/continue.yml
