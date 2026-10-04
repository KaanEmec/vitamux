// REPLAY=1: a fetch that answers from a synthetic cassette (testdata/replay.json) instead of WHOOP.
// A response matches on the request path, the token when the entry names one (the bearer token,
// or the refresh token of a Cognito call, so the scenario's synthetic tokens can provoke errors)
// and query values when given; the first match wins. An unrecorded request is a 404, which the sidecar reports as schema_drift.
export function replayFetch(cassette) {
  return async (url, init = {}) => {
    const u = new URL(url);
    const token = /^Bearer (.*)$/.exec(init.headers?.Authorization ?? "")?.[1] ?? JSON.parse(init.body ?? "null")?.AuthParameters?.REFRESH_TOKEN;
    const hit = cassette.responses.find((r) => r.path === u.pathname && (!r.token || r.token === token) && Object.entries(r.query ?? {}).every(([k, v]) => u.searchParams.get(k) === v));
    if (!hit) return new Response("not recorded", { status: 404 });
    return new Response(JSON.stringify(hit.body), { status: hit.status ?? 200, headers: { "content-type": "application/json", ...hit.headers } });
  };
}
