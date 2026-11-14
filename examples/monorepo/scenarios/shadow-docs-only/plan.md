**Smarter Workflows, shadow mode:** every job runs; select mode would run 3 of 9.

| Job | Select mode | Reason |
|---|---|---|
| lint | run | reads docs/guide.md |
| security-scan | run | mandatory job |
| api-test | skip | inputs unchanged |
| web-test | skip | inputs unchanged |
| build | skip | inputs unchanged |
| e2e | skip | inputs unchanged |
| docs | run | reads docs/guide.md |
| hold-deploy | skip | does not run: its filters leave out branch feature |
| deploy | skip | does not run: its filters leave out branch feature |

**Changed files (1)**

- M docs/guide.md
