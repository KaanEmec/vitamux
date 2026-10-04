import { createServer } from "../src/server.js";

export const SECRET = "sidecar-test-secret";
export const PASSWORD = "sentinel-password-9f3";
export const CODE = "sentinel-code-714";
export const ACCESS = "sentinel-access-token";
export const REFRESH = "sentinel-refresh-token";
export const USER_ID = 4242;

export const json = (body, status = 200, headers = {}) =>
  new Response(typeof body === "string" ? body : JSON.stringify(body), { status, headers: { "content-type": "application/json", ...headers } });

// A fake WHOOP: `routes` maps "METHOD pathname" (or the Cognito target) to (url, init) => Response.
// Cognito calls are routed by their X-Amz-Target action. Every call is recorded in `calls`.
export function fakeWhoop(routes) {
  const calls = [];
  const fetch = async (url, init = {}) => {
    const u = new URL(url);
    const action = init.headers?.["X-Amz-Target"]?.split(".").pop();
    const key = action ?? u.pathname;
    const body = init.body ? JSON.parse(init.body) : null;
    calls.push({ key, params: Object.fromEntries(u.searchParams), body, auth: init.headers?.Authorization });
    const handler = routes[key];
    if (!handler) throw new Error(`unrouted ${key}`);
    return handler({ params: Object.fromEntries(u.searchParams), body, url: u });
  };
  return { fetch, calls };
}

export const bootstrap = () => json({ user: { id: USER_ID } });
export const authResult = (extra = {}) => json({ AuthenticationResult: { AccessToken: ACCESS, RefreshToken: REFRESH, ExpiresIn: 3600, ...extra } });

export async function start(routes, overrides = {}) {
  const logs = [];
  const log = console.log;
  console.log = (...a) => logs.push(a.join(" "));
  const whoop = fakeWhoop(routes);
  const server = createServer({ secret: SECRET, fetch: whoop.fetch, throttleMs: 0, hrStep: 6, hrWindowH: 24, info: { version: "0.1.0", upstreamVersion: "0.1.65" }, ...overrides });
  await new Promise((r) => server.listen(0, "127.0.0.1", r));
  const base = `http://127.0.0.1:${server.address().port}`;
  const call = async (method, path, body, headers = { authorization: `Bearer ${SECRET}` }) => {
    const res = await fetch(base + path, { method, headers: { "content-type": "application/json", ...headers }, body: body === undefined ? undefined : JSON.stringify(body) });
    const text = await res.text();
    return { status: res.status, type: res.headers.get("content-type"), protocol: res.headers.get("vitamux-protocol"), retryAfter: res.headers.get("retry-after"), text, json: () => JSON.parse(text), lines: () => text.trim().split("\n").map((l) => JSON.parse(l)) };
  };
  const stop = () => {
    console.log = log;
    return new Promise((r) => server.close(r));
  };
  return { call, calls: whoop.calls, logs, stop };
}

// Credentials as the sidecar issues them; `expiresInMs` < 60 s forces a refresh before use.
export const creds = (expiresInMs = 3_600_000, over = {}) => ({ access_token: ACCESS, refresh_token: REFRESH, expires_at: new Date(Date.now() + expiresInMs).toISOString(), extra: { user_id: USER_ID }, ...over });
