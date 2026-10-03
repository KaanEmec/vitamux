# ADR-0009 Sleep date and night window

Status: Accepted · Date: 2026-10-03 · Deciders: owner

## Context
Sleep crosses midnight, naps happen in the afternoon or evening, and owners travel. Every sleep code, `local_night` window and nightly metric (HRV, SpO2, nocturnal RHR) needs one rule for which date a sleep belongs to ([resolution.md › Windows](../architecture/resolution.md#windows), [Sleep episode alignment](../architecture/resolution.md#sleep-episode-alignment)).

## Decision
- **`sleep_sessions.sleep_date`** is the local calendar date of `end_at` (waking up), with the same zone precedence as every local date: record offset, record zone, then timezone periods. The canonical writer already stores it this way.
- **Night D** (window `local_night`, key `D`) covers sessions that end in `[D−1 at anchor, D at anchor)`, local wall-clock time in the zone in effect at each bound. The anchor is `quality.sleep.night_anchor`, default `18:00`, allowed `12:00`–`23:59`, so a morning wake-up always belongs to the night of that date.
- **Night of a session** (`resolve.NightOf`): its wake date if it ends before the anchor time of day, else the next date. It equals `sleep_date` for every session ending before the anchor.
- The **main episode** of night D is the aligned episode with the largest union span among those candidates. Others are secondary (naps unless `include_naps`). A `local_night` value for an intensive metric reads samples inside the main episode's span.
- **`sleep_episode`** windows are keyed by the episode's UTC start and carry their night date.
- **DST and travel:** bounds are wall-clock times, so a night over a DST change is 23 or 25 hours, and a night over a period change runs from the anchor in the old zone to the anchor in the new one.

## Alternatives considered
- **Start date ("night of the 14th")**: matches how people speak, but a session starting after midnight would move to the next night.
- **Pure wake date with no anchor:** an evening nap ending at 19:00 would join the night that ended that morning.
- **UTC dates:** wrong for everyone outside UTC.

## Consequences
- A session ending at or after the anchor belongs to night `sleep_date + 1`. The writer marks `resolution_dirty` for `sleep_date`, so J09.9 must also invalidate night `sleep_date + 1` for sleep-derived metrics, and readers of night D query `sleep_date IN (D−1, D)`.
- Changing an anchor is a rule edit, so it creates a new rule version and its own cache entries.
