// vitamux-connector/1 over HTTP (api/connector-sidecar.v1.yaml). All protocol code lives here;
// WHOOP specifics are in whoop.js.
import http from "node:http";
import { createHash, timingSafeEqual } from "node:crypto";
import * as whoop from "./whoop.js";

const MAX_BODY = 1 << 20;
const sha = (data) => createHash("sha256").update(data).digest();

const PROTOCOL = { "vitamux-protocol": "vitamux-connector/1" };

function problem(res, err) {
  const body = { type: `urn:vitamux:connector:${err.code}`, title: err.title ?? err.message, status: err.status, code: err.code };
  const headers = { ...PROTOCOL, "content-type": "application/problem+json" };
  if (err.retryAfterS != null) {
    body.retry_after_s = err.retryAfterS;
    headers["retry-after"] = String(err.retryAfterS);
  }
  if (err.code === "schema_drift") {
    body.endpoint = err.endpoint ?? "unknown";
    body.fingerprint = err.fingerprint ?? sha(body.title).toString("hex");
  }
  res.writeHead(err.status, headers);
  res.end(JSON.stringify(body));
}

async function readJSON(req) {
  let size = 0;
  const chunks = [];
  for await (const chunk of req) {
    size += chunk.length;
    if (size > MAX_BODY) throw new whoop.SidecarError("permanent", "request too large", { status: 413 });
    chunks.push(chunk);
  }
  try {
    const v = JSON.parse(Buffer.concat(chunks).toString() || "{}");
    if (v !== null && typeof v === "object" && !Array.isArray(v)) return v;
  } catch {}
  throw new whoop.SidecarError("permanent", "body must be a JSON object", { status: 400 });
}

// The raw line. `response` is WHOOP's JSON text, embedded unparsed so numbers and key order survive.
function rawLine(it) {
  const head = JSON.stringify({ type: "raw", external_key: it.external_key, content_type: "application/json", request: it.request });
  return `${head.slice(0, -1)},"body":{"unit":${JSON.stringify(it.unit)},"response":${it.response}}}\n`;
}

const resultLine = (p) => JSON.stringify({ type: "result", next_cursor: p.next_cursor, done: p.done, high_watermark: p.high_watermark ?? undefined, credentials: p.credentials }) + "\n";

// deps: { secret, fetch, throttleMs, hrStep, hrWindowH, info: { version, upstreamVersion } }
export function createServer(deps) {
  const secretDigest = sha(deps.secret);
  const routes = {
    "GET /v1/describe": async () => whoop.describe({ ...deps.info, throttleMs: deps.throttleMs }),
    "POST /v1/auth/begin": async (req) => {
      await readJSON(req);
      return whoop.authBegin();
    },
    "POST /v1/auth/continue": async (req) => whoop.authContinue(await readJSON(req), deps),
    "POST /v1/auth/refresh": async (req) => whoop.authRefresh(await readJSON(req), deps),
  };

  return http.createServer(async (req, res) => {
    const path = new URL(req.url, "http://x").pathname;
    let outcome = "ok";
    res.on("finish", () => console.log(JSON.stringify({ level: "info", msg: "request", method: req.method, path, status: res.statusCode, outcome })));
    try {
      if (path === "/healthz") {
        res.writeHead(200, { ...PROTOCOL, "content-type": "text/plain" });
        return res.end("ok\n");
      }
      const bearer = /^Bearer (.+)$/.exec(req.headers.authorization ?? "")?.[1] ?? "";
      if (!timingSafeEqual(sha(bearer), secretDigest)) throw new whoop.SidecarError("permanent", "unauthorized", { status: 401 });

      const key = `${req.method} ${path}`;
      if (key === "POST /v1/fetch") {
        const p = await whoop.fetchPage(await readJSON(req), deps);
        res.writeHead(200, { ...PROTOCOL, "content-type": "application/x-ndjson" });
        return res.end(p.items.map(rawLine).join("") + resultLine(p));
      }
      const handler = routes[key];
      if (!handler) throw new whoop.SidecarError("permanent", "not found", { status: 404 });
      const out = await handler(req);
      res.writeHead(200, { ...PROTOCOL, "content-type": "application/json" });
      res.end(JSON.stringify(out));
    } catch (err) {
      const e = whoop.classify(err);
      outcome = `${e.code}:${err?.name ?? "Error"}`; // names only, never messages
      problem(res, e);
    }
  });
}
