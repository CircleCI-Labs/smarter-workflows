**Smarter Workflows, select mode:** 2 of 9 jobs running.

| Job | Run | Reason |
|---|---|---|
| lint | run | reads scripts/deploy.sh |
| security-scan | run | mandatory job |
| api-test | skip | inputs unchanged |
| web-test | skip | inputs unchanged |
| build | skip | inputs unchanged |
| e2e | skip | inputs unchanged |
| docs | skip | inputs unchanged |
| hold-deploy | skip | does not run: its filters leave out branch feature |
| deploy | skip | does not run: its filters leave out branch feature |

**Changed files (1)**

- D scripts/deploy.sh
