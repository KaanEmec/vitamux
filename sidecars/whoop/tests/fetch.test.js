import { test } from "node:test";
import assert from "node:assert/strict";
import { start, json, authResult, creds, ACCESS, REFRESH, USER_ID } from "./helpers.js";

const METRICS = `/metrics-service/v1/metrics/user/${USER_ID}`;
// A window request (correction or backfill): from and to, and the owner's timezone in config.
const req = (over) => ({ stream: "whoop.heart_rate", from: "2026-01-01T06:00:00Z", to: "2026-01-03T00:00:00Z", credentials: creds(), config: { timezone: "UTC" }, ...over });

// Verbatim JSON text including things JSON.parse would change: a big integer and odd spacing.
const HR_TEXT = '{ "values": [ {"time": 1767225600000, "data": 61}, {"time": 1767225606000, "data": 62} ], "big": 9007199254740993, "extra": {"a":1} }';

test("heart rate window: verbatim raw, envelope, sha256, cursor", async (t) => {
  const s = await start({ [METRICS]: () => json(HR_TEXT) });
  t.after(s.stop);
  const r = await s.call("POST", "/v1/fetch", req());
  assert.equal(r.status, 200);
  assert.equal(r.type, "application/x-ndjson");
  assert.ok(r.text.includes('"response":' + HR_TEXT)); // text untouched
  const [raw, result] = r.lines();
  assert.equal(raw.type, "raw");
  assert.equal(raw.external_key, "whoop.heart_rate:2026-01-01T00:00:00.000Z");
  assert.equal(raw.content_type, "application/json");
  assert.deepEqual(raw.body.unit, { start: "2026-01-01T00:00:00.000Z", end: "2026-01-02T00:00:00.000Z" });
  assert.equal(raw.body.response.values.length, 2);
  assert.deepEqual(raw.request.params, { start: "2026-01-01T00:00:00.000Z", end: "2026-01-02T00:00:00.000Z", step: "6", name: "heart_rate", apiVersion: "7" });
  assert.equal(raw.request.endpoint, METRICS);
  assert.deepEqual(result, { type: "result", next_cursor: { next: "2026-01-02T00:00:00.000Z" }, done: false });
  assert.equal(r.protocol, "vitamux-connector/1");
  assert.equal(s.calls[0].auth, `Bearer ${ACCESS}`);

  // Second window ends at the requested end.
  const r2 = await s.call("POST", "/v1/fetch", req({ cursor: result.next_cursor }));
  const [raw2, result2] = r2.lines();
  assert.equal(raw2.external_key, "whoop.heart_rate:2026-01-02T00:00:00.000Z");
  assert.equal(raw2.request.params.end, "2026-01-03T00:00:00.000Z");
  assert.deepEqual(result2, { type: "result", done: true, high_watermark: "2026-01-03T00:00:00.000Z" }); // a finished window leaves the stream cursor alone
});

test("heart rate: step is configurable, end is clipped, empty windows emit nothing", async (t) => {
  let text = '{"values":[]}';
  const s = await start({ [METRICS]: () => json(text) }, { hrStep: 60 });
  t.after(s.stop);
  let lines = (await s.call("POST", "/v1/fetch", req({ to: "2026-01-01T12:00:00Z" }))).lines();
  assert.equal(lines.length, 1); // result only
  assert.equal(lines[0].done, true);
  assert.equal(lines[0].high_watermark, "2026-01-01T12:00:00.000Z");
  assert.equal(s.calls[0].params.step, "60");
  assert.equal(s.calls[0].params.end, "2026-01-01T12:00:00.000Z");
  text = '{"values":[{"time":1,"data":2}]}';
  lines = (await s.call("POST", "/v1/fetch", req({ to: "2026-01-01T12:00:00Z" }))).lines();
  assert.equal(lines[0].body.unit.end, "2026-01-02T00:00:00.000Z"); // unit stays the full window: unchanged data hashes the same
});

// The cycles BFF's real shape (synthetic values): {records: [{cycle, recovery, sleeps, workouts, v2_activities}]}.
const SLEEP = (activity_id, is_nap, extra = {}) => ({ activity_id, during: "['2026-01-01T22:00:00.000Z','2026-01-02T06:00:00.000Z')", timezone_offset: "+03:00", is_nap, significant: !is_nap, score: 80, ...extra });
const CYCLES = [
  { cycle: { id: 1, days: "['2026-01-02','2026-01-03')", timezone_offset: "+03:00" }, recovery: { activity_id: "s-main", recovery_score: 70 }, sleeps: [SLEEP("s-main", false), SLEEP("s-nap", true)], v2_activities: [{ id: "s-main", type: "sleep", score_type: "SLEEP" }, { id: "w-run", type: "running", score_type: "CARDIO" }] },
  { cycle: { id: 2, days: "['2026-01-03','2026-01-04')" }, recovery: null, sleeps: [SLEEP("s-gone", false, { timezone_offset: undefined })] },
  { cycle: { id: 3, days: "['2026-01-03','2026-01-04')" }, recovery: { activity_id: "s-main" }, sleeps: [SLEEP("s-main", false)] }, // seen twice: fetched once
];

test("cycles: the window response verbatim, one item", async (t) => {
  const s = await start({ "/core-details-bff/v0/cycles/details": () => json({ records: CYCLES }) }); // wrapped shape: the client unwraps it, the raw stays wrapped
  t.after(s.stop);
  const [raw, result] = (await s.call("POST", "/v1/fetch", req({ stream: "whoop.cycles", to: "2026-01-04T00:00:00Z" }))).lines();
  assert.match(raw.external_key, /^whoop\.cycles:\d{4}-\d\d-\d\dT00:00:00\.000Z$/);
  assert.equal(raw.body.response.records.length, 3);
  assert.equal(raw.request.params.limit, "200");
  assert.equal(raw.request.params.id, String(USER_ID));
  assert.equal(result.done, true);
});

test("sleep: one raw per sleep of the window's cycles, naps included, each fetched once", async (t) => {
  const events = [{ during: "['2026-01-01T22:00:00.000Z','2026-01-01T23:00:00.000Z')", type: "light" }];
  const s = await start({
    "/core-details-bff/v0/cycles/details": () => json({ records: CYCLES }),
    "/sleep-service/v1/sleep-events": ({ params }) => (params.activityId === "s-gone" ? json({}, 404) : json(events)),
  });
  t.after(s.stop);
  const lines = (await s.call("POST", "/v1/fetch", req({ stream: "whoop.sleep", to: "2026-01-04T00:00:00Z" }))).lines();
  const raws = lines.slice(0, -1);
  assert.deepEqual(raws.map((x) => x.external_key), ["whoop.sleep:s-main", "whoop.sleep:s-nap"]); // s-gone is 404: skipped
  assert.deepEqual(s.calls.filter((c) => c.key === "/sleep-service/v1/sleep-events").map((c) => c.params.activityId), ["s-main", "s-nap", "s-gone"]);
  assert.deepEqual(raws[0].body.unit, { id: "s-main", is_nap: false, timezone_offset: "+03:00" });
  assert.deepEqual(raws[1].body.unit, { id: "s-nap", is_nap: true, timezone_offset: "+03:00" });
  assert.deepEqual(raws[0].body.response, events);
  assert.deepEqual(raws[0].request, { endpoint: "/sleep-service/v1/sleep-events", params: { activityId: "s-main", apiVersion: "7" } });
  assert.equal(lines.at(-1).done, true);
});

test("sleep: a sleep without activity_id or is_nap, or events that are not an array, is schema_drift", async (t) => {
  let records = [{ cycle: { id: 1 }, sleeps: [{ activity_id: "s-1" }] }];
  const s = await start({ "/core-details-bff/v0/cycles/details": () => json({ records }), "/sleep-service/v1/sleep-events": () => json({ stages: [] }) });
  t.after(s.stop);
  let r = await s.call("POST", "/v1/fetch", req({ stream: "whoop.sleep", to: "2026-01-04T00:00:00Z" }));
  assert.equal(r.json().code, "schema_drift");
  records = [{ cycle: { id: 1 }, sleeps: [SLEEP("s-1", false)] }];
  r = await s.call("POST", "/v1/fetch", req({ stream: "whoop.sleep", to: "2026-01-04T00:00:00Z" }));
  assert.deepEqual([r.json().code, r.json().endpoint], ["schema_drift", "/sleep-service/v1/sleep-events"]);
});

const DEV = (records, next_token = null) => ({ records, next_token });
const W = (id, start, extra = {}) => ({ id, start, end: new Date(Date.parse(start) + 3_600_000).toISOString(), sport_name: "x", score_state: "SCORED", ...extra });

test("workouts: one raw per in-window record plus weightlifting detail where it exists", async (t) => {
  const page = DEV([W("w-new", "2026-01-03T10:00:00Z"), W("w-lift", "2026-01-02T10:00:00Z", { unknown_field: 1 }), W("w-old", "2025-12-01T10:00:00Z")], "tok-2");
  const s = await start({
    "/developer/v2/activity/workout": () => json(page),
    "/weightlifting-service/v2/weightlifting-workout/w-lift": () => json({ activity_id: "w-lift", workout_groups: [] }),
    "/weightlifting-service/v2/weightlifting-workout/w-new": () => json({}, 404),
  });
  t.after(s.stop);
  const r = await s.call("POST", "/v1/fetch", req({ stream: "whoop.workouts", to: "2026-01-04T00:00:00Z" }));
  const lines = r.lines();
  assert.deepEqual(lines.slice(0, -1).map((x) => x.external_key), ["whoop.workouts:w-new", "whoop.workouts:w-lift", "whoop.workouts:w-lift:weightlifting"]);
  const [, rec, lift] = lines;
  assert.deepEqual(rec.body.unit, { id: "w-lift" });
  assert.ok(r.text.includes('"response":' + JSON.stringify(page.records[1]) + "}")); // the record verbatim, nothing dropped
  assert.equal(rec.request.endpoint, "/developer/v2/activity/workout");
  assert.deepEqual(lift.body.unit, { id: "w-lift" });
  assert.equal(lift.request.endpoint, "/weightlifting-service/v2/weightlifting-workout/w-lift");
  // w-old is before the window: oldest record < start, so paging stops.
  assert.deepEqual(lines.at(-1), { type: "result", done: true, high_watermark: "2026-01-04T00:00:00.000Z" });
  assert.equal(s.calls.filter((c) => c.key.includes("weightlifting")).length, 2); // w-old not asked
});

test("workouts: a record's raw is sliced byte-exact from the page", async (t) => {
  const rec = '{ "id" : "w-1", "start":"2026-01-02T10:00:00Z", "end":"2026-01-02T11:00:00Z", "note": "a \\"}]\\" b", "big": 9007199254740993, "zones": [{"z": [1, 2]}] }';
  const text = `{"next_token": null, "records" : [ ${rec} ], "after": {"records": []}}`;
  const s = await start({ "/developer/v2/activity/workout": () => json(text), "/weightlifting-service/v2/weightlifting-workout/w-1": () => json({}, 404) });
  t.after(s.stop);
  const r = await s.call("POST", "/v1/fetch", req({ stream: "whoop.workouts", to: "2026-01-04T00:00:00Z" }));
  assert.ok(r.text.includes('"response":' + rec + "}"));
  assert.equal(r.lines()[0].external_key, "whoop.workouts:w-1");
});

test("workouts: follows next_token while the window is not reached, and refuses a repeated token", async (t) => {
  const s = await start({
    "/developer/v2/activity/workout": ({ params }) => json(params.nextToken ? DEV([W("a", "2026-01-02T00:00:00Z")], params.nextToken) : DEV([W("b", "2026-01-03T00:00:00Z")], "tok-1")),
    "/weightlifting-service/v2/weightlifting-workout/b": () => json({}, 404),
    "/weightlifting-service/v2/weightlifting-workout/a": () => json({}, 404),
  });
  t.after(s.stop);
  const first = (await s.call("POST", "/v1/fetch", req({ stream: "whoop.workouts", to: "2026-01-04T00:00:00Z" }))).lines().at(-1);
  assert.deepEqual([first.done, first.next_cursor], [false, { next_token: "tok-1" }]);
  assert.equal(s.calls[0].params.limit, "25");
  // The stub answers a request with token tok-1 by returning tok-1 again: reported, not looped on.
  const r = await s.call("POST", "/v1/fetch", req({ stream: "whoop.workouts", to: "2026-01-04T00:00:00Z", cursor: { next_token: "tok-1" } }));
  assert.equal(r.status, 502);
  assert.equal(r.json().code, "schema_drift");
});

test("workouts: an empty page ends paging even with a next_token", async (t) => {
  const s = await start({ "/developer/v2/activity/workout": () => json(DEV([], "tok-more")) });
  t.after(s.stop);
  const lines = (await s.call("POST", "/v1/fetch", req({ stream: "whoop.workouts" }))).lines();
  assert.deepEqual(lines, [{ type: "result", done: true, high_watermark: "2026-01-03T00:00:00.000Z" }]);
});

test("strain deep dive: one call per local day", async (t) => {
  const s = await start({ "/home-service/v1/deep-dive/strain": () => json({ CONTRIBUTORS_TILE_STEPS: { value: 9000 } }) });
  t.after(s.stop);
  const r = await s.call("POST", "/v1/fetch", req({ stream: "whoop.strain_deep_dive", from: "2026-03-10T22:00:00Z", to: "2026-03-12T00:00:00Z", config: { timezone: "Europe/Istanbul" } }));
  const [raw, result] = r.lines();
  assert.equal(raw.request.params.date, "2026-03-11"); // 01:00 local
  assert.deepEqual(raw.body.unit, { start: "2026-03-10T21:00:00.000Z", end: "2026-03-11T21:00:00.000Z" });
  assert.equal(raw.external_key, "whoop.strain_deep_dive:2026-03-10T21:00:00.000Z");
  assert.deepEqual([result.done, result.next_cursor], [false, { next: "2026-03-12" }]);
  // DST: New York springs forward on 2026-03-08, that local day is 23 hours long.
  const ny = (await s.call("POST", "/v1/fetch", req({ stream: "whoop.strain_deep_dive", from: "2026-03-08T12:00:00Z", to: "2026-03-08T13:00:00Z", config: { timezone: "America/New_York" } }))).lines()[0];
  assert.deepEqual(ny.body.unit, { start: "2026-03-08T05:00:00.000Z", end: "2026-03-09T04:00:00.000Z" });
  assert.equal((await s.call("POST", "/v1/fetch", req({ stream: "whoop.strain_deep_dive", config: { timezone: "Mars/Olympus" } }))).json().code, "permanent");
});

test("an expired access token is refreshed before use and returned", async (t) => {
  const s = await start({
    InitiateAuth: () => authResult({ AccessToken: "fresh-access", RefreshToken: "rotated" }),
    "/users-service/v2/bootstrap/": () => json({ user: { id: USER_ID } }),
    [METRICS]: () => json({ values: [{ time: 1, data: 2 }] }),
  });
  t.after(s.stop);
  const lines = (await s.call("POST", "/v1/fetch", req({ credentials: creds(10_000) }))).lines();
  assert.equal(s.calls.find((c) => c.key === METRICS).auth, "Bearer fresh-access");
  const c = lines.at(-1).credentials;
  assert.deepEqual([c.access_token, c.refresh_token], ["fresh-access", "rotated"]);
  assert.deepEqual(c.extra, { user_id: USER_ID });
});

test("a 401 refreshes once and retries; a second 401 is reauth_required", async (t) => {
  let denied = 1;
  const s = await start({
    InitiateAuth: () => authResult({ AccessToken: "fresh-access" }),
    "/users-service/v2/bootstrap/": () => json({ user: { id: USER_ID } }),
    [METRICS]: () => (denied-- > 0 ? json("denied", 401) : json({ values: [{ time: 1, data: 2 }] })),
  });
  t.after(s.stop);
  const ok = (await s.call("POST", "/v1/fetch", req())).lines();
  assert.equal(ok.at(-1).credentials.access_token, "fresh-access");
  denied = 5;
  const r = await s.call("POST", "/v1/fetch", req());
  assert.equal(r.status, 401);
  assert.equal(r.json().code, "reauth_required");
  assert.equal(s.calls.filter((c) => c.key === "InitiateAuth").length, 2); // once per call
});

test("error classes", async (t) => {
  let next;
  const s = await start({ [METRICS]: () => next() });
  t.after(s.stop);
  const check = async (make, code, status, extra = {}) => {
    next = make;
    const r = await s.call("POST", "/v1/fetch", req({ credentials: creds() }));
    assert.equal(r.type, "application/problem+json");
    const p = r.json();
    assert.equal(r.status, status);
    assert.deepEqual({ code: p.code, status: p.status, ...extra }, { code, status, ...extra });
    assert.ok(typeof p.type === "string" && typeof p.title === "string");
    assert.equal(r.protocol, "vitamux-connector/1");
    if (code === "schema_drift") assert.ok(p.endpoint === METRICS && /^[0-9a-f]{64}$/.test(p.fingerprint), "drift names the endpoint and a shape fingerprint");
    if (code === "rate_limited") assert.equal(r.retryAfter, String(p.retry_after_s));
    for (const secret of [ACCESS, REFRESH, "internal detail"]) assert.ok(!r.text.includes(secret));
  };
  await check(() => json("internal detail", 429, { "retry-after": "17" }), "rate_limited", 429, { retry_after_s: 17 });
  await check(() => json("internal detail", 503), "transient", 503);
  await check(() => json("internal detail", 500), "transient", 503);
  await check(() => {
    throw new TypeError("fetch failed");
  }, "transient", 503);
  await check(() => json("internal detail", 400), "schema_drift", 502); // metrics-service rejects the metric
  await check(() => json("<html>", 200), "schema_drift", 502); // not JSON
  await check(() => json({ rows: [] }), "schema_drift", 502); // no values array
  await check(() => json({ values: [{ t: 1 }] }), "schema_drift", 502); // sample changed shape
  await check(() => json("gone", 404), "schema_drift", 502);
  assert.ok(!s.logs.join("\n").includes("internal detail"));
});

test("any other client error is permanent", async (t) => {
  const s = await start({ "/core-details-bff/v0/cycles/details": () => json("internal detail", 400) });
  t.after(s.stop);
  const r = await s.call("POST", "/v1/fetch", req({ stream: "whoop.cycles" }));
  assert.equal(r.status, 500);
  assert.equal(r.json().code, "permanent");
  assert.ok(!r.text.includes("internal detail"));
});

test("a malformed developer workout page is schema_drift", async (t) => {
  const s = await start({ "/developer/v2/activity/workout": () => json({ records: "nope" }) });
  t.after(s.stop);
  const r = await s.call("POST", "/v1/fetch", req({ stream: "whoop.workouts" }));
  assert.equal(r.json().code, "schema_drift");
});

test("bad requests", async (t) => {
  const s = await start({});
  t.after(s.stop);
  assert.equal((await s.call("POST", "/v1/fetch", req({ stream: "whoop.nope" }))).status, 400);
  assert.equal((await s.call("POST", "/v1/fetch", req({ from: "yesterday" }))).status, 400);
  assert.equal((await s.call("POST", "/v1/fetch", req({ credentials: "x" }))).status, 400);
  assert.equal((await s.call("GET", "/v1/nope")).status, 404);
});

// Incremental and manual runs send no window: the stored cursor {since} (or one lookback ago) to now.
test("incremental: runs from the stored cursor to now and stores {since: now}", async (t) => {
  const now = Date.parse("2026-01-03T10:00:00Z");
  const s = await start({ [METRICS]: () => json({ values: [{ time: 1, data: 2 }] }) }, { now: () => now });
  t.after(s.stop);
  const { stream, credentials } = req();
  const first = (await s.call("POST", "/v1/fetch", { stream, mode: "incremental", credentials, cursor: { since: "2026-01-02T22:00:00Z" } })).lines();
  assert.deepEqual(first.slice(0, -1).map((x) => x.external_key), ["whoop.heart_rate:2026-01-02T00:00:00.000Z"]); // the grid window holding `since`
  assert.deepEqual(first.at(-1), { type: "result", next_cursor: { since: "2026-01-02T22:00:00.000Z", next: "2026-01-03T00:00:00.000Z" }, done: false });
  const last = (await s.call("POST", "/v1/fetch", { stream, mode: "incremental", credentials, cursor: first.at(-1).next_cursor })).lines();
  assert.equal(last[0].external_key, "whoop.heart_rate:2026-01-03T00:00:00.000Z");
  assert.deepEqual(last.at(-1), { type: "result", next_cursor: { since: "2026-01-03T10:00:00.000Z" }, done: true, high_watermark: "2026-01-03T10:00:00.000Z" });
  // The first run of a stream (no cursor) looks back one lookback (48 h for heart rate).
  const fresh = (await s.call("POST", "/v1/fetch", { stream, mode: "incremental", credentials })).lines();
  assert.equal(fresh[0].external_key, "whoop.heart_rate:2026-01-01T00:00:00.000Z");
  assert.equal((await s.call("POST", "/v1/fetch", { stream, credentials, cursor: "x" })).json().code, "permanent");
});

test("replay mode answers from the cassette without touching WHOOP", async (t) => {
  const { replayFetch } = await import("../src/replay.js");
  const cassette = JSON.parse((await import("node:fs")).readFileSync(new URL("../testdata/replay.json", import.meta.url), "utf8"));
  const s = await start({}, { fetch: replayFetch(cassette), replay: true, now: () => Date.parse(cassette.now) });
  t.after(s.stop);
  const lines = (await s.call("POST", "/v1/fetch", { stream: "whoop.heart_rate", credentials: { access_token: "synthetic-access-token" } })).lines();
  assert.equal(lines.length, 2);
  assert.equal(lines[0].body.response.values.length, 2);
  assert.equal(lines.at(-1).next_cursor.since, "2026-09-12T12:00:00.000Z");
  assert.equal(s.calls.length, 0); // the fake WHOOP was never reached
});
