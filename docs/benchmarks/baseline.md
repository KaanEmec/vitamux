# Volume and performance baseline

Measured by J04.4 on one synthetic year ([fixtures/README.md](../../fixtures/README.md)) loaded into the canonical tables. Validates the estimates in [data-model#volume-and-partitioning](../architecture/data-model.md#volume-and-partitioning) and ADR-005 ([overview](../architecture/overview.md#key-decisions)). Numbers are for one machine and one run: use them for orders of magnitude and regressions, not as a promise.

## Method

- Dataset: `fixturegen -seed 42`, 2025-01-01 for 365 days. 6,211,856 `measurements` rows, of which 6,142,352 are `heart_rate` (6 s wrist source 5.0 M, minute relay 0.5 M, irregular Apple Watch 0.6 M, a few manual), plus steps, daily values, 749 groups, 637 sleep sessions (10,956 stages) and 2,110 raw payload rows.
- Loader: `internal/db/fixtureload` (binary COPY through `db.DB.CopyFrom`, 10,000-row chunks, all foreign keys and indexes active, dedupe keys as in [data-model](../architecture/data-model.md#identifiers-and-dedupe-keys)). Revisions are not applied, so every row is an active original.
- `VACUUM (ANALYZE) measurements` after the load, then queries from the app role over its pool. Timings are warm cache (one discarded run each) and include draining every row into Go.
- Environment: PostgreSQL 18.6 (`postgres:18-alpine` from `deploy/compose/compose.dev.yaml`, Docker Desktop VM with 3.8 GiB RAM), default `shared_buffers=128MB`, no container limits, Apple Silicon host. The 1 GiB budget limit was not enforced during the run.
- Reproduce: `VITAMUX_VOLUME_BASELINE=1 go test -tags integration -run TestVolumeBaseline -v -timeout 30m ./internal/db/` (about 5 min; add `VITAMUX_VOLUME_DATASET=fixtures/generated` after `make fixtures` to reuse a dataset).

## Storage

| | Bytes | Per row |
| --- | --- | --- |
| Heap (`measurements`) | 1,200,635,904 (1.12 GiB) | 193 |
| Indexes | 926,613,504 (0.86 GiB) | 149 |
| **Total** | **2,127,618,048 (1.98 GiB)** | **342** |

| Index | Bytes | Per row |
| --- | --- | --- |
| `measurements_metric_start_idx` (user, metric, start_at; partial) | 463,446,016 | 74.6 |
| `measurements_dedupe_idx` (unique, partial) | 323,330,048 | 52.1 |
| `measurements_pkey` | 139,550,720 | 22.5 |
| all others (daily_value, group, 2 BRIN, 2 rare-reference) | < 0.3 MB together | ~0 |

Every other table is under 2 MB for the year (`sleep_stages` 1.6 MB, `raw_payloads` 0.9 MB, `measurement_groups` 0.4 MB). Raw blobs live on the filesystem ([ADR-004](../architecture/overview.md#key-decisions)) and were not generated; the 0.3-0.6 GB/yr estimate is unchanged and unverified here.

## Queries

| Query | Rows read | p50 | p95 | Target |
| --- | --- | --- | --- | --- |
| One local day of `heart_rate`, all sources, by `start_at` range (100 days) | ~16,750 | 4.7 ms | 7.1 ms | < 50 ms |
| 90-day daily aggregate of `heart_rate` per (day, provider, device): count, avg, min, max (12 windows) | ~1.4 M | 293 ms | 338 ms | none set |
| 90 days of `steps` `daily_value` rows (12 windows) | 89 | 0.39 ms | 0.45 ms | none set |
| One local day of `heart_rate` by `local_date = $1` only | ~16,500 | 427 ms | 535 ms | n/a |

The first row uses `measurements_metric_start_idx` (Index Scan). The last row has no `start_at` bound, so the planner falls back to a parallel sequential scan: the `local_date` index exists only for `daily_value` rows.

Load throughput: 6.2 M rows in 4 min 36 s, about 22,500 rows/s.

## Findings

- **Day windows must bound `start_at`.** Resolution should turn a local day into the instant range of that day in the user's timezone (as the benchmark does) and may add `local_date` as a filter. A bare `local_date =` predicate over sample or interval rows scans the table. Not worth an index at one owner while the range form takes 5 ms; revisit if a caller cannot compute the range.
- **Index overhead is 149 B/row, about 44 % of the total.** The two partial b-trees on `start_at` and `dedupe_key` are 85 % of it. Their entries average well above the minimum (`metric_start_idx` is 75 B/row where about 45 is the floor), most likely because eight sources interleave their inserts and random hash keys split pages at 50 %; a `REINDEX` would show how much is bloat. Not verified. If size matters later, the options are `REINDEX` after backfills and a narrower leading key, neither needed now.
- No schema or index change is required by these results, so no migration was written.

## ADR-005 check

ADR-005 and [data-model](../architecture/data-model.md#volume-and-partitioning) estimated about 5.3 M rows and 1.1-1.3 GB with indexes for one 6 s source, i.e. 210-245 B/row. Measured: 342 B/row, or about 1.8 GB for 5.3 M rows. That is 1.4-1.65 times the estimate, inside the 2x tolerance, so ADR-005 stands and no new ADR is written. The whole dataset (6.2 M rows from several overlapping sources) takes 2.1 GB, within the 1-3 GB/yr total. The estimate in data-model was raised to 340 B/row.

## CI smoke test

`TestVolumeSmoke` (`go test -tags integration ./internal/db/`) generates a 30-day slice (2025-03-01, 0.5 M heart rate rows, includes the spring DST change), loads it (about 20 s) and checks, with thresholds roughly 70-100 times the measured values:

| Check | Threshold | Measured (30 days) |
| --- | --- | --- |
| one-day `heart_rate` p95 | 500 ms | 5 ms |
| 30-day aggregate p95 | 5 s | 38 ms |
| `daily_value` query p95 | 500 ms | 0.4 ms |
| bytes per `measurements` row, indexes included | 600 | 344 |
| plan uses `measurements_metric_start_idx` and `measurements_daily_value_idx` | required | yes |

The plan checks catch a lost or unusable index; the timing limits only catch order-of-magnitude regressions.
