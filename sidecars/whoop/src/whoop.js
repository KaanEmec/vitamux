// WHOOP logic: sign-in, refresh and one page of one stream at a time.
// No HTTP-server or wire-format code here (see server.js).
import { createHash } from "node:crypto";
import { WhoopClient, WHOOP_API_THROTTLE_MS } from "@dofek/whoop";

const DAY = 86_400_000;
const HOUR = 3_600_000;
const CYCLE_LIMIT = 200;
const WORKOUT_PAGE = 25;

// ---------------------------------------------------------------- errors

// One error type for everything the protocol can say. `code` is the problem+json code.
export class SidecarError extends Error {
  constructor(code, title, { retryAfterS = null, status } = {}) {
    super(title);
    this.code = code;
    this.retryAfterS = retryAfterS;
    this.endpoint = null; // schema_drift: the endpoint whose shape changed
    this.fingerprint = null; // schema_drift: hash of the unexpected shape
    this.status = status ?? { reauth_required: 401, rate_limited: 429, transient: 503, schema_drift: 502, permanent: 500 }[code];
  }
}

const drift = (what) => new SidecarError("schema_drift", `unexpected WHOOP response: ${what}`);
const bad = (what) => new SidecarError("permanent", what, { status: 400 });

const REAUTH = new Set(["NotAuthorizedException", "CodeMismatchException", "ExpiredCodeException", "UserNotFoundException", "PasswordResetRequiredException", "UserNotConfirmedException"]);
const LIMITED = new Set(["TooManyRequestsException", "LimitExceededException", "TooManyFailedAttemptsException"]);
const NETWORK = new Set(["ETIMEDOUT", "ECONNRESET", "ECONNREFUSED", "ENOTFOUND", "EAI_AGAIN", "ENETUNREACH", "UND_ERR_CONNECT_TIMEOUT"]);

// Map anything thrown by @dofek/whoop or our own checks to a SidecarError.
// Only error names, status numbers and fixed titles are used: messages and
// bodies from upstream can hold secrets and are never copied out.
export function classify(err) {
  if (err instanceof SidecarError) return err;
  const msg = String(err?.message ?? "");
  const name = err?.name;
  if (name === "WhoopRateLimitError" || name === "ProviderRateLimitError") {
    return new SidecarError("rate_limited", "WHOOP rate limit", { retryAfterS: err.retryAfterSeconds ?? null });
  }
  if (name === "ProviderServiceUnavailableError" || name === "ProviderRequestTimeoutError" || name === "TimeoutError" || name === "AbortError") {
    return new SidecarError("transient", "WHOOP unavailable");
  }
  if (name === "ZodError" || name === "SyntaxError" || name === "WhoopMetricUnavailableError") return drift(name);
  const cognito = /^WHOOP Cognito (\w+):/.exec(msg)?.[1];
  if (cognito) {
    if (REAUTH.has(cognito)) return new SidecarError("reauth_required", "credentials or code refused");
    if (LIMITED.has(cognito)) return new SidecarError("rate_limited", "WHOOP sign-in rate limit");
    if (cognito === "InternalErrorException" || cognito === "ServiceUnavailable") return new SidecarError("transient", "WHOOP sign-in unavailable");
    return new SidecarError("permanent", "WHOOP sign-in failed");
  }
  const status = Number(/(?:API error|auth failed) \((\d{3})\)/.exec(msg)?.[1]);
  if (status === 401 || status === 403) return new SidecarError("reauth_required", "token refused");
  if (status >= 500) return new SidecarError("transient", "WHOOP unavailable");
  if (status === 404) return drift("endpoint not found");
  if (/no tokens in response|could not determine user ID/.test(msg)) return drift("sign-in response");
  if ((err instanceof TypeError && msg === "fetch failed") || NETWORK.has(err?.code) || NETWORK.has(err?.cause?.code)) {
    return new SidecarError("transient", "network error");
  }
  return new SidecarError("permanent", "WHOOP request failed");
}

const apiStatus = (err) => Number(/API error \((\d{3})\)/.exec(String(err?.message))?.[1]) || 0;

// ---------------------------------------------------------------- describe

const h = (n) => n * 3600;
const d = (n) => n * 86400;

// Seconds. Conservative: the 6 s HR stream is ~14,400 samples per day. The journal (self-reported
// behaviours) is left out until the protocol can mark a stream off by default.
const STREAMS = [
  { name: "whoop.heart_rate", interval_s: h(1), lookback_s: h(48), unit_size_s: h(168), max_backfill_s: d(90) },
  { name: "whoop.cycles", interval_s: h(1), lookback_s: h(72), unit_size_s: h(720), max_backfill_s: d(3650) },
  { name: "whoop.sleep", interval_s: h(1), lookback_s: h(72), unit_size_s: h(720), max_backfill_s: d(3650) },
  { name: "whoop.workouts", interval_s: h(1), lookback_s: h(72), unit_size_s: h(720), max_backfill_s: d(3650) },
  { name: "whoop.strain_deep_dive", interval_s: h(6), lookback_s: h(48), unit_size_s: h(720), max_backfill_s: d(365) },
];
const LOOKBACK_MS = Object.fromEntries(STREAMS.map((x) => [x.name, x.lookback_s * 1000]));

export function describe(info) {
  return {
    protocol: "vitamux-connector/1",
    provider: "whoop",
    name: "WHOOP (unofficial)",
    version: info.version,
    official: false,
    auth_kind: "interactive_mfa",
    upstream: { package: "@dofek/whoop", version: info.upstreamVersion, source_url: "https://github.com/Asherlc/dofek/tree/main/packages/whoop-whoop" },
    streams: STREAMS,
    // One request per throttle interval, no bursts.
    rate_limits: [{ requests: 1, per_s: Math.max(1, Math.round((info.throttleMs || WHOOP_API_THROTTLE_MS) / 1000)) }],
    capabilities: { incremental: true, backfill: true, manual_sync: true },
  };
}

// ---------------------------------------------------------------- credentials and auth

// `deps` = { fetch, throttleMs, hrStep, hrWindowH }; the client is always the real WhoopClient.
// Credentials: { access_token, refresh_token, expires_at, extra: { user_id } }.
const pack = (t) => ({
  access_token: t.accessToken,
  refresh_token: t.refreshToken,
  expires_at: new Date(Date.now() + t.expiresInSeconds * 1000).toISOString(),
  extra: { user_id: t.userId },
});
const done = (t) => ({ authorized: { account_id: String(t.userId), credentials: pack(t) } });

function unpack(c) {
  const userId = c?.extra?.user_id;
  if (typeof c?.access_token === "string" && c.access_token && typeof c.refresh_token === "string" && c.refresh_token && Number.isSafeInteger(userId)) {
    return { access_token: c.access_token, refresh_token: c.refresh_token, user_id: userId, expires: Date.parse(c.expires_at) };
  }
  throw bad("malformed credentials");
}

// The MFA state sealed into the opaque `session` (standard base64, as the protocol requires).
const seal = (o) => Buffer.from(JSON.stringify(o)).toString("base64");
function unseal(s) {
  try {
    const o = JSON.parse(Buffer.from(String(s), "base64").toString());
    if (o.session && o.username && (o.method === "totp" || o.method === "sms") && CODE_CHALLENGE.test(o.challenge)) return o;
  } catch {}
  throw bad("invalid session");
}

const str = (v, name) => {
  if (typeof v !== "string" || v === "") throw bad(`${name} required`);
  return v;
};

const field = (name, label, kind) => ({ name, label, kind });

// @dofek/whoop answers every challenge but the app's as SMS_MFA, yet WHOOP also sends codes by
// email (Cognito EMAIL_OTP). So sign-in records the challenge Cognito names, and the answer is
// relabelled to it: <CHALLENGE> with the code in <CHALLENGE>_CODE.
const CODE_CHALLENGE = /^[A-Z_]+_(MFA|OTP)$/;
const target = (init) => new Headers(init?.headers).get("x-amz-target") ?? "";
const HOW = { SOFTWARE_TOKEN_MFA: "the authenticator app code", SMS_MFA: "the SMS code WHOOP sent you", EMAIL_OTP: "the code WHOOP emailed you" };

function recordChallenge(f, seen) {
  return async (url, init) => {
    const res = await f(url, init);
    if (target(init).endsWith(".InitiateAuth")) seen.challenge = (await res.clone().json().catch(() => ({}))).ChallengeName;
    return res;
  };
}

function answerAs(f, challenge) {
  return (url, init) => {
    if (!target(init).endsWith(".RespondToAuthChallenge")) return f(url, init);
    const b = JSON.parse(init.body);
    const { USERNAME, ...code } = b.ChallengeResponses;
    b.ChallengeName = challenge;
    b.ChallengeResponses = { USERNAME, [`${challenge}_CODE`]: Object.values(code)[0] };
    return f(url, { ...init, body: JSON.stringify(b) });
  };
}

// Does not call WHOOP: the first step asks for the sign-in.
export async function authBegin() {
  return { step: { prompt: { message: "Sign in to WHOOP (unofficial access with your own account).", fields: [field("username", "Email", "text"), field("password", "Password", "password")] } } };
}

// Without a session: the answers to the sign-in prompt. With one: the MFA code.
export async function authContinue(input, deps) {
  const values = input?.values ?? {};
  try {
    if (!input?.session) {
      const username = str(values.username, "username");
      const seen = {};
      const r = await WhoopClient.signIn(username, str(values.password, "password"), recordChallenge(deps.fetch ?? globalThis.fetch, seen));
      if (r.type !== "verification_required") return done(r.token);
      if (!CODE_CHALLENGE.test(seen.challenge)) throw bad(`WHOOP asks for ${seen.challenge ?? "an unknown step"}, which Vitamux cannot answer yet`);
      const how = HOW[seen.challenge] ?? "the verification code";
      return { step: { prompt: { message: `Enter ${how}.`, fields: [field("code", "Verification code", "code")] }, session: seal({ session: r.session, username, method: r.method, challenge: seen.challenge }) } };
    }
    const s = unseal(input.session);
    return done(await WhoopClient.verifyCode(s.session, str(values.code, "code"), s.username, s.method, answerAs(deps.fetch ?? globalThis.fetch, s.challenge)));
  } catch (err) {
    throw classify(err);
  }
}

// Cognito reuses the refresh token unless it returns a new one; either way the caller stores the result.
async function refresh(creds, deps) {
  try {
    const t = await WhoopClient.refreshAccessToken(creds.refresh_token, deps.fetch);
    return pack({ ...t, userId: t.userId ?? creds.user_id });
  } catch (err) {
    throw classify(err);
  }
}

export async function authRefresh(input, deps) {
  return { credentials: await refresh(unpack(input?.credentials), deps) };
}

// ---------------------------------------------------------------- client with raw capture

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
let lastCallAt = 0;

// Wrap fetch: pace data requests, and keep the body text of the last successful response, so the
// raw stays exactly what WHOOP sent (the client's zod parsing and array unwrapping are bypassed).
// `seen` is the latest request's endpoint and, if it succeeded, its text: what a schema_drift names.
function capture(fetchFn, throttleMs) {
  let last = null;
  const cap = { seen: null };
  cap.fetch = async (url, init) => {
    const wait = lastCallAt + throttleMs - Date.now();
    if (wait > 0) await sleep(wait);
    lastCallAt = Date.now();
    const u = new URL(url);
    cap.seen = { endpoint: u.pathname, text: null };
    const res = await fetchFn(url, init);
    last = null;
    if (res.ok) {
      const text = (await res.clone().text()).trim().replace(/[\r\n]+/g, " ");
      cap.seen.text = text;
      last = { endpoint: u.pathname, params: Object.fromEntries(u.searchParams), text };
    }
    return res;
  };
  cap.take = () => {
    const c = last;
    last = null;
    return c;
  };
  return cap;
}

// A JSON value's shape (keys and kinds, never values), hashed: the fingerprint of a drifted response.
const shape = (v) => (Array.isArray(v) ? (v.length ? [shape(v[0])] : []) : v && typeof v === "object" ? Object.fromEntries(Object.keys(v).sort().map((k) => [k, shape(v[k])])) : v === null ? "null" : typeof v);
function fingerprint(text, fallback) {
  let basis = fallback;
  try {
    basis = JSON.stringify(shape(JSON.parse(text)));
  } catch {}
  return createHash("sha256").update(basis).digest("hex");
}

const REPLAY_CREDS = { access_token: "replay", refresh_token: "replay", user_id: 4242, expires: Infinity };

// Run `work(client, take)` with valid tokens: refresh before use when expired, and once after a 401.
async function withClient(credentials, deps, work) {
  let creds = deps.replay ? { ...REPLAY_CREDS, access_token: credentials?.access_token || "replay", refresh_token: credentials?.refresh_token || "replay" } : unpack(credentials);
  let refreshed = null;
  const renew = async () => {
    refreshed = await refresh(creds, deps);
    creds = unpack(refreshed);
  };
  if (creds.expires - 60_000 < Date.now()) await renew();
  for (;;) {
    const cap = capture(deps.fetch, deps.throttleMs);
    const client = new WhoopClient({ accessToken: creds.access_token, refreshToken: creds.refresh_token, userId: creds.user_id }, cap.fetch);
    try {
      return { page: await work(client, cap.take), credentials: refreshed };
    } catch (err) {
      const e = classify(err);
      if (e.code === "reauth_required" && refreshed === null && err !== e) {
        await renew();
        continue;
      }
      if (e.code === "schema_drift") {
        e.endpoint ??= cap.seen?.endpoint ?? null;
        e.fingerprint ??= fingerprint(cap.seen?.text, e.message);
      }
      throw e;
    }
  }
}

// ---------------------------------------------------------------- time helpers

const iso = (ms) => new Date(ms).toISOString();

function parseTime(v, name) {
  const ms = Date.parse(v);
  if (!Number.isFinite(ms)) throw bad(`${name} must be RFC 3339`);
  return ms;
}

function localDate(ms, tz) {
  return new Intl.DateTimeFormat("en-CA", { timeZone: tz }).format(ms);
}

function zoneOffsetMs(ms, tz) {
  const p = Object.fromEntries(
    new Intl.DateTimeFormat("en-US", { timeZone: tz, hourCycle: "h23", year: "numeric", month: "numeric", day: "numeric", hour: "numeric", minute: "numeric", second: "numeric" })
      .formatToParts(ms)
      .map((x) => [x.type, Number(x.value)]),
  );
  return Date.UTC(p.year, p.month - 1, p.day, p.hour, p.minute, p.second) - Math.floor(ms / 1000) * 1000;
}

// Instant of 00:00 on a local date.
function localMidnight(date, tz) {
  const guess = Date.parse(`${date}T00:00:00Z`);
  const t = guess - zoneOffsetMs(guess, tz);
  return guess - zoneOffsetMs(t, tz);
}

const nextDate = (date) => iso(Date.parse(`${date}T00:00:00Z`) + DAY).slice(0, 10);

// ---------------------------------------------------------------- streams

const item = (key, unit, c) => ({ external_key: key, unit, request: { endpoint: c.endpoint, params: c.params }, response: c.text });
// A grid window's raw: its unit is the whole window, so unchanged data hashes the same however the window was clipped.
const windowItem = (name, w, c) => item(`${name}:${iso(w.from)}`, { start: iso(w.from), end: iso(w.to) }, c);

// Window per grid stream: 7 days for cycles and sleep;
// heart rate's is VITAMUX_WHOOP_HR_WINDOW_H (deps.hrWindowH).
const GRID = { "whoop.cycles": 7 * DAY, "whoop.sleep": 7 * DAY };

// Fixed UTC grid so the same window is fetched, keyed and hashed identically on every run.
function gridWindow(cursor, start, end, grid) {
  const from = cursor?.next ? parseTime(cursor.next, "cursor") : Math.floor(start / grid) * grid;
  if (from >= end) return null;
  const to = from + grid;
  return { from, to, reqEnd: Math.min(to, end), last: to >= end };
}

const finish = (w, items) => ({ items, next_cursor: w.last ? null : { next: iso(w.to) }, done: w.last, high_watermark: w.last ? iso(w.reqEnd) : null });
const EMPTY = { items: [], next_cursor: null, done: true, high_watermark: null };

// The verbatim text of each object in the top-level `key` array of an object's JSON text (already
// parsed once, so valid): slicing keeps a record's bytes, large integers and key order.
function sliceRecords(text, key) {
  let depth = 0, str = "", member = null, inArray = false, from = 0, out = null;
  for (let i = 0; i < text.length; i++) {
    const ch = text[i];
    if (ch === '"') {
      const s = i;
      for (i++; text[i] !== '"'; i++) if (text[i] === "\\") i++;
      str = text.slice(s, i + 1);
    } else if (ch === ":" && depth === 1) {
      member = JSON.parse(str);
    } else if (ch === "{" || ch === "[") {
      if (depth === 1 && ch === "[" && member === key && !out) {
        inArray = true;
        out = [];
      } else if (inArray && depth === 2) from = i;
      depth++;
    } else if (ch === "}" || ch === "]") {
      depth--;
      if (inArray && depth === 2) out.push(text.slice(from, i + 1));
      else if (inArray && depth === 1) inArray = false;
    }
  }
  if (!out) throw drift(`no ${key} array`);
  return out;
}

// The number of samples in a metrics body, after checking its shape.
function sampleCount(c) {
  if (!c) throw drift("no response");
  const values = JSON.parse(c.text)?.values;
  if (!Array.isArray(values)) throw drift("no values array");
  const v = values[0];
  if (v && !(typeof v.time === "number" && typeof v.data === "number")) throw drift("sample shape");
  return values.length;
}

function metric(name, call) {
  return async ({ client, take, start, end, cursor, grid, deps }) => {
    const w = gridWindow(cursor, start, end, grid);
    if (!w) return EMPTY;
    await call(client, iso(w.from), iso(w.reqEnd), deps);
    const c = take();
    return finish(w, sampleCount(c) ? [windowItem(name, w, c)] : []);
  };
}

async function cyclesIn(client, take, w) {
  const cycles = await client.getCycles(iso(w.from), iso(w.reqEnd), CYCLE_LIMIT);
  const c = take();
  if (!c || !Array.isArray(cycles)) throw drift("cycles");
  if (cycles.length >= CYCLE_LIMIT) throw drift("cycle limit reached, window too large");
  return { cycles, c };
}

// Every sleep of the window's cycles, naps included, once each: sleeps[] lists them all by
// activity_id (the main sleep too, so recovery.activity_id adds nothing). The unit carries the
// nap flag and offset the stage events lack; the Go sleep normalizer writes the session from them.
function sleepUnits(cycles) {
  const units = new Map();
  for (const cy of cycles) {
    for (const s of cy?.sleeps ?? []) {
      if (typeof s?.activity_id !== "string" || s.activity_id === "" || typeof s.is_nap !== "boolean") throw drift("sleep without activity_id or is_nap");
      units.set(s.activity_id, { id: s.activity_id, is_nap: s.is_nap, timezone_offset: typeof s.timezone_offset === "string" ? s.timezone_offset : undefined });
    }
  }
  return [...units.values()];
}

