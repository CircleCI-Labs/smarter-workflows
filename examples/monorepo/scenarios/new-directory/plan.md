**Smarter Workflows, select mode:** 4 of 9 jobs running.

| Job | Run | Reason |
|---|---|---|
| lint | run | reads services/billing/main.go |
| security-scan | run | mandatory job |
| api-test | skip | inputs unchanged |
| web-test | skip | inputs unchanged |
| build | run | reads services/billing/main.go |
| e2e | run | consumes the workspace of build |
| docs | skip | inputs unchanged |
| hold-deploy | skip | does not run: its filters leave out branch feature |
| deploy | skip | does not run: its filters leave out branch feature |

**Changed files (1)**

- A services/billing/main.go
