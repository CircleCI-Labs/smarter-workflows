# 3. Build the impact map from file-access traces of full runs on the default branch

Date: 2026-11-13. Status: accepted.

## Context

Smarter Testing builds an impact map on the default branch, then uses it on other branches to pick the tests a change affects. This project wants the same loop for jobs:
1. a full run on the default branch rebuilds the map;
2. branches use it;
3. a merge triggers the next full run, which rebuilds it again.

A job's equivalent of test coverage is the set of repo files it reads. There are several ways to record that:
- **Hand-written globs** go stale.
- **Language analyzers** need one per ecosystem, and miss scripts and config files.
- **`strace` and `LD_PRELOAD`** are slow, and Go binaries bypass libc.
- **fanotify and eBPF** need privileges that Docker jobs don't have.
- **Access times (atime)** cost nothing. Under the usual `relatime` mount option, the first read after a write always updates a file's atime. A freshly checked-out file has atime equal to mtime, so a file that was read has atime greater than mtime.

## Decision

1. On full runs of the default branch, the planner adds a post-step to every job of the pruned workflow. The post-step records:
   - the git-tracked files whose atime is newer than their mtime;
   - the directories that were listed.
   It first probes that atime works on that filesystem. If it doesn't, the trace is marked incomplete.
2. A final job merges the traces into the impact map.
3. It saves the map to the project cache under a key derived from the commit SHA.
4. On other branches, the planner restores the map for the merge-base commit, falling back to the newest map.
5. Only green full runs produce a map.

## Consequences

- No ML, no per-language code, no hand-written globs. The map explains itself: "this job read these files".
- `noatime` filesystems, busybox `find` and non-Linux executors give incomplete traces, so those jobs always run.
- Traces miss inputs that were read and then rewritten in the same job (formatters that run with `-w`), and inputs from outside the repo. See ADR 2 for how those are covered.
- Cache entries can expire (default retention) and can be written by any branch. A missing map means a full run. Mandatory jobs and guardrails don't depend on the map. Signing the map on the default branch is planned hardening.
