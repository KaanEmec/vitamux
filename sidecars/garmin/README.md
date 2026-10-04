# Garmin sidecar

Wraps [`garminconnect`](UPSTREAM.md) (unofficial) and speaks `vitamux-connector/1` ([ADR-0017](../../docs/adr/0017-sidecar-protocol.md), [wrapping a collector](../../docs/sidecars.md)). Stateless: tokens live in the core, the one exception is a pending MFA sign-in, kept in memory for 10 minutes. Provider facts, streams and the raw envelope: [docs/providers/garmin.md](../../docs/providers/garmin.md).

- `src/server.py`: all HTTP and protocol code (auth, NDJSON, problem+json).
- `src/garmin.py`: all Garmin logic (sign-in, refresh, streams, error mapping).
- `src/replay.py`, `testdata/replay.json`: the `REPLAY=1` cassette (synthetic).
- `tests/`: stdlib tests with a faked Garmin client, no network.
- `conformance.json`: the [scenario](../../schemas/connector-test-scenario.v1.json) for `vitamux connector-test`.

| Variable | Default | |
| --- | --- | --- |
| `VITAMUX_SIDECAR_SECRET_FILE` | required | File with the bearer secret every request but `/healthz` must send |
| `SIDECAR_ADDR` | `0.0.0.0:8080` | Listen address |
| `VITAMUX_GARMIN_CALL_DELAY_S` | `1` | Pause after each Garmin call |
| `VITAMUX_GARMIN_RELOAD_WAIT_S` | `20` | Pause between checks that a reloaded day is back (cold-storage reload, up to six checks) |
| `REPLAY` | unset | `1`: answer from `testdata/replay.json` (run from this directory, or mount it at `/app/testdata`) and never call Garmin |

```sh
uv sync && uv run python -m unittest -q        # tests
head -c 32 /dev/urandom | xxd -p -c 64 > /tmp/garmin.secret
REPLAY=1 VITAMUX_SIDECAR_SECRET_FILE=/tmp/garmin.secret SIDECAR_ADDR=127.0.0.1:8080 uv run python -m src.main &
go run ../../cmd/vitamux connector-test --url http://127.0.0.1:8080 --secret-file /tmp/garmin.secret --scenario conformance.json
```

`scripts/sidecar-check.sh garmin` does the same in Docker. The replay run cannot check `auth_refresh` (it needs a real sign-in); everything else passes.

It never logs request bodies, and `garminconnect` logging is silenced. Keep passwords, codes and tokens out of issues, logs and fixtures.
