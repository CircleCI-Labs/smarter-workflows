# Shared jq filters for fetch-history.sh and test-parse.sh (POSIX sh; source, do not execute).
# shellcheck disable=SC2016

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

# workflows_named lists workflows with a given name, oldest first ($1 = name).
workflows_named() {
  _wf_name="$1"
  json_pipe --arg name "$_wf_name" '
    [.items[]? | select(.name == $name) | {id, created_at}]
    | sort_by(.created_at)
  '
}

# merge_workflow_reruns reads [{created_at, items}, ...] (oldest first) and emits
# history jobs: final row per name from the newest workflow; flaky when the job
# failed in an earlier rerun and succeeded in a later one.
merge_workflow_reruns() {
  json_pipe '
    def iso_epoch:
      sub("\\.[0-9]+"; "") | fromdateiso8601;
    def duration_s:
      if (.duration? != null) then ((.duration // 0) / 1000)
      elif (.started_at? and .stopped_at?) then
        ((.stopped_at | iso_epoch) - (.started_at | iso_epoch))
      else 0 end;
    def map_job:
      {
        name,
        status: (if .status == "success" then "success" elif .status == "failed" then "failed" else .status end),
        duration_s: duration_s
      };
    def flaky_pair($events):
      ([range(0; ($events | length))] as $idx
      | any($idx[] as $i |
          ($events[$i].status == "failed")
          and (any(range($i + 1; ($events | length)) as $j | $events[$j].status == "success"))
        ));
    (
      [.[] | . as $wf | ($wf.items // [])[]
        | select(.type == "build" and (.status == "success" or .status == "failed"))
        | . + {wf_at: $wf.created_at}]
      | group_by(.name)
      | map(
          sort_by(.wf_at)
          | . as $ev
          | ($ev | last | map_job) as $last
          | if flaky_pair($ev) then $last + {flaky: true} else $last end
        )
    )
  '
}

# jobs_from_workflow maps one workflow job page (no reruns).
jobs_from_workflow() {
  json_pipe --arg at "1970-01-01T00:00:00Z" '[{created_at: $at, items: .items}]' | merge_workflow_reruns
}