# Resource budget: measured

Measured by J14.3 on the one-year synthetic dataset ([fixtures/README.md](../fixtures/README.md); `fixturegen -seed 42`, 6,211,856 `measurements` rows, same dataset as the [J04.4 and J09.9 baseline](benchmarks/baseline.md)). The targets are in [project#resource-budget](architecture/project.md#resource-budget). One machine, one to three runs per figure: use them for orders of magnitude, not as a promise.

## Verdict

Routine and bulk work are inside the budget for `vitamux`. The two operations J14.3 found far over it (D1 full-year `rebuild_aggregates`, D2 cold 90-day dashboard) were fixed afterwards and re-measured with the same harness on 2026-10-04 (rows marked "after the fix"); `postgres` still peaks slightly above its own target with ten parallel cold requests (D4). See [Deviations](#deviations).

| Target (core profile) | Measured | Verdict |
| --- | --- | --- |
| `vitamux` idle RSS ≤ 80 MiB | 25 MiB fresh (24 MiB in a Linux container); 89 MiB after one sign-in | Over after sign-in; target raised to ≤ 100 MiB (D3) |
| `postgres` idle ≤ 270 MiB | 178 MiB private memory (anon + shared buffers) | Within |
| Idle total ≤ 350 MiB | 203 MiB fresh; 267 MiB after sign-in | Within |
| Idle CPU < 2 % of a core | `vitamux` 0.0 % (under 1 % in any one second), `postgres` 0.2-0.5 % | Within |
| Peak total ≤ 900 MiB, routine work | export of the year 326 MiB; one-week `rebuild_aggregates` 254 MiB; warm dashboard 228 MiB | Within |
| Peak total ≤ 900 MiB, bulk work | after the fix: full-year `rebuild_aggregates` 27 MiB + `postgres` 193 MiB; cold dashboard, ten in parallel, 382 MiB + 285 MiB (before: 1.4-1.8 GiB, OOM-killed at 512 MiB; 1.7-2.1 GiB) | Within (D1, D2 fixed) |
| `postgres` peak ≤ 270 MiB | 172-228 MiB in most phases; 193 MiB in the full-year rebuild after the fix; 285 MiB with ten parallel cold requests (328 MiB before) | Slightly over with ten parallel cold requests (D4) |
| Compose limits: `vitamux` 512 MiB, `postgres` 1 GiB | `postgres` working set reached 995-1,020 MiB of 1 GiB (page cache, reclaimable; no OOM kill); `vitamux` peaks at 382 MiB after the fix | Within |
| 1 vCPU enough | Not measured: the test VM has 2 vCPUs. Bulk work used 1.3-1.9 cores for 6-8 s | Unverified |
| Data 1-3 GB/yr | Database 2.04 GiB after the load, 2.07 GiB with aggregates and cache; see Disk | Within |
| Images ≈ 0.5 GB | about 465 MB (computed, see Disk) | Within |
| Core + one sidecar | Not measured: no sidecar ships in the MVP | n/a |

## Measurements

"Private" is the PostgreSQL container's anonymous plus shared memory (what the postgres processes hold, shared buffers included). "Working set" is the container's `memory.current` minus inactive file cache, as `docker stats` shows it; it grows toward the limit as the 2 GiB database is read and is reclaimed under pressure. CPU is a share of one core: average over the phase, then the busiest second. Each phase below ran in a fresh process so one phase's heap cannot hide another's peak.

| Phase | Wall | `vitamux` peak RSS | `vitamux` CPU avg / peak | `postgres` private peak | `postgres` CPU avg |
| --- | --- | --- | --- | --- | --- |
| Idle, fresh start, 60-120 s | 60 s | 25 MiB | 0.0 % / 1.0 % | 178 MiB | 0.2 % |
| Idle, after one sign-in, 60-120 s | 60 s | 89 MiB | 0.0 % / 1.0 % | 178 MiB | 0.2 % |
| (a) Load by COPY (`fixtureload`, test process) | 4 m 22 s-4 m 39 s | n/a (not `vitamux`) | n/a | 172-173 MiB | 96-97 % |
| (a) Load by `vitamux import ndjson` of the export | 10 m 52 s | 30 MiB | 9 % / 101 % | 186 MiB | 90 % |
| (b) `rebuild_aggregates`, one week marked (57 marks) | 0.3 s | 64-82 MiB | n/a | 190 MiB | n/a |
| (b) `rebuild_aggregates`, whole year marked (3,229 marks), after the fix | 9.4 s | 27 MiB | 1.5 % / 11 % | 193 MiB | 92 % |
| (b) the same before the fix | 7.8-8.4 s | 1,433-1,757 MiB | 132 % / 179 % | 203-316 MiB | 42 % |
| (c) Dashboard, cold, ten requests in a row, after the fix | 5.6-6.4 s per round | 204 MiB | 0.6 cores / 1.0 (from rusage) | 236 MiB | 66 % |
| (c) Dashboard, cold, ten in parallel, after the fix | 4.0-4.4 s per round | 382 MiB | 0.8 cores / 1.5 (from rusage) | 285 MiB | 150 % |
| (c) Dashboard, cold, before the fix: in a row / in parallel | 6.9-12.3 / 6.1-13.7 s per round | 2,024-2,148 / 1,740-1,905 MiB | 0.5-0.9 cores / 1.9 | 198-228 / 322-328 MiB | 69-111 % |
| (c) Dashboard, warm, ten in parallel | 42-87 ms per round | 43-44 MiB | negligible | 182-185 MiB | negligible |
| (d) Export of the year with raw content, and download | 43-47 s | 143-144 MiB | 39-51 % / 46-59 % | 182 MiB | 69-85 % |

- Load by COPY is the J04.4 method (23,000 rows/s here, 22,500 there). `vitamux import` is the only path that loads a whole year inside the `vitamux` process, so it is the figure for its memory; it is bounded (30 MiB) but takes 2.4 times as long as COPY.
- Rebuild peaks are whole-process maximums from `getrusage`; so are the export and idle ones. The dashboard rows are the helper process's maximums (its own idle is 17 MiB).
- "After the fix" rows come from one run of the same harness (`VITAMUX_BUDGET_SKIP_IMPORT=1`) on the same machine and VM; the other rows were not re-measured, nor was the 512 MiB container check. The dashboard helper runs with `GOMEMLIMIT=400MiB`, so its 382 MiB is partly garbage the collector is allowed to keep. The rebuild's CPU is now in `postgres` (one core for 9 s), not in `vitamux`.
- Run-to-run spread in the cold dashboard wall times (6.1 s to 13.7 s in parallel) came from the Docker VM's page cache, not from the code: the VM has 3.8 GiB for a 2 GiB database plus the 1 GiB limit.

## Intraday series

Measured by J26.4 ([E26](plan/E26-intraday-views/README.md)): one local day of heart rate on the synthetic dataset with `fixturegen -hr-step 6`, so the WHOOP-like source has 14,400 rows, plus the dataset's other heart-rate sources. Budget: 1-minute resolved buckets in under 300 ms; raw at most 2,000 points per request. Median of seven calls through the router with an admin key (JSON included), one run, same machine as above, PostgreSQL 18 in the dev container.

| Request (`heart_rate`, one day) | Before | After | Budget |
| --- | --- | --- | --- |
| `GET /resolved/series` `window=1m` (1,440 buckets) | 999 ms | 80 ms | < 300 ms: within |
| `GET /resolved/series` `window=30s` (2,880 buckets) | 1,989 ms | 144 ms | none |
| `GET /sources/series` `grain=30s` | 95 ms | 46 ms | none |
| `GET /sources/series` `grain=1m` | 90 ms | 38 ms | none |
| `GET /sources/series` `grain=raw&limit=2000` | 17 ms | 10 ms | 2,000 points: within (the page is capped over all sources) |

The resolved series was over budget: `Rule.windowRows` scanned every row of the day for each bucket (rows times windows, 14,400 rows by 1,440 buckets). `ResolveWindowsOverridden` now hands each bucket or hour window only the rows that can overlap it (binary search over rows in start order, `narrower` in `internal/resolve/override.go`), so the work is linear in the rows; results are identical (`TestPropertyNarrowedWindowsEqualWhole`) and rules with `require_wear` keep the old path. No index or migration was needed: the source series queries read the day by the existing `measurements` indexes. After the fix the time is mostly the row load and JSON. Reproduce: `go test -tags integration -run TestIntradaySeriesBudget -v ./internal/api/` (needs `VITAMUX_DATABASE_URL`; it fails above 300 ms).

## Disk

| Item | Measured |
| --- | --- |
| Database after load (6.2 M rows, indexes included) | 2,044 MiB (2.14 GB): `measurements` 2,029 MiB (heap 1,145, indexes 884), 342 B/row as in J04.4; every other table under 3 MiB |
| After `rebuild_aggregates` and a cached 90-day dashboard | 2,075 MiB (`source_hourly_aggregates` 27 MiB, `resolved_cache` 4 MiB) |
| Export of the year, zip (includes raw content, the raw payload rows are 1 MiB) | 216 MiB (226,667,408 B), about a tenth of the database |
| Blob directory after one export | 214 MiB: the encrypted export itself, removed after 7 days or by the next export. Generated data has no raw blobs, so the 0.3-0.6 GB/yr estimate for them is still unverified |
| Images | `postgres:18-alpine` 425 MB on disk, `vitamux` binary 21.4 MB (linux/arm64, stripped), distroless base 6 MB, `pg_dump` client about 12 MB (per the Dockerfile): about 465 MB. Computed from the parts; the release image was not built (this Docker has no BuildKit) |

## Method

- Dataset: `go run ./tools/fixturegen -seed 42 -out <dir>`, loaded with `internal/db/fixtureload.Load` into a fresh `dbtest` database, then `VACUUM (ANALYZE)`. The owner account of the load gets a random throwaway password (never printed) and the persona's three timezone periods, as in the J09.9 benchmark.
- Binary: `CGO_ENABLED=0 go build -tags webui -trimpath ./cmd/vitamux` after `npm --prefix web run build`. Server settings follow the Compose file: `VITAMUX_ENV=production`, `GOMEMLIMIT=400MiB`, secrets from files in a temp directory, `GOMAXPROCS=2` to match the 2-vCPU test VM (the host has 18 cores).
- PostgreSQL: a throwaway `postgres:18-alpine` container with `--memory 1g` and `shared_buffers=128MB`, as in `deploy/compose/compose.yaml`; the dev database was not touched. Its memory and CPU come from the container's cgroup (`memory.stat`, `cpu.stat`) every second (every 10 s while idle: each `docker exec` costs the container about 4 ms of CPU, which would otherwise show up as 1.7 % idle CPU). Busy-phase `postgres` CPU includes about 0.4 pp of that.
- `vitamux` memory and CPU: `ps -o rss,time` every 250 ms for phase peaks, and `getrusage` of the exited process (`ru_maxrss`) for whole-run maximums. A fresh process per phase.
- (b): rows of `resolution_dirty` are inserted backdated beyond `MarkSettle`, the `rebuild_aggregates` job is queued and the harness waits for the job and the marks to clear.
- (c): the resolved endpoints (`GET /api/v1/resolved/*`) are not routed yet (`planned` in [`api/authz.yaml`](../api/authz.yaml)), so a helper process of the harness runs `resolve.Run` as the J09.9 benchmark does: ten metrics (`steps`, `distance_walk_run`, `active_energy`, `resting_heart_rate`, `resting_heart_rate_nocturnal`, `weight`, `body_fat_ratio`, `blood_pressure`, `sleep`, `spo2`) over 2025-03-02..05-30, three rounds with `resolved_cache` emptied, then ten warm rounds. The JSON layer will add to these numbers.
- (d): `POST /api/v1/exports` (`ndjson`, `include_raw`) and download over HTTP with a signed-in owner session.
- Container check: the same binary built for linux/arm64 ran on `distroless/static` with `--memory 512m`, `GOMEMLIMIT=400MiB`, read-only root and `cap_drop ALL` against the same database, once idle and once for the whole-year rebuild.
- Reproduce: the header of [`tools/resourcebudget/resourcebudget_integration_test.go`](../tools/resourcebudget/resourcebudget_integration_test.go) (about 11 minutes; the import adds 11, `VITAMUX_BUDGET_SKIP_IMPORT=1` skips it; `VITAMUX_BUDGET_CONTAINER_BIN` and `VITAMUX_BUDGET_SHARE_DIR` add the container check).

## Hardware and software

Apple M5 Pro (18 cores), 64 GiB RAM, macOS 26.6 (Darwin 25.6.0); `vitamux` and the harness ran natively on macOS. Docker Desktop 29.5 VM with 3.8 GiB RAM and 2 vCPUs hosting PostgreSQL 18.6 (`aarch64-musl`, kernel 6.8) with a 1 GiB container limit. Go 1.27.1. Linux numbers differ in detail: the container check gave 24 MiB idle against 25 MiB RSS on macOS, so the idle figures carry over; allocation-heavy peaks may differ.

## Deviations

- **D1 (fixed). Whole-year `rebuild_aggregates` needed 1.4-1.8 GiB and was OOM-killed under the Compose limit.** `rebuildHours` (`internal/resolve/aggregates.go`) loaded every row of a run of consecutive marked days (a year of `heart_rate` is 6.1 M rows). It now aggregates in SQL (`RebuildHourlyAggregates` in `internal/db/queries/cache.sql`), 31 local days per statement, with JIT off for the transaction: 27 MiB RSS and 9.4 s for the year, `postgres` 193 MiB; the aggregates are bit-identical to the Go builder's on the whole year. Marks are still consumed 1,000 per transaction, and the job saves a checkpoint after each.
- **D2 (fixed). A cold 90-day dashboard of ten metrics peaked at 1.7-2.1 GiB.** Resolution loaded every row of a metric's range before resolving its first date (`resting_heart_rate_nocturnal` held 1.5 M heart-rate rows). The loader now reads rows as it walks the dates (`slider` in `internal/resolve/load.go`, about 50,000 rows per read) and drops those behind the current date, and passes metric ids to the row query so PostgreSQL plans sparse metrics with an index scan instead of a parallel seq scan (the wear-bucket query keeps its code lookup: with the dense `heart_rate` id it picked a slower seq scan). Ten parallel cold requests: 382 MiB, 4.0-4.4 s on 2 vCPUs (in a row: 204 MiB); warm unchanged at 43-44 MiB and 42-87 ms. Results are unchanged: identical values and explanations for the ten metrics over the 90 days, `resolve verify` 0 diffs; only clock times in explanations now use the owner's zone instead of the process zone. Single cold requests and warm p95 are in [baseline#resolution-and-cache](benchmarks/baseline.md#resolution-and-cache).
- **D3. Idle RSS after a sign-in is 89 MiB, not 80.** The Argon2id password hash allocates 64 MiB (`internal/auth/password.go`) and the Go runtime kept it for the two minutes measured. The target is raised to ≤ 100 MiB; the idle total stays well under 350 MiB.
- **D4. `postgres` private memory reaches 285 MiB with ten parallel cold requests** (328 MiB before the D2 fix), ten backends at once, against 270 MiB; the full-year rebuild now stays at 193 MiB. Its working set ran at 995-1,020 MiB of the 1 GiB limit during bulk work; that is page cache and was reclaimed without an OOM kill.
- **D5. `vitamux import ndjson` takes 10 m 52 s for the year**, 2.4 times the COPY load (about 9,500 rows/s); memory stays at 30 MiB. No target applies; noted for restore and migration planning.
- The "Core + one optional sidecar" row and the 1-vCPU claim are not verified (no sidecar exists in the MVP; the test VM has 2 vCPUs).
