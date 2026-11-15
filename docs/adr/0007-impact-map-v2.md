# 7. Impact map v2: shared path tables and compact indices

Date: 2026-11-15. Status: accepted.

## Context

Impact map v1 stores every repo path verbatim in each job's `files` and `dirs` arrays. On large monorepos the JSON grows with the number of job×path pairs (roughly jobs × average reads), blows past the 16 MiB parse cap, and takes seconds to decode. A realistic shape is thousands of jobs each reading a large fraction of tens or hundreds of thousands of unique paths.

## Decision

1. **On-disk format v2** (still JSON, gzip-compressed as `impact-map.json.gz`):
   - Sorted `files` and `dirs` tables: each repo path appears once.
   - Per job, `file_idx` and `dir_idx` are standard base64 blobs of **sorted delta varints** over table indices (first index absolute, later indices encode the gap from the previous one).
   - Metadata unchanged: `revision`, `created_at`, `method`, and per-job `complete` and `nodes`.
2. **Why delta varints instead of bitsets:** Indices are dense when jobs read many files but sparse when they read few; delta varints are small in the common case, need no third-party format, and decode in one pass with strict bounds. Roaring or raw bitsets win when sets are extremely sparse across a huge table; this workload is usually "many indices per job out of a shared table", where varints are simpler and fast enough.
3. **Writers** (`map build`, the trace map job) emit v2 gzip only. **Readers** accept v1 JSON (legacy cache) and v2 gzip; `plan` tries `impact-map.json.gz` then `impact-map.json`.
4. **Caps** (oversize or malformed → full run): compressed file ≤ 64 MiB; decompressed JSON ≤ 256 MiB; when compressed size &lt; 64 KiB, decompressed must be ≤ 32× compressed (zip-bomb guard); ≤ 4096 jobs; ≤ 200_000 file table entries; ≤ 50_000 dir table entries; ≤ 100_000 indices per job per table; job names ≤ 256 chars. v1 maps keep the former 16 MiB uncompressed cap.

## Consequences

- Monorepo-sized maps fit in cache and decode in sub-second time on developer hardware (see `docs/benchmarks-perf.md`).
- Path tables must stay sorted and deduplicated; corrupt index blobs fail closed at parse time.
- Cache artifacts change extension; operators with only v1 files still work until the next default-branch map build.

## Verification (2026-11-15)

`go test ./internal/impactmap -run TestBenchMapV2FitsTarget` on this branch: synthetic 2000 jobs × 10k files each, v2 gzip under 20 MiB and decode under 0.5 s.