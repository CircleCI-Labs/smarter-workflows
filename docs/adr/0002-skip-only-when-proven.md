# 2. Skip a job only when the impact map proves the change cannot reach it

Date: 2026-11-13. Status: accepted (open for review in a later grilling round).

## Context

A skipped job that would have failed (a miss) costs more than an extra job that passes. Path filters miss when their globs drift from what jobs really read. Learned models miss when history is thin. GitHub branch protection also waits forever on a required check whose job was skipped.

## Decision

1. Every job runs unless the planner can show that the change touches none of its inputs:
   - no changed file is among the files the job read in the last traced full run;
   - no added or deleted file sits in a directory the job listed.
2. These always run:
   - jobs with no trace, or an incomplete trace;
   - mandatory jobs;
   - every ancestor (`requires`) of a selected job, approvals included.
3. A full run happens:
   - on the default branch and on tags;
   - for scheduled pipelines;
   - when the change touches the pipeline definition (`.circleci/**` by default);
   - when there is no impact map, the diff base can't be found, or the planner hits any error;
   - when the head commit asks for one.
4. Every decision layer added later (history, models) can only *add* jobs.
5. Every workflow the planner prunes ends in one gate job, `smarter-gate`, which requires every selected job that will run. It is a no-op job (`type: no-op`), so it costs no credits. So the workflow always exists and its check always reports:
   - GitHub App projects report one check per workflow, and branch protection requires that check;
   - with per-job statuses, branch protection requires the gate alone.

## Consequences

- Savings come from skipping the parts of the job graph a change cannot reach, such as other services in a monorepo. Shared early jobs, like a repo-wide lint, still run.
- It assumes a job's behaviour depends on the repo files it reads. Inputs from outside the repo (the network, the time, secrets, upstream images) are covered by the full run on the default branch, by mandatory jobs, and by extra inputs a project declares by hand.
- A job that does not run on the branch (its own `filters` leave it out, or all the jobs it requires are left out) is never selected, even when it is mandatory. The planner does not edit the project's filters or requirements to force a job. It leaves such a job out, and a mandatory one gets a warning. It drops a `requires` name only when it names a job that does not run, which CircleCI ignores anyway.
- Skipping a job on confidence alone, without proof, is out of scope until shadow-mode data shows recall of at least 99% for that job.
