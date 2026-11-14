**Smarter Workflows, select mode:** 6 of 9 jobs running.

| Job | Run | Reason |
|---|---|---|
| lint | run | reads libs/shared/schema.json |
| security-scan | run | mandatory job |
| api-test | run | reads libs/shared/schema.json |
| web-test | run | reads libs/shared/schema.json |
| build | run | reads libs/shared/schema.json |
| e2e | run | consumes the workspace of build |
| docs | skip | inputs unchanged |
| hold-deploy | skip | does not run: its filters leave out branch feature |
| deploy | skip | does not run: its filters leave out branch feature |

**Changed files (1)**

- M libs/shared/schema.json
