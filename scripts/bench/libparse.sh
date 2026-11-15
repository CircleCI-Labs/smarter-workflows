# Shared jq filters for fetch-history.sh and test-parse.sh (POSIX sh; source, do not execute).

# json_echo pipes JSON into jq without echo(1) expanding backslash sequences.
json_pipe() {
  jq "$@"
}

commit_from_pipeline() {
  json_pipe -r '
    .vcs.revision
    // .trigger_parameters.git.checkout_sha
    // .trigger_parameters["git.checkout_sha"]
    // .trigger_parameters.github_app.checkout_sha
    // empty
  '
}

branch_from_pipeline() {
  json_pipe -r '
    .vcs.branch
    // .trigger_parameters.git.branch
    // .trigger_parameters["git.branch"]
    // .trigger_parameters.github_app.branch
    // ""
  '
}

next_page_token_from_pipelines() {
  json_pipe -r '.next_page_token // empty'
}

# jobs_from_workflow maps workflow job API items to history jobs.
jobs_from_workflow() {
  json_pipe '
    def iso_epoch:
      sub("\\.[0-9]+"; "") | fromdateiso8601;
    def duration_s:
      if (.duration? != null) then ((.duration // 0) / 1000)
      elif (.started_at? and .stopped_at?) then
        ((.stopped_at | iso_epoch) - (.started_at | iso_epoch))
      else 0 end;
    [.items[] | select(.type == "build" and (.status == "success" or .status == "failed")) | {
      name,
      status: (if .status == "success" then "success" elif .status == "failed" then "failed" else .status end),
      duration_s: duration_s,
      rerun_success: (.rerun_success // false)
    }]
  '
}

mark_flaky_jobs() {
  json_pipe '
    map(
      if .status == "failed" and (.rerun_success // false) then . + {flaky: true} | del(.rerun_success)
      else . | del(.rerun_success)
      end
    )
  '
}

job_detail_rerun_success() {
  json_pipe '([.retries[]? | select(.status=="success")] | length > 0)'
}