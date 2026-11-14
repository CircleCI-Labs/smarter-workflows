# Data flow

How jobs hand data to each other, and how that fixes the order. Tags such as [ws#overview] are sources; the URLs are at the end of the file.
`(compiler-checked)` means confirmed with `circleci config validate` (CLI 1.6.0). `(heuristic)` means advice, not a documented rule.

## 1. Workspaces, caches, artifacts

| | Workspace | Cache | Artifacts |
|---|---|---|---|
| Moves data | between the jobs of one workflow, downstream only | between runs, across branches of the project | out of the pipeline, to people and tools |
| Write | `persist_to_workspace` (`root`, `paths`) | `save_cache` (`key`, `paths`) | `store_artifacts` (`path`, `destination`) |
| Read | `attach_workspace` (`at`) | `restore_cache` (`keys`) | the Artifacts tab and the API |
| Ordering | only upstream jobs' data is visible | none: the first write of a key wins | none |
| Default retention | 15 days | 15 days | 30 days |

Pick by the question: does a job of this workflow need the data? Workspace. Does a later run need the data to go faster, and is it safe to be stale? Cache. Does a person or tool need the output after the run? Artifact. [concepts#persistence] [persist#types]

## 2. Workspace rules

- A job sees only what the jobs upstream of it (by `requires`, directly or not) persisted. A consumer that is not downstream of its producer gets nothing. [ws#overview]
- A workspace is additive: jobs add files and cannot delete them. Each addition is a layer. [ws#overview]
- Layers are applied in graph order. For two concurrent jobs the order is undefined, and the same filename from both makes `attach_workspace` fail. Give concurrent producers disjoint paths, for example the matrix value in the file name (`coverage/unit-<< parameters.runtime >>.txt`). [ws#overview]
- `attach_workspace` downloads and unpacks every upstream layer. Persist only the paths a downstream job needs, and keep a job away from heavy producers it does not need: a no-op coordinator or gate between them hides nothing. [cfg#attach] [ws#overview]
- `root` is an absolute path or relative to `working_directory`. `paths` are relative to `root`, may be globs (Go `filepath.Match`), and must not be the root itself. [cfg#persist]
- A rerun of a workflow inherits its workspace. Workspaces live 15 days by default, so an approval that waits longer than that may find nothing to attach. (heuristic, from the two retention facts) [ws#overview] [wf#states]

## 3. Data contracts fix the order

| Contract | Edge |
|---|---|
| B attaches what A persists | A is an ancestor of B, directly or through other jobs |
| D attaches what A and C persist | A and C are both ancestors of D, with disjoint paths |
| The producer is a matrix | the consumer requires the matrix by its alias, so it sees every cell |
| The consumer is behind an approval | the producer is upstream of the approval or of the consumer |
| Two jobs both write the same path | an edge between them, so their layer order is defined, or different paths |

```yaml
jobs:
  build:
    docker:
      - image: cimg/base:2026.11
    steps:
      - run: mkdir -p /tmp/workspace/dist && echo out > /tmp/workspace/dist/app.txt
      - persist_to_workspace:
          root: /tmp/workspace
          paths:
            - dist
  e2e:
    docker:
      - image: cimg/base:2026.11
    steps:
      - attach_workspace:
          at: /tmp/workspace
      - run: ls /tmp/workspace/dist

workflows:
  ci:
    jobs:
      - build
      - e2e:
          requires: [build]
```

How to apply it:
1. List the producers (`persist_to_workspace`) and consumers (`attach_workspace`) of the catalog. Orb jobs may do either: read the orb source.
2. Add one edge from each producer to each of its consumers. Never run a consumer without one.
3. Put no other work between them unless the other work has a reason.
4. Check the paths of producers that can run at the same time.

The v0.1 planner follows the same contract: a selected producer pulls in every consumer downstream of it, and an orb job counts as both (`docs/spec/v0.1.md`, step 4). A consumer that is not downstream of its producer breaks that rule.

## 4. Caches

- `restore_cache` goes before the install step, `save_cache` after it. A list of keys is one cache with fallbacks; keys match by prefix, and a fallback key gives a partial restore. [cache#restoring]
- Caches are immutable on write. A job that writes an existing key is discarded without an error. [cache#how]
- A miss must never break a job: caching is an optimization, never a requirement for correctness. [cache#how]
- Never pass this commit's build output through a cache. Caches are project-wide and shared across branches; use a workspace. [persist#types]
- Parallel jobs that write the same key race: a job that reads it later gets whichever write came first. Write separate keys, or order the jobs with `requires` so one job writes and the others read. [cache#race] [cache#order]

```yaml
jobs:
  deps:
    docker:
      - image: cimg/base:2026.11
    steps:
      - checkout
      - restore_cache:
          keys:
            - deps-v1-{{ checksum "package-lock.json" }}
            - deps-v1-
      - run: echo "install dependencies here"
      - save_cache:
          key: deps-v1-{{ checksum "package-lock.json" }}
          paths:
            - ~/.cache

workflows:
  ci:
    jobs:
      - deps
```

## 5. Artifacts

- `store_artifacts` keeps files for people and tools: reports, logs, binaries. They live 30 days by default. [art#store]
- `store_test_results` uploads test results; both steps run after a failed step. [cfg#when-attr]
- To give a file to a later job, use a workspace, not an artifact. [concepts#persistence]

## Checklist

- [ ] Every consumer of a workspace has every producer it needs upstream.
- [ ] Producers that can run at the same time persist disjoint paths.
- [ ] Each `persist_to_workspace` lists only the paths a downstream job uses.
- [ ] A matrix producer is required by its alias.
- [ ] No job needs a cache hit to be correct, and no job passes this commit's output through a cache.

[cfg#persist]: https://circleci.com/docs/reference/configuration-reference/#persisttoworkspace
[cfg#attach]: https://circleci.com/docs/reference/configuration-reference/#attachworkspace
[cfg#when-attr]: https://circleci.com/docs/reference/configuration-reference/#the-when-attribute
[ws#overview]: https://circleci.com/docs/guides/orchestrate/workspaces/#overview
[wf#states]: https://circleci.com/docs/guides/orchestrate/workflows/#states
[persist#types]: https://circleci.com/docs/guides/optimize/persist-data/
[concepts#persistence]: https://circleci.com/docs/reference/concepts/#data-persistence
[cache#how]: https://circleci.com/docs/guides/optimize/caching/#how-caching-works
[cache#restoring]: https://circleci.com/docs/guides/optimize/caching/#restoring-cache
[cache#race]: https://circleci.com/docs/guides/optimize/caching/#caching-race-conditions
[cache#order]: https://circleci.com/docs/guides/optimize/caching/#order-jobs-to-avoid-duplicate-saves
[art#store]: https://circleci.com/docs/guides/optimize/artifacts/
