import { readFileSync } from "node:fs";
import { createServer } from "./server.js";
import { replayFetch } from "./replay.js";
import { WHOOP_API_THROTTLE_MS } from "@dofek/whoop";

const pkg = (url) => JSON.parse(readFileSync(url, "utf8"));
const env = process.env;

const secretFile = env.VITAMUX_SIDECAR_SECRET_FILE;
if (!secretFile) {
  console.error("VITAMUX_SIDECAR_SECRET_FILE is required");
  process.exit(1);
}
const secret = readFileSync(secretFile, "utf8").trim();
if (!secret) {
  console.error("sidecar secret file is empty");
  process.exit(1);
}

const positive = (name, fallback) => {
  const n = Number(env[name] ?? fallback);
  if (!Number.isInteger(n) || n < 1) {
    console.error(`${name} must be a positive integer`);
    process.exit(1);
  }
  return n;
};

// REPLAY=1: answer from the synthetic cassette in testdata/ instead of calling WHOOP (docs/sidecars.md).
const replay = env.REPLAY === "1" ? JSON.parse(readFileSync("testdata/replay.json", "utf8")) : null;

const [host, port] = (env.SIDECAR_ADDR || "0.0.0.0:8080").split(/:(?=[^:]*$)/);
const server = createServer({
  secret,
  fetch: replay ? replayFetch(replay) : globalThis.fetch,
  replay: !!replay,
  now: replay ? () => Date.parse(replay.now) : undefined,
  throttleMs: replay ? 0 : WHOOP_API_THROTTLE_MS,
  hrStep: positive("VITAMUX_WHOOP_HR_STEP_S", 6),
  hrWindowH: positive("VITAMUX_WHOOP_HR_WINDOW_H", 24),
  info: {
    version: pkg(new URL("../package.json", import.meta.url)).version,
    upstreamVersion: pkg(new URL("../package.json", import.meta.resolve("@dofek/whoop"))).version,
  },
});
server.listen(Number(port), host, () => console.log(JSON.stringify({ level: "info", msg: "listening", addr: `${host}:${port}` })));
for (const sig of ["SIGTERM", "SIGINT"]) process.on(sig, () => server.close(() => process.exit(0)));
