package org

import future.keywords

# Run with: circleci policy test policies
#
# This file is part of the bundle directory, so `circleci policy push` uploads
# it too. It is inert there: its only rules are tests, and it enables nothing.

policy_name["smarter_guardrails_test"]

# =============================================================================
# Fixtures
# =============================================================================

# A config with one workflow that has the given jobs.
fixture_workflow(jobs) := {"version": 2.1, "workflows": {"ci": {"jobs": jobs}}}

# A complete config that passes every rule.
fixture_good := {
	"version": 2.1,
	"orbs": {
		"node": "circleci/node@5.1.0",
		"shared": "https://example.com/example-org/example-orbs/0123456789abcdef0123456789abcdef01234567/src/orb.yml",
		"local": {"jobs": {"hello": {"docker": [{"image": "cimg/base:stable"}], "steps": ["checkout"]}}},
	},
	"jobs": {
		"build": {"resource_class": "medium", "steps": ["checkout"]},
		"security-scan": {"resource_class": "small", "steps": ["checkout"]},
		"deploy": {"steps": ["checkout"]},
	},
	"executors": {"small-docker": {"docker": [{"image": "cimg/base:stable"}], "resource_class": "small"}},
	"workflows": {"ci": {"jobs": [
		"build",
		"security-scan",
		{"hold-deploy": {"type": "approval", "requires": ["build", "security-scan"]}},
		{"deploy": {
			"context": "deploy",
			"requires": ["hold-deploy"],
			"filters": {"branches": {"only": "main"}},
		}},
	]}},
}

# A deploy job entry that passes every restricted_contexts check, for tests
# that change one thing about it.
fixture_deploy := {
	"context": ["deploy"],
	"requires": ["hold-deploy"],
	"filters": {"branches": {"only": ["main"]}},
}

fixture_hold := {"hold-deploy": {"type": "approval"}}

# The deploy job entry with other branch and tag filters. (object.union merges
# nested objects, so the filters are removed first.)
fixture_deploy_filtered(filters) := object.union(json.remove(fixture_deploy, ["filters"]), {"filters": filters})

has_message(messages, fragment) {
	some message in messages
	contains(message, fragment)
}

# =============================================================================
# The rules that are on
# =============================================================================

test_every_rule_is_hard_by_default {
	enable_hard == {
		"required_jobs",
		"restricted_contexts",
		"resource_classes",
		"registry_orbs_pinned",
		"url_orbs_pinned",
		"no_setup_in_continuation",
	}
	count(enable_rule) == 0
}

test_a_rule_moves_between_hard_and_soft {
	enable_hard == {"required_jobs"} with data.org.hard_rules as {"required_jobs"}
		with data.org.soft_rules as {"resource_classes"}
	enable_rule == {"resource_classes"} with data.org.hard_rules as {"required_jobs"}
		with data.org.soft_rules as {"resource_classes"}
}

test_the_good_config_passes_every_rule {
	count(required_jobs) == 0 with input as fixture_good
	count(restricted_contexts) == 0 with input as fixture_good
	count(resource_classes) == 0 with input as fixture_good
	count(registry_orbs_pinned) == 0 with input as fixture_good
	count(url_orbs_pinned) == 0 with input as fixture_good
	count(no_setup_in_continuation) == 0 with input as fixture_good
		with data.meta as {"smarter": {"continuation": true}}
}

# =============================================================================
# 1. required_jobs
# =============================================================================

test_required_jobs_missing_carries_the_repair_code {
	required_jobs == {"workflow \"ci\" is missing the required job \"security-scan\" [smarter:require-job=security-scan]"} with input as fixture_workflow(["build"])
}

test_required_jobs_present {
	count(required_jobs) == 0 with input as fixture_workflow(["build", "security-scan"])
}

test_required_jobs_present_with_options {
	count(required_jobs) == 0 with input as fixture_workflow(["build", {"security-scan": {"requires": ["build"]}}])
}

test_required_jobs_matches_the_workflow_name {
	count(required_jobs) == 0 with input as fixture_workflow([{"scan": {"name": "security-scan"}}])
	count(required_jobs) == 1 with input as fixture_workflow([{"security-scan": {"name": "scan"}}])
}

test_required_jobs_every_workflow {
	cfg := {"version": 2.1, "workflows": {
		"version": 2,
		"ci": {"jobs": ["build", "security-scan"]},
		"nightly": {"jobs": ["build"]},
	}}
	required_jobs == {"workflow \"nightly\" is missing the required job \"security-scan\" [smarter:require-job=security-scan]"} with input as cfg
}

