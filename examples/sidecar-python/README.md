# Example sidecar (Python)

A small, complete `vitamux-connector/1` sidecar for a fictional heart-rate source: provider `example_sidecar`, stream `example_sidecar.heart_rate`, MFA sign-in (username and password, then a code). It is one file, [`sidecar.py`](sidecar.py), standard library only, and serves **synthetic data only**. Copy it to start a sidecar of your own. The protocol is in [connectors.md#remote-sidecar-mode](../../docs/architecture/connectors.md#remote-sidecar-mode) and ADR 0017; the Go side of this stream is `example.Normalizer{Stream: example.SidecarStream}` in `internal/connectors/example`.

## Run

```sh
head -c 32 /dev/urandom | xxd -p -c 64 > /tmp/example-sidecar.secret   # the shared secret; any text works
VITAMUX_SIDECAR_SECRET_FILE=/tmp/example-sidecar.secret SIDECAR_ADDR=127.0.0.1:8080 python3 sidecar.py   # Python 3.12
curl -H "Authorization: Bearer $(cat /tmp/example-sidecar.secret)" localhost:8080/v1/describe
go run ../../cmd/vitamux connector-test --url http://127.0.0.1:8080 --secret-file /tmp/example-sidecar.secret --scenario conformance.json
```

| Variable | Meaning |
| --- | --- |
| `VITAMUX_SIDECAR_SECRET_FILE` | Required. File holding the bearer secret the core sends (the sidecar template's name). Never pass the secret as a value. |
| `SIDECAR_ADDR` | `host:port`, default `0.0.0.0:8080` (the template's convention; bind `127.0.0.1` when running by hand). |
| `REPLAY` | Accepted, changes nothing: the example has no upstream, so it always answers from built-in synthetic data, which is what `REPLAY=1` does for a real sidecar. |
| `BREAK` | Optional, see below. |

With Docker: `docker build -t vitamux-sidecar-example . && docker run --rm --read-only -p 127.0.0.1:8080:8080 -v /tmp/example-sidecar.secret:/secrets/s:ro -e VITAMUX_SIDECAR_SECRET_FILE=/secrets/s vitamux-sidecar-example` (the secret file must be readable by uid 65532). `GET /healthz` is the only unauthenticated endpoint.

With Vitamux, use the Compose profile in [`deploy/compose`](../../deploy/compose/compose.yaml). Create the secret first (`admin init-secrets` writes a missing `sidecar-example_sidecar.secret`), then:

```sh
export VITAMUX_SIDECARS=example_sidecar=http://example-sidecar:8080
docker compose --profile setup run --rm init-secrets
docker compose --profile sidecar-example up -d
```

The service has a 256 MiB limit (it uses about 15 MiB), no published port and no route out. In the UI the provider appears as unofficial: connecting it starts paused until you enable it.

## Synthetic sign-in

| Step | Value |
| --- | --- |
| username | `synthetic-user` |
| password | `synthetic-pass` |
| code | `123456` |

Anything else is rejected with `401 reauth_required`. The username also picks a scenario for everything after sign-in (the issued tokens carry it, so the sidecar keeps no state; tokens are `synthetic-access-<scenario>-<n>`):

| Username | What `fetch` does |
| --- | --- |
| `synthetic-user` | Serves 25 samples, 10 per page, five minutes apart from 2026-09-14T07:00:00Z. |
| `synthetic-limited` | `429 rate_limited`, `Retry-After: 7` and `retry_after_s: 7`. |
| `synthetic-flaky` | `503 transient`. |
| `synthetic-drift` | `200` page of one quarantined record, then a `schema_drift` error line with `endpoint` and `fingerprint`. |
| `synthetic-expired` | `401 reauth_required`; refresh fails the same way. |
| `synthetic-rotate` | Success, and every result line carries rotated `credentials`. |

All errors are `application/problem+json` with a `code` of `reauth_required`, `rate_limited`, `transient`, `schema_drift` or `permanent`. Every response carries `Vitamux-Protocol: vitamux-connector/1`. The stream cursor is `{"since": <time of the newest sample>}`; a page cursor adds `{"offset": n}`. The same cursor always returns the same bytes. The auth `session` is a signed blob (the core seals it at rest and never shows it to a browser); a real sidecar would keep its upstream login state there.

## Conformance

[`conformance.json`](conformance.json) is the scenario for `vitamux connector-test` (schema: [`connector-test-scenario.v1.json`](../../schemas/connector-test-scenario.v1.json)): the synthetic sign-in values above, and which login provokes which typed error. The conformant example passes every check.

## BREAK variants

`BREAK=<check>` makes the sidecar misbehave in exactly one way, so a conformance check has something to fail. An unknown value refuses to start. `TestBrokenVariantsFailOneCheck` in `internal/connectors/remote` (and the `sidecar-kit` CI job) starts the example once per value and asserts that the kit fails exactly the check named in the last column.

| `BREAK` | Misbehaviour | Fails check |
| --- | --- | --- |
| `bad_describe` | `describe` has an `auth_kind` outside the allowed set | `describe` |
| `bad_auth_step` | `auth/begin` returns a step with both `redirect_url` and `prompt` | `auth_flow` |
| `no_auth_check` | any bearer secret is accepted | `bad_bearer` |
| `no_protocol_header` | responses lack `Vitamux-Protocol` | `protocol_header` |
| `no_result_line` | `fetch` ends without the result line | `fetch_lines` |
| `malformed_line` | `fetch` emits a line that is not JSON | `fetch_lines` |
| `bad_cursor` | `next_cursor` of an unfinished page repeats the request cursor | `fetch_paging` |
| `unstable_replay` | the same cursor returns different raw bodies | `fetch_replay` |
| `no_rotation` | `refresh` returns the credentials it was given | `rotated_credentials` |
| `wrong_error_code` | `fetch` errors carry the wrong typed `code` | `typed_errors` |
| `no_retry_after` | `rate_limited` has neither `Retry-After` nor `retry_after_s` | `retry_after` |
| `bad_drift` | `schema_drift` omits `endpoint` and `fingerprint` | `drift_report` |
| `leaks_secret` | error details echo the bearer secret | `no_secret_echo` |

## Tests

`python3 -m unittest` in this directory (Python 3.12; starts the server on a free port). It covers the conformant behaviour, every `BREAK` value, and that the sample shape matches the Go normalizer's golden input.