const STREAM_PAGES = {
  "whoop.heart_rate": metric("whoop.heart_rate", (cl, s, e, deps) => cl.getHeartRate(s, e, deps.hrStep)),

  async "whoop.cycles"({ client, take, start, end, cursor, grid }) {
    const w = gridWindow(cursor, start, end, grid);
    if (!w) return EMPTY;
    const { cycles, c } = await cyclesIn(client, take, w);
    return finish(w, cycles.length ? [windowItem("whoop.cycles", w, c)] : []);
  },

  async "whoop.sleep"({ client, take, start, end, cursor, grid }) {
    const w = gridWindow(cursor, start, end, grid);
    if (!w) return EMPTY;
    const items = [];
    for (const unit of sleepUnits((await cyclesIn(client, take, w)).cycles)) {
      let events;
      try {
        events = await client.getSleep(unit.id);
      } catch (err) {
        if (apiStatus(err) === 404) continue; // gone upstream: nothing to store
        throw err;
      }
      const c = take();
      if (!c || !Array.isArray(events)) throw drift("sleep events");
      items.push(item(`whoop.sleep:${unit.id}`, unit, c));
    }
    return finish(w, items);
  },

  // The developer list has no time filter: newest first, page by page until a record older than `start`.
  // One raw per in-window record, keyed by its id, so a new workout never reshuffles stored raws.
  async "whoop.workouts"({ client, take, start, end, cursor }) {
    const token = cursor?.next_token ?? undefined;
    let page;
    try {
      page = await client.listDeveloperWorkouts({ limit: WORKOUT_PAGE, nextToken: token });
    } catch (err) {
      // @dofek/whoop 0.1.65 refuses null in optional fields (older workouts have sport_id: null),
      // so read the page from WHOOP's captured body instead; the checks below still apply.
      if (err?.name !== "ZodError") throw err;
    }
    const c = take();
    if (!c) throw drift("workouts");
    if (!page) {
      const b = JSON.parse(c.text);
      if (!Array.isArray(b?.records)) throw drift("workouts");
      page = { records: b.records, next_token: typeof b.next_token === "string" ? b.next_token : null };
    }
    const texts = sliceRecords(c.text, "records");
    if (texts.length !== page.records.length) throw drift("workout records");
    const starts = page.records.map((r) => Date.parse(r.start)).filter(Number.isFinite);
    const items = [];
    for (const [k, r] of page.records.entries()) {
      if (!(Date.parse(r.start) >= start && Date.parse(r.start) < end)) continue;
      if (typeof r.id !== "string" || r.id === "") throw drift("workout without id");
      items.push(item(`whoop.workouts:${r.id}`, { id: r.id }, { ...c, text: texts[k] }));
      const lifting = await client.getWeightliftingWorkout(r.id);
      if (lifting === null) continue;
      const lc = take();
      if (!lc || typeof lifting !== "object") throw drift("weightlifting");
      items.push(item(`whoop.workouts:${r.id}:weightlifting`, { id: r.id }, lc));
    }
    const next = page.next_token ?? null;
    if (next !== null && next === token) throw drift("repeated pagination token");
    // An empty page ends the walk too: a token without records must not page on forever.
    const finished = next === null || starts.length === 0 || Math.min(...starts) < start;
    return { items, next_cursor: finished ? null : { next_token: next }, done: finished, high_watermark: finished ? iso(end) : null };
  },

  // One call per local day (the day is computed in the owner's timezone).
  async "whoop.strain_deep_dive"({ client, take, start, end, cursor, tz }) {
    const date = cursor?.next ?? localDate(start, tz);
    const from = localMidnight(date, tz);
    if (from >= end) return EMPTY;
    const upto = nextDate(date);
    const to = localMidnight(upto, tz);
    const body = await client.getStrainDeepDive(date);
    const c = take();
    if (!c || body === null || typeof body !== "object") throw drift("strain deep dive");
    const last = to >= end;
    return { items: [item(`whoop.strain_deep_dive:${iso(from)}`, { start: iso(from), end: iso(to) }, c)], next_cursor: last ? null : { next: upto }, done: last, high_watermark: last ? iso(Math.min(to, end)) : null };
  },

};

// The window of a fetch. Correction and backfill send from/to; an incremental or manual run sends
// only the stored cursor {since}, and runs from there (or one lookback ago) to now.
function window(req, deps) {
  const cursor = req.cursor ?? {};
  if (typeof cursor !== "object" || Array.isArray(cursor)) throw bad("cursor must be an object");
  if (req.from) return { start: parseTime(req.from, "from"), end: parseTime(req.to, "to"), cursor, incremental: false };
  const end = (deps.now ?? Date.now)();
  return { start: cursor.since ? parseTime(cursor.since, "cursor") : end - LOOKBACK_MS[req.stream], end, cursor, incremental: true };
}

// One page: the raws it produced, the next cursor, and refreshed credentials when the token was renewed.
// A finished incremental run stores {since: end}; a finished window run leaves the stream cursor alone.
export async function fetchPage(req, deps) {
  const page = STREAM_PAGES[req?.stream];
  if (!page) throw bad("unknown stream");
  const { start, end, cursor, incremental } = window(req, deps);
  const tz = req.config?.timezone || "UTC";
  try {
    localDate(0, tz);
  } catch {
    throw bad("unknown timezone");
  }
  const grid = req.stream === "whoop.heart_rate" ? deps.hrWindowH * HOUR : GRID[req.stream];
  const { page: p, credentials } = await withClient(req.credentials, deps, (client, take) => page({ client, take, start, end, cursor, tz, grid, deps }));
  const next_cursor = p.done ? (incremental ? { since: iso(end) } : undefined) : incremental ? { since: iso(start), ...p.next_cursor } : p.next_cursor;
  return { ...p, next_cursor, credentials: credentials ?? undefined };
}
