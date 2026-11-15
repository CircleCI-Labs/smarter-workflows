---
page-title: Backtest
page-audience: admin
page-description: Recall and job-time savings when the planner is replayed against historical CI runs, and the limits of that replay.
---

# Backtest

This page will hold backtest results: for each historical commit, what the planner would have selected versus the jobs that actually ran, overall recall for failed jobs, and the job-time savings when skipped jobs are summed.

The command is `circleci-smarter backtest`, and `scripts/bench/fetch-history.sh` builds the history JSON from CircleCI and git. Until the first backtest run lands on `main`, there is no report here.

Replaying with today's impact map against older commits is approximate. The finished page will say which limits apply and how flaky failures are treated.