# Glossary

The words this project uses, and what each one means. There are no implementation details here. For those, see the [spec](docs/spec/v0.1.md) and the [decisions](docs/adr/).

**Change**
: The set of files a branch changes, compared with the commit it branched from on the default branch.

**Planner**
: The step in the setup workflow that decides, for one change, which jobs of the pipeline run.

**Plan**
: What the planner decides for one pipeline: the jobs that run, the jobs that are skipped and the reason for each, and the continuation config that carries this out.

**Full run**
: A pipeline that runs every job. The default branch, scheduled pipelines, changes to the pipeline definition, and any case the planner is unsure about all get a full run.

**Impact map**
: For each job, the repo files and directories the planner treats as its inputs. `circleci-smarter analyze` builds it from the continuation config and the repository. Branches use the map to see which jobs a change can affect.

**Inputs (of a job)**
: The files and directories listed for a job in the impact map. A job whose inputs a change does not touch is a candidate to skip.

**Shadow mode**
: The planner makes and reports a plan, but every job still runs. Shadow mode measures what select mode would save, and what it would miss.

**Select mode**
: The planner runs only the jobs its plan selects.

**Compose mode**
: A later mode: an agent builds jobs and workflows from approved building blocks, within the guardrails.

**Mandatory job**
: A job that runs in every pipeline, whatever the change, such as a security scan.

**Gate job**
: The last job of the workflow, which waits for every selected job. It always runs, so a branch protection rule can require it alone, even when other jobs are skipped. It is a no-op job: no executor, no steps, no credits.

**No-op job**
: A job defined with `type: no-op`. It runs nothing and costs no credits. It succeeds when the jobs it requires succeed.

**Gatekeeper**
: A no-op job that no other job requires, which a project uses as its one required status check (for example `all-tests-passed`). Select mode always keeps it, and cuts what it waits for to the jobs that stay.

**Layout**
: How the jobs of one workflow wait for each other (`requires`, approvals, filters), as opposed to what each job does. `layout check` reports what is wrong with it.

**Guardrail**
: A config policy that every generated config must pass. Examples: mandatory jobs, contexts allowed only on certain jobs and branches, an approval before a deploy.

**Pre-flight**
: The planner's own check of its config against the guardrails, before it continues the pipeline.

**Repair**
: A change the planner makes to its plan so that it passes a guardrail, such as adding a missing mandatory job. A failure it cannot repair turns the plan into a full run.

**Miss**
: A job the plan skipped (or, in shadow mode, would have skipped) that would have failed.

**Savings**
: The credits and minutes that skipped jobs did not spend.

**Catalog**
: The jobs, commands, executors and orbs the planner may pick from. In select mode, the catalog is the project's own pipeline config.