test_required_jobs_several_required {
	required_jobs == {
		"workflow \"ci\" is missing the required job \"lint\" [smarter:require-job=lint]",
		"workflow \"ci\" is missing the required job \"security-scan\" [smarter:require-job=security-scan]",
	} with input as fixture_workflow(["build"])
		with data.org.required_job_names as {"security-scan", "lint"}
}

test_required_jobs_not_asked_of_a_setup_config {
	cfg := object.union(fixture_workflow(["plan"]), {"setup": true})
	count(required_jobs) == 0 with input as cfg
}

test_required_jobs_config_without_workflows {
	count(required_jobs) == 0 with input as {"version": 2.1, "jobs": {"build": {}}}
}

# =============================================================================
# 2. restricted_contexts
# =============================================================================

test_restricted_context_allowed_job_branch_and_approval {
	count(restricted_contexts) == 0 with input as fixture_workflow([fixture_hold, {"deploy": fixture_deploy}])
}

test_restricted_context_as_a_string {
	count(restricted_contexts) == 0 with input as fixture_workflow([fixture_hold, {"deploy": object.union(fixture_deploy, {"context": "deploy"})}])
}

test_other_contexts_are_unrestricted {
	count(restricted_contexts) == 0 with input as fixture_workflow([{"build": {"context": ["registry", "docs"]}}, "lint"])
}

test_restricted_context_wrong_job {
	msgs := restricted_contexts with input as fixture_workflow([fixture_hold, {"publish": fixture_deploy}])
	count(msgs) == 1
	has_message(msgs, "job \"publish\" uses the restricted context \"deploy\"; allowed jobs: deploy")
}

test_restricted_context_the_job_definition_counts_not_its_name {
	# A job that calls itself "deploy" is still the job `publish`.
	msgs := restricted_contexts with input as fixture_workflow([fixture_hold, {"publish": object.union(fixture_deploy, {"name": "deploy"})}])
	has_message(msgs, "job \"deploy\" (job \"publish\") uses the restricted context \"deploy\"; allowed jobs: deploy")

	# A renamed `deploy` job is the allowed job.
	count(restricted_contexts) == 0 with input as fixture_workflow([fixture_hold, {"deploy": object.union(fixture_deploy, {"name": "deploy-prod"})}])
}

test_restricted_context_without_a_branch_filter {
	msgs := restricted_contexts with input as fixture_workflow([fixture_hold, {"deploy": json.remove(fixture_deploy, ["filters"])}])
	count(msgs) == 1
	has_message(msgs, "must be limited to the branches [main] (filters.branches.only)")
}

test_restricted_context_on_another_branch {
	msgs := restricted_contexts with input as fixture_workflow([fixture_hold, {"deploy": fixture_deploy_filtered({"branches": {"only": ["main", "dev"]}})}])
	count(msgs) == 1
	has_message(msgs, "must be limited to the branches [main]")
}

test_restricted_context_a_branch_ignore_does_not_limit {
	msgs := restricted_contexts with input as fixture_workflow([fixture_hold, {"deploy": fixture_deploy_filtered({"branches": {"ignore": "dev"}})}])
	has_message(msgs, "must be limited to the branches [main]")
}

test_restricted_context_a_regex_branch_must_be_listed {
	cfg := fixture_workflow([fixture_hold, {"deploy": fixture_deploy_filtered({"branches": {"only": "/^release-.*/"}})}])
	count(restricted_contexts) == 1 with input as cfg
	count(restricted_contexts) == 0 with input as cfg
		with data.org.context_restrictions as {"deploy": {
			"jobs": {"deploy"},
			"branches": {"main", "/^release-.*/"},
			"allow_tags": true,
			"require_approval": true,
		}}
}

test_restricted_context_on_tags_only {
	tags_only := {"branches": {"ignore": "/.*/"}, "tags": {"only": "/^v.*/"}}
	count(restricted_contexts) == 0 with input as fixture_workflow([fixture_hold, {"deploy": fixture_deploy_filtered(tags_only)}])
}

test_restricted_context_on_branches_and_tags {
	both := {"branches": {"only": "main"}, "tags": {"only": "/^v.*/"}}
	count(restricted_contexts) == 0 with input as fixture_workflow([fixture_hold, {"deploy": fixture_deploy_filtered(both)}])
}

test_restricted_context_tags_not_allowed {
	no_tags := {"deploy": {
		"jobs": {"deploy"},
		"branches": {"main"},
		"allow_tags": false,
		"require_approval": true,
	}}
	both := {"branches": {"only": "main"}, "tags": {"only": "/^v.*/"}}
	msgs := restricted_contexts with input as fixture_workflow([fixture_hold, {"deploy": fixture_deploy_filtered(both)}])
		with data.org.context_restrictions as no_tags
	count(msgs) == 1
	has_message(msgs, "must not run on tags (filters.tags)")

	# Ignoring some tags still leaves the others.
	ignores := {"branches": {"only": "main"}, "tags": {"ignore": "/^old-.*/"}}
	count(restricted_contexts) == 1 with input as fixture_workflow([fixture_hold, {"deploy": fixture_deploy_filtered(ignores)}])
		with data.org.context_restrictions as no_tags

	# Ignoring every tag leaves none, and no tags filter at all means no tags.
	none := {"branches": {"only": "main"}, "tags": {"ignore": "/.*/"}}
	count(restricted_contexts) == 0 with input as fixture_workflow([fixture_hold, {"deploy": fixture_deploy_filtered(none)}])
		with data.org.context_restrictions as no_tags
	count(restricted_contexts) == 0 with input as fixture_workflow([fixture_hold, {"deploy": fixture_deploy}])
		with data.org.context_restrictions as no_tags
}

test_restricted_context_without_an_approval {
	msgs := restricted_contexts with input as fixture_workflow([{"deploy": json.remove(fixture_deploy, ["requires"])}])
	count(msgs) == 1
	has_message(msgs, "must run downstream of an approval job")
}

test_restricted_context_requiring_a_job_that_is_not_an_approval {
	msgs := restricted_contexts with input as fixture_workflow(["build", {"deploy": object.union(fixture_deploy, {"requires": ["build"]})}])
	has_message(msgs, "must run downstream of an approval job")
}

test_restricted_context_an_approval_that_is_not_an_ancestor {
	# The approval exists, but the deploy job does not wait for it.
	msgs := restricted_contexts with input as fixture_workflow([fixture_hold, "build", {"deploy": object.union(fixture_deploy, {"requires": ["build"]})}])
	has_message(msgs, "must run downstream of an approval job")
}

test_restricted_context_an_approval_further_up {
	jobs := [
		fixture_hold,
		{"build": {"requires": ["hold-deploy"]}},
		{"deploy": object.union(fixture_deploy, {"requires": ["build"]})},
	]
	count(restricted_contexts) == 0 with input as fixture_workflow(jobs)
}

test_restricted_context_an_approval_by_name {
	jobs := [
		{"approve": {"type": "approval", "name": "hold-deploy"}},
		{"deploy": fixture_deploy},
	]
	count(restricted_contexts) == 0 with input as fixture_workflow(jobs)
}

test_restricted_context_an_approval_defined_under_jobs {
	# The approval type may be set in the job's definition, and not on the entry.
	cfg := object.union(
		fixture_workflow([{"hold-deploy": {}}, {"deploy": fixture_deploy}]),
		{"jobs": {"hold-deploy": {"type": "approval"}}},
	)
	count(restricted_contexts) == 0 with input as cfg
}

test_restricted_context_a_job_defined_under_jobs_as_a_no_op_is_not_an_approval {
	cfg := object.union(
		fixture_workflow([{"hold-deploy": {}}, {"deploy": fixture_deploy}]),
		{"jobs": {"hold-deploy": {"type": "no-op"}}},
	)
	has_message(restricted_contexts, "must run downstream of an approval job") with input as cfg
}

test_restricted_context_requires_with_a_status {
	jobs := [fixture_hold, {"deploy": object.union(fixture_deploy, {"requires": [{"hold-deploy": ["success"]}]})}]
	count(restricted_contexts) == 0 with input as fixture_workflow(jobs)
}

test_restricted_context_approval_is_optional_per_context {
	no_approval := {"deploy": {
		"jobs": {"deploy"},
		"branches": {"main"},
		"allow_tags": true,
		"require_approval": false,
	}}
	count(restricted_contexts) == 0 with input as fixture_workflow([{"deploy": json.remove(fixture_deploy, ["requires"])}])
		with data.org.context_restrictions as no_approval
}

test_restricted_context_a_cycle_does_not_hang {
	jobs := [{"a": {"requires": ["b"]}}, {"b": {"requires": ["a"]}}, {"deploy": object.union(fixture_deploy, {"requires": ["a"]})}]
	has_message(restricted_contexts, "must run downstream of an approval job") with input as fixture_workflow(jobs)
}

test_restricted_context_parameterized_name_is_flagged {
	jobs := [{"build": {"context": "<< pipeline.parameters.ctx >>"}}]
	msgs := restricted_contexts with input as fixture_workflow(jobs)
	count(msgs) == 1
	has_message(msgs, "uses the parameterized context \"<< pipeline.parameters.ctx >>\"")
}

test_restricted_context_every_failure_is_reported {
	jobs := [{"publish": {"context": ["deploy"]}}]
	count(restricted_contexts) == 3 with input as fixture_workflow(jobs)
}

test_restricted_context_approval_jobs_run_no_code {
	jobs := [{"hold-deploy": {"type": "approval", "context": ["deploy"]}}]
	count(restricted_contexts) == 0 with input as fixture_workflow(jobs)
}

test_restricted_context_an_approval_defined_under_jobs_runs_no_code {
	cfg := object.union(
		fixture_workflow([{"hold-deploy": {"context": ["deploy"]}}]),
		{"jobs": {"hold-deploy": {"type": "approval"}}},
	)
	count(restricted_contexts) == 0 with input as cfg
}

test_restricted_context_malformed_entries_are_ignored {
	jobs := [{"a": null}, {"b": {}, "c": {}}, 7, {"d": {"context": null}}]
	count(restricted_contexts) == 0 with input as fixture_workflow(jobs)
}

# =============================================================================
# 3. resource_classes
# =============================================================================

test_resource_class_allowed {
	count(resource_classes) == 0 with input as {"jobs": {
		"a": {"resource_class": "small"},
		"b": {"resource_class": "medium+"},
		"c": {"resource_class": "large"},
		"d": {},
	}}
}

test_resource_class_not_allowed_in_a_job {
	resource_classes == {"job \"build\" uses the resource_class \"2xlarge\", which is not allowed (allowed: large, medium, medium+, small)"} with input as {"jobs": {"build": {"resource_class": "2xlarge"}}}
}

test_resource_class_not_allowed_in_an_executor {
	msgs := resource_classes with input as {"executors": {"big": {"resource_class": "xlarge"}}}
	has_message(msgs, "executor \"big\" uses the resource_class \"xlarge\"")
}

test_resource_class_not_allowed_after_compiling {
	cfg := {
		"jobs": {"build": {"resource_class": "<< parameters.size >>"}},
		"_compiled_": {"jobs": {
			"build": {"resource_class": "2xlarge+"},
			"node/test": {"resource_class": "xlarge"},
			"lint": {"resource_class": "small"},
		}},
	}
	msgs := resource_classes with input as cfg
	count(msgs) == 2
	has_message(msgs, "job \"build\" uses the resource_class \"2xlarge+\"")
	has_message(msgs, "job \"node/test\" uses the resource_class \"xlarge\"")
}

test_resource_class_in_both_source_and_compiled_is_reported_once {
	cfg := {
		"jobs": {"build": {"resource_class": "xlarge"}},
		"_compiled_": {"jobs": {"build": {"resource_class": "xlarge"}}},
	}
	count(resource_classes) == 1 with input as cfg
}

test_resource_class_parameterized_is_skipped_in_the_source {
	count(resource_classes) == 0 with input as {"jobs": {"build": {"resource_class": "<< parameters.size >>"}}}
}

test_resource_class_a_no_op_job_has_no_executor_so_it_is_not_checked {
	count(resource_classes) == 0 with input as {"jobs": {"gate": {"type": "no-op"}}}
	count(resource_classes) == 0 with input as {"jobs": {"gate": {"type": "no-op", "resource_class": "2xlarge"}}}
}

test_resource_class_a_no_op_job_is_not_checked_after_compiling_either {
	cfg := {"_compiled_": {"jobs": {
		"gate": {"type": "no-op", "resource_class": "xlarge"},
		"build": {"resource_class": "xlarge"},
	}}}
	msgs := resource_classes with input as cfg
	count(msgs) == 1
	has_message(msgs, "job \"build\" uses the resource_class \"xlarge\"")
}

test_resource_class_a_job_of_another_type_is_still_checked {
	count(resource_classes) == 1 with input as {"jobs": {"build": {"type": "build", "resource_class": "xlarge"}}}
}

test_resource_class_allowlist_is_a_setting {
	cfg := {"jobs": {"build": {"resource_class": "xlarge"}}}
	count(resource_classes) == 0 with input as cfg
		with data.org.allowed_resource_classes as {"xlarge"}
	count(resource_classes) == 1 with input as cfg
}

# =============================================================================
# 4. registry_orbs_pinned
# =============================================================================

test_registry_orb_pinned {
	count(registry_orbs_pinned) == 0 with input as {"orbs": {"a": "circleci/node@5.1.0", "b": "some-ns/some_orb@10.20.30"}}
}

test_registry_orb_not_pinned {
	cfg := {"orbs": {
		"major": "circleci/node@5",
		"minor": "circleci/node@5.1",
		"volatile": "circleci/node@volatile",
		"dev": "circleci/node@dev:alpha",
		"unversioned": "circleci/node",
		"parameterized": "circleci/node@<< pipeline.parameters.node >>",
		"prerelease": "circleci/node@5.1.0-beta.1",
	}}
	msgs := registry_orbs_pinned with input as cfg
	count(msgs) == 7
	has_message(msgs, "orb \"major\" (circleci/node@5) is not pinned to an exact x.y.z version")
	has_message(msgs, "orb \"parameterized\"")
}

test_two_aliases_of_one_orb_at_different_versions {
	msgs := registry_orbs_pinned with input as {"orbs": {"a": "circleci/node@5.1.0", "b": "circleci/node@4"}}
	count(msgs) == 1
}

test_registry_orb_rule_ignores_url_and_inline_orbs {
	cfg := {"orbs": {
		"url": "https://example.com/orb.yml",
		"inline": {"jobs": {}},
	}}
	count(registry_orbs_pinned) == 0 with input as cfg
}

# =============================================================================
# 5. url_orbs_pinned
# =============================================================================

test_url_orb_pinned_to_a_commit {
	cfg := {"orbs": {
		"a": "https://example.com/example-org/example-orbs/0123456789abcdef0123456789abcdef01234567/src/orb.yml",
		"b": "https://example.com/example-org/example-orbs/-/raw/0123456789ABCDEF0123456789abcdef01234567/orb.yml",
	}}
	count(url_orbs_pinned) == 0 with input as cfg
}

test_url_orb_not_pinned {
	cfg := {"orbs": {
		"branch": "https://example.com/example-org/example-orbs/main/src/orb.yml",
		"short": "https://example.com/example-org/example-orbs/0123456/src/orb.yml",
		"too-long": "https://example.com/example-org/example-orbs/0123456789abcdef0123456789abcdef012345678/src/orb.yml",
		"not-hex": "https://example.com/example-org/example-orbs/0123456789abcdef0123456789abcdef0123456g/src/orb.yml",
		"query": "https://example.com/orb.yml?ref=0123456789abcdef0123456789abcdef01234567",
		"inside-a-name": "https://example.com/example-org/orb-0123456789abcdef0123456789abcdef01234567.yml",
	}}
	msgs := url_orbs_pinned with input as cfg
	count(msgs) == 6
	has_message(msgs, "URL orb \"branch\" is not pinned to a 40-character commit SHA in its path")
}

test_url_orb_message_does_not_repeat_the_url {
	cfg := {"orbs": {"private": "https://user:example-password@example.com/example-org/orbs/main/orb.yml?token=example-token"}}
	msgs := url_orbs_pinned with input as cfg
	count(msgs) == 1
	not has_message(msgs, "example-password")
	not has_message(msgs, "example-token")
}

test_url_orb_rule_ignores_registry_and_inline_orbs {
	cfg := {"orbs": {"a": "circleci/node@5", "b": {"jobs": {}}}}
	count(url_orbs_pinned) == 0 with input as cfg
}

# =============================================================================
# 6. no_setup_in_continuation
# =============================================================================

test_setup_in_a_continuation {
	no_setup_in_continuation == {"a continuation config must not declare setup: true"} with input as {"setup": true}
		with data.meta as {"smarter": {"continuation": true}}
}

test_continuation_without_setup {
	count(no_setup_in_continuation) == 0 with input as {"version": 2.1}
		with data.meta as {"smarter": {"continuation": true}}
	count(no_setup_in_continuation) == 0 with input as {"setup": false}
		with data.meta as {"smarter": {"continuation": true}}
}

test_setup_config_is_not_blocked_by_the_service {
	# CircleCI evaluates setup configs too, and never sets data.meta.smarter.
	count(no_setup_in_continuation) == 0 with input as {"setup": true}
		with data.meta as {"project_id": "p", "vcs": {"branch": "main"}}
	count(no_setup_in_continuation) == 0 with input as {"setup": true}
		with data.meta as {"smarter": {"continuation": false}}
}
