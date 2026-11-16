**Smarter Workflows, select mode:** 5 of 9 jobs running.

| Job | Run | Reason |
|---|---|---|
| lint | run | reads services/web/src/footer.ts |
| security-scan | run | mandatory job |
| api-test | skip | inputs unchanged |
| web-test | run | reads services/web/src/footer.ts |
| build | run | reads services/web/src/footer.ts |
| e2e | run | consumes the workspace of build |
| docs | skip | inputs unchanged |
| hold-deploy | skip | does not run: its filters leave out branch feature |
| deploy | skip | does not run: its filters leave out branch feature |

**Changed files (1)**

- A services/web/src/footer.ts
