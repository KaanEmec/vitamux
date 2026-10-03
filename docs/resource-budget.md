# Resource budget: measured

Measured by J14.3 on the one-year synthetic dataset ([fixtures/README.md](../fixtures/README.md); `fixturegen -seed 42`, 6,211,856 `measurements` rows, same dataset as the [J04.4 and J09.9 baseline](benchmarks/baseline.md)). The targets are in [project#resource-budget](architecture/project.md#resource-budget). One machine, one to three runs per figure: use them for orders of magnitude, not as a promise.

## Verdict

Routine work is inside the budget. Two operations are far over it and are recorded as deviations, not fixed here: `rebuild_aggregates` after a whole year is marked dirty, and a cold 90-day dashboard of ten metrics (see [Deviations](#deviations)).

| Target (core profile) | Measured | Verdict |
| --- | --- | --- |
| `vitamux` idle RSS ≤ 80 MiB | 25 MiB fresh (24 MiB in a Linux container); 89 MiB after one sign-in | Over after sign-in; target raised to ≤ 100 MiB (D3) |
| `postgres` idle ≤ 270 MiB | 178 MiB private memory (anon + shared buffers) | Within |
| Idle total ≤ 350 MiB | 203 MiB fresh; 267 MiB after sign-in | Within |
| Idle CPU < 2 % of a core | `vitamux` 0.0 % (under 1 % in any one second), `postgres` 0.2-0.5 % | Within |
| Peak total ≤ 900 MiB, routine work | export of the year 326 MiB; one-week `rebuild_aggregates` 254 MiB; warm dashboard 228 MiB | Within |
| Peak total ≤ 900 MiB, bulk work | full-year `rebuild_aggregates` 1.4-1.8 GiB, OOM-killed at the 512 MiB limit; cold dashboard 1.7-2.1 GiB | **Over** (D1, D2) |
| `postgres` peak ≤ 270 MiB | 172-228 MiB in most phases; 316-328 MiB in the full-year rebuild (one of two runs) and with ten parallel cold requests | Over in bulk work (D4) |
| Compose limits: `vitamux` 512 MiB, `postgres` 1 GiB | `postgres` working set reached 995-1,020 MiB of 1 GiB (page cache, reclaimable; no OOM kill); `vitamux` D1, D2 | See D1, D2 |
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
| (b) `rebuild_aggregates`, whole year marked (3,229 marks) | 7.8-8.4 s | **1,433-1,757 MiB** | 132 % / 179 % | 203-316 MiB | 42 % |
| (c) Dashboard, cold, ten requests in a row | 6.9-12.3 s per round | **2,024-2,148 MiB** | 0.6-0.8 cores / 1.9 | 198-228 MiB | 69-73 % |
| (c) Dashboard, cold, ten in parallel | 6.1-13.7 s per round | **1,740-1,905 MiB** | 0.5-0.9 cores / 1.9 | 322-328 MiB | 99-111 % |
| (c) Dashboard, warm, ten in parallel | 42-79 ms per round | 43 MiB | negligible | 182-185 MiB | negligible |
| (d) Export of the year with raw content, and download | 43-47 s | 143-144 MiB | 39-51 % / 46-59 % | 182 MiB | 69-85 % |

- Load by COPY is the J04.4 method (23,000 rows/s here, 22,500 there). `vitamux import` is the only path that loads a whole year inside the `vitamux` process, so it is the figure for its memory; it is bounded (30 MiB) but takes 2.4 times as long as COPY.
- Rebuild peaks are whole-process maximums from `getrusage`; so are the export and idle ones. The dashboard rows are the helper process's maximums (its own idle is 17 MiB).
- Run-to-run spread in the cold dashboard wall times (6.1 s to 13.7 s in parallel) came from the Docker VM's page cache, not from the code: the VM has 3.8 GiB for a 2 GiB database plus the 1 GiB limit.

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

- **D1. Whole-year `rebuild_aggregates` needs 1.4-1.8 GiB and is OOM-killed under the Compose limit.** In the 512 MiB container the kernel killed the process (`OOMKilled=true`, exit 137) and the job stayed leased. Cause: `rebuildHours` (`internal/resolve/aggregates.go`) loads every row of a run of consecutive marked days for one (user, metric) into memory before aggregating, so the claim batch of 1,000 marks does not bound memory: a year of `heart_rate` is 6.1 M rows. A normal sync marks a few days (7 days: 64 MiB, 0.3 s). A year is marked after `vitamux import`, `reprocess` or a backfill of that size. Fix later (not here): rebuild in bounded date chunks or aggregate in SQL. Until then a large backfill can kill the process.
- **D2. A cold 90-day dashboard of ten metrics peaks at 1.7-2.1 GiB and takes 6-7 s on 2 vCPUs.** It is the known J09.9 result (4.5 s on 18 cores; [baseline#resolution-and-cache](benchmarks/baseline.md#resolution-and-cache)) and now with memory: resolution materializes every row of a metric's range, and the dense heart-rate based metrics (`resting_heart_rate_nocturnal`, wear gate) hold 1.5 M rows each per 90 days. Ten parallel requests multiply it. Not run under a container limit (the endpoints are not routed), but it exceeds 512 MiB by 3-4 times. Warm, it is 42-79 ms and 43 MiB, so only the first view after new data or a rule change pays. The J09.9 note stands: load only the night windows' rows for night metrics, and bound memory per request.
- **D3. Idle RSS after a sign-in is 89 MiB, not 80.** The Argon2id password hash allocates 64 MiB (`internal/auth/password.go`) and the Go runtime kept it for the two minutes measured. The target is raised to ≤ 100 MiB; the idle total stays well under 350 MiB.
- **D4. `postgres` private memory reached 328 MiB with ten parallel cold requests**, ten backends at once, against 270 MiB. It follows D2 and goes away with it. Its working set ran at 995-1,020 MiB of the 1 GiB limit during bulk work; that is page cache and was reclaimed without an OOM kill.
- **D5. `vitamux import ndjson` takes 10 m 52 s for the year**, 2.4 times the COPY load (about 9,500 rows/s); memory stays at 30 MiB. No target applies; noted for restore and migration planning.
- The "Core + one optional sidecar" row and the 1-vCPU claim are not verified (no sidecar exists in the MVP; the test VM has 2 vCPUs).
