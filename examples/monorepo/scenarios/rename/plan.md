**Smarter Workflows, select mode:** 5 of 9 jobs running.

| Job | Run | Reason |
|---|---|---|
| lint | run | reads services/api/handler.go |
| security-scan | run | mandatory job |
| api-test | run | reads services/api/handler.go |
| web-test | skip | inputs unchanged |
| build | run | reads services/api/handler.go |
| e2e | run | consumes the workspace of build |
| docs | skip | inputs unchanged |
| hold-deploy | skip | does not run: its filters leave out branch feature |
| deploy | skip | does not run: its filters leave out branch feature |

**Changed files (2)**

- D services/api/handler.go
- A services/api/routes.go
