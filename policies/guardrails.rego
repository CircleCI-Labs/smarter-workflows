package org

import data.circleci.utils
import future.keywords

# The Smarter Workflows guardrail bundle. The planner evaluates it locally
# against every config it generates, and the same bundle can be pushed as
# org policy (`circleci policy push`).
#
# Everything under "Settings" is meant to be edited per project. The rest
# implements the rules; each rule is a set of messages, and a rule that
# produces no message passes.
#
# Messages may carry a repair code. `[smarter:require-job=NAME]` tells the
# planner that selecting the job NAME fixes the violation.

policy_name["smarter_guardrails"]

# =============================================================================
# Settings
# =============================================================================

# Rules that block the pipeline when they fail (HARD_FAIL). Move a name to
# soft_rules to report it without blocking (SOFT_FAIL), or delete it to switch
# the rule off. When you first push this bundle as org policy, start with every
# rule in soft_rules.
hard_rules := {
	"required_jobs",
	"restricted_contexts",
	"resource_classes",
	"registry_orbs_pinned",
	"url_orbs_pinned",
	"no_setup_in_continuation",
}

soft_rules := set()

# required_jobs: jobs that every workflow of a non-setup config must contain.
# A job is matched by its name in the workflow (`name:` if set, else the job
# name). Setup configs are exempt.
required_job_names := {"security-scan"}

# restricted_contexts: contexts that only some jobs may use. For each context:
#   jobs              job definitions (the key under `jobs:`, or `orb/job`) that
#                     may use it
#   branches          the branches those jobs must be limited to with
#                     `filters.branches.only`; compared as written, so a regex
#                     filter is allowed only if it is listed verbatim
#   allow_tags        true if they may also run on tags (`filters.tags`)
#   require_approval  true if they must run downstream of an approval job
context_restrictions := {
	"deploy": {
		"jobs": {"deploy"},
		"branches": {"main"},
		"allow_tags": true,
		"require_approval": true,
	},
	"smarter-workflows-release": {
		"jobs": {"release", "orb-publish-dev", "orb-publish-prod", "docs-site-publish"},
		"branches": {"main"},
		"allow_tags": true,
		"require_approval": false,
	},
}

# resource_classes: the resource_class values jobs and executors may use. A job
# that sets none runs on its executor's default class and is not checked.
allowed_resource_classes := {"small", "medium", "medium+", "large"}

# =============================================================================
# Which rules are on
# =============================================================================

enable_hard[name] {
	hard_rules[name]
}

enable_rule[name] {
	soft_rules[name]
}

# =============================================================================
# 1. required_jobs: every workflow contains the required jobs
# =============================================================================

required_jobs[msg] {
	not input.setup
	some wf, workflow in input.workflows
	is_object(workflow)
	some required in required_job_names
	not workflow_has_job(workflow, required)
	msg := sprintf("workflow %q is missing the required job %q [smarter:require-job=%s]", [wf, required, required])
}

workflow_has_job(workflow, name) {
	some entry in workflow.jobs
	entry_name(entry) == name
}

# =============================================================================
# 2. restricted_contexts: restricted contexts only on listed jobs, listed
#    branches or tags, and downstream of an approval job
# =============================================================================

# The job is not one of the jobs allowed to use the context.
restricted_contexts[msg] {
	restricted_context_uses[[wf, name, ref, _, context]]
	not context_restrictions[context].jobs[ref]
	msg := sprintf(
		"workflow %q: job %s uses the restricted context %q; allowed jobs: %s",
		[wf, job_label(name, ref), context, concat(", ", object.get(context_restrictions[context], "jobs", set()))],
	)
}

# The job can run on branches other than the listed ones.
restricted_contexts[msg] {
	restricted_context_uses[[wf, name, ref, cfg, context]]
	branches := object.get(context_restrictions[context], "branches", set())
	not limited_to_branches(cfg, branches)
	msg := sprintf(
		"workflow %q: job %s uses the restricted context %q and must be limited to the branches [%s] (filters.branches.only)",
		[wf, job_label(name, ref), context, concat(", ", branches)],
	)
}

# The job can run on tags, which this context does not allow.
restricted_contexts[msg] {
	restricted_context_uses[[wf, name, ref, cfg, context]]
	not object.get(context_restrictions[context], "allow_tags", false)
	runs_on_tags(cfg)
	msg := sprintf(
		"workflow %q: job %s uses the restricted context %q and must not run on tags (filters.tags)",
		[wf, job_label(name, ref), context],
	)
}

# The job does not run downstream of an approval job.
restricted_contexts[msg] {
	restricted_context_uses[[wf, name, ref, _, context]]
	object.get(context_restrictions[context], "require_approval", false)
	not downstream_of_approval(input.workflows[wf], name)
	msg := sprintf(
		"workflow %q: job %s uses the restricted context %q and must run downstream of an approval job (type: approval, in its requires)",
		[wf, job_label(name, ref), context],
	)
}

# A parameterized context name could expand to a restricted context.
restricted_contexts[msg] {
	some wf, workflow in input.workflows
	is_object(workflow)
	some entry in workflow.jobs
	some context in utils.to_set(entry_cfg(entry).context)
	is_string(context)
	utils.is_parameterized_expression(context)
	msg := sprintf(
		"workflow %q: job %q uses the parameterized context %q, which cannot be checked against the restricted contexts",
		[wf, entry_name(entry), context],
	)
}

# Each use of a restricted context by a job that runs code (approval jobs run
# none): [workflow, job name, job definition, job config, context].
restricted_context_uses[[wf, name, ref, cfg, context]] {
	some wf, workflow in input.workflows
	is_object(workflow)
	some entry in workflow.jobs
	cfg := entry_cfg(entry)
	not approval_entry(entry)
	some context in utils.to_set(cfg.context)
	context_restrictions[context]
	ref := entry_ref(entry)
	name := entry_name(entry)
}

# The job runs on no branch outside `allowed`: its branch filter lists only
# allowed branches, or it ignores every branch.
limited_to_branches(cfg, allowed) {
	only := filter_set(cfg, "branches", "only")
	count(only) > 0
	count(only - allowed) == 0
}

limited_to_branches(cfg, _) {
	"/.*/" in filter_set(cfg, "branches", "ignore")
}

# CircleCI runs a job on tags only if it has a tags filter.
runs_on_tags(cfg) {
	count(filter_set(cfg, "tags", "only")) > 0
}

runs_on_tags(cfg) {
	ignored := filter_set(cfg, "tags", "ignore")
	count(ignored) > 0
	not "/.*/" in ignored
}

filter_set(cfg, kind, key) := utils.to_set(cfg.filters[kind][key]) if {
	cfg.filters[kind][key]
} else := set()

# Some `requires` ancestor of the job is an approval job.
downstream_of_approval(workflow, name) {
	ancestors := graph.reachable(requires_graph(workflow), {name})
	count(ancestors & approval_jobs(workflow)) > 0
}

approval_jobs(workflow) := {name |
	some entry in workflow.jobs
	approval_entry(entry)
	name := entry_name(entry)
}

# Job name -> the names it requires. Every job is a key, because
# graph.reachable ignores nodes that are not.
requires_graph(workflow) := {name: requires |
	some name in workflow_job_names(workflow)
	requires := {required |
		some entry in workflow.jobs
		entry_name(entry) == name
		some item in utils.to_array(object.get(entry_cfg(entry), "requires", []))
		required := utils.get_element_name(item)
	}
}

workflow_job_names(workflow) := {name |
	some entry in workflow.jobs
	name := entry_name(entry)
}

# =============================================================================
# 3. resource_classes: resource classes come from the allowlist
# =============================================================================

# Checked on the source config. A parameterized class is skipped here: it is
# only known in `_compiled_`, which exists when CircleCI evaluates the policy
# but not in the planner's local check.
resource_classes[msg] {
	some name, job in input.jobs
	class := disallowed_class(job)
	msg := class_message("job", name, class)
}

