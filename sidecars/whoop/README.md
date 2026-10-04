# Vitamux WHOOP sidecar

Stateless Node service that speaks `vitamux-connector/1` ([ADR-0017](../../docs/adr/0017-sidecar-protocol.md), [wrapping a collector](../../docs/sidecars.md)) and wraps the unofficial [`@dofek/whoop`](UPSTREAM.md) client. It stores nothing: credentials live, sealed, in the Vitamux core, which passes them with every call. Provider facts: [providers/whoop.md](../../docs/providers/whoop.md).

- `src/server.js`: HTTP and wire format (endpoints, bearer check, NDJSON, problem+json).
- `src/whoop.js`: WHOOP sign-in, refresh, streams, error mapping.
- `src/main.js`: reads the environment and starts the server.
- `src/replay.js`, `testdata/replay.json`: the `REPLAY=1` cassette (synthetic).
- `tests/`: `node --test`, WHOOP is a fake fetch. `conformance.json`: the [scenario](../../schemas/connector-test-scenario.v1.json) for `vitamux connector-test`.

## Configuration

| Variable | Default | |
| --- | --- | --- |
| `VITAMUX_SIDECAR_SECRET_FILE` | required | File holding the shared bearer secret (every endpoint except `/healthz`). |
| `SIDECAR_ADDR` | `0.0.0.0:8080` | Listen address. Keep it on the private network. |
| `REPLAY` | unset | `1`: answer from `testdata/replay.json` (run from this directory, or mount it at `/app/testdata`) and never call WHOOP. |
| `VITAMUX_WHOOP_HR_STEP_S` | `6` | Heart-rate sampling step in seconds. |
| `VITAMUX_WHOOP_HR_WINDOW_H` | `24` | Heart-rate window per request, in hours. Lower it if WHOOP truncates a day. |

Requests to WHOOP are spaced by `WHOOP_API_THROTTLE_MS` (1 s). Logs hold method, path, status and error class only, never bodies, credentials or tokens.

## Develop

```sh
npm ci
npm test          # node --test, no network: WHOOP is a fake fetch
head -c 32 /dev/urandom | xxd -p -c 64 > /tmp/whoop.secret
REPLAY=1 VITAMUX_SIDECAR_SECRET_FILE=/tmp/whoop.secret SIDECAR_ADDR=127.0.0.1:8080 node src/main.js &
go run ../../cmd/vitamux connector-test --url http://127.0.0.1:8080 --secret-file /tmp/whoop.secret --scenario conformance.json
```

`scripts/sidecar-check.sh whoop` does the same in Docker (non-root, read-only root filesystem works, 256 MiB is plenty). The replay run cannot check `auth_refresh` (it needs a real sign-in); everything else passes.
