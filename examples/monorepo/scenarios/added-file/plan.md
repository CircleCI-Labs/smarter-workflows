**Smarter Workflows, select mode:** 5 of 9 jobs running.

| Job | Run | Reason |
|---|---|---|
| lint | run | lists services/web/src, where services/web/src/footer.ts was added |
| security-scan | run | mandatory job |
| api-test | skip | inputs unchanged |
| web-test | run | lists services/web/src, where services/web/src/footer.ts was added |
| build | run | lists services/web/src, where services/web/src/footer.ts was added |
| e2e | run | consumes the workspace of build |
| docs | skip | inputs unchanged |
| hold-deploy | skip | does not run: its filters leave out branch feature |
| deploy | skip | does not run: its filters leave out branch feature |

**Changed files (1)**

- A services/web/src/footer.ts