resource_classes[msg] {
	some name, executor in input.executors
	class := disallowed_class(executor)
	msg := class_message("executor", name, class)
}

# Checked on the compiled config, which also has the jobs that come from orbs,
# and the values that parameters set.
resource_classes[msg] {
	some name, job in input._compiled_.jobs
	class := disallowed_class(job)
	msg := class_message("job", name, class)
}

# A no-op job has no executor and costs no credits, so its resource class (if it
# has one) is never used and is not checked.
disallowed_class(definition) := class {
	not no_op_job(definition)
	class := definition.resource_class
	is_string(class)
	not utils.is_parameterized_expression(class)
	not allowed_resource_classes[class]
}

no_op_job(definition) {
	definition.type == "no-op"
}

class_message(kind, name, class) := sprintf(
	"%s %q uses the resource_class %q, which is not allowed (allowed: %s)",
	[kind, name, class, concat(", ", allowed_resource_classes)],
)

# =============================================================================
# 4. registry_orbs_pinned: registry orbs are pinned to an exact x.y.z
# =============================================================================

# Not the helpers in data.circleci.config: config.orbs is keyed by orb name, so
# two aliases of one orb at different versions are a conflict error, and it
# skips URL orbs.
registry_orbs_pinned[msg] {
	some alias, orb in input.orbs
	is_string(orb)
	not is_url_orb(orb)
	not regex.match(`^[^@/\s]+/[^@/\s]+@[0-9]+\.[0-9]+\.[0-9]+$`, orb)
	msg := sprintf("orb %q (%s) is not pinned to an exact x.y.z version", [alias, orb])
}

# =============================================================================
# 5. url_orbs_pinned: URL orbs carry a 40-hex commit SHA
# =============================================================================

# The message names the alias and not the URL, which may carry credentials.
url_orbs_pinned[msg] {
	some alias, orb in input.orbs
	is_string(orb)
	is_url_orb(orb)
	not url_has_commit_sha(orb)
	msg := sprintf("URL orb %q is not pinned to a 40-character commit SHA in its path", [alias])
}

is_url_orb(orb) {
	contains(orb, "://")
}

# A path segment is a full commit SHA.
url_has_commit_sha(url) {
	path := split(split(split(url, "#")[0], "?")[0], "/")
	some segment in path
	regex.match(`^[0-9a-fA-F]{40}$`, segment)
}

# =============================================================================
# 6. no_setup_in_continuation: a continuation config is not a setup config
# =============================================================================

# CircleCI cannot tell a continuation config from a setup config (both are
# `setup: true` there), so this rule only runs where the caller says the config
# is a continuation: the planner's local check sets data.meta.smarter.continuation.
# Pushed as org policy, it never fires, and setup configs are not blocked.
no_setup_in_continuation[msg] {
	data.meta.smarter.continuation == true
	input.setup == true
	msg := "a continuation config must not declare setup: true"
}

# =============================================================================
# Workflow job entries
# =============================================================================

# An entry of a workflow's `jobs` is a bare job name or a single-key object
# {job: config}.
entry_ref(entry) := utils.get_element_name(entry)

entry_cfg(entry) := cfg if {
	is_object(entry)
	count(entry) == 1
	some _, cfg in entry
	is_object(cfg)
} else := {}

# The name that other jobs `require`: `name:` if set, else the job name.
entry_name(entry) := object.get(entry_cfg(entry), "name", entry_ref(entry))

# An approval: the entry says `type: approval`, or the job it names is defined
# with that type under `jobs`.
approval_entry(entry) {
	entry_cfg(entry).type == "approval"
}

approval_entry(entry) {
	input.jobs[entry_ref(entry)].type == "approval"
}

job_label(name, ref) := sprintf("%q", [name]) if {
	name == ref
} else := sprintf("%q (job %q)", [name, ref])
