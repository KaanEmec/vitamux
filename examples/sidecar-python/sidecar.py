"""Example Vitamux sidecar: protocol vitamux-connector/1 for a fictional heart-rate source.

Standard library only; all data is synthetic. Run it with SIDECAR_SECRET_FILE pointing at the
shared secret (README.md lists the synthetic sign-in values, the scenarios and BREAK variants).
"""

import base64
import hashlib
import hmac
import json
import os
import re
import sys
from datetime import datetime, timedelta, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

PROTOCOL = "vitamux-connector/1"
PROVIDER = "example_sidecar"
STREAM = "example_sidecar.heart_rate"
MAX_BODY = 1 << 20

# Synthetic sign-in. The username picks a scenario for everything that follows: it is carried in
# the issued tokens, so the sidecar keeps no state.
PASSWORD = "synthetic-pass"
CODE = "123456"
SCENARIOS = {
    "synthetic-user": "ok",  # normal data
    "synthetic-limited": "limited",  # fetch answers rate_limited with Retry-After
    "synthetic-flaky": "flaky",  # fetch answers transient
    "synthetic-drift": "drift",  # fetch answers schema_drift
    "synthetic-expired": "expired",  # fetch and refresh answer reauth_required
    "synthetic-rotate": "rotate",  # every fetch hands back rotated credentials
}
RETRY_AFTER_S = 7
TOKEN_RE = re.compile(r"^synthetic-(access|refresh)-(" + "|".join(SCENARIOS.values()) + r")-(\d+)$")

# BREAK=<check> makes the sidecar fail that one conformance check and nothing else.
BREAKS = {
    "bad_describe": "describe carries an auth_kind outside the allowed set",
    "bad_auth_step": "auth/begin returns a step with both redirect_url and prompt",
    "no_auth_check": "any bearer secret is accepted",
    "no_protocol_header": "responses lack the Vitamux-Protocol header",
    "no_result_line": "fetch ends without the result line",
    "malformed_line": "fetch emits a line that is not JSON",
    "bad_cursor": "next_cursor of a page that is not done repeats the request cursor",
    "unstable_replay": "the same cursor returns different raw bodies each time",
    "no_rotation": "refresh returns the credentials it was given",
    "wrong_error_code": "errors carry the wrong typed code",
    "no_retry_after": "rate_limited has neither the Retry-After header nor retry_after_s",
    "bad_drift": "schema_drift omits endpoint and fingerprint",
    "leaks_secret": "error details echo the bearer secret",
}

# 25 samples five minutes apart, served 10 per page.
T0 = datetime(2026, 9, 14, 7, 0, tzinfo=timezone.utc)
SAMPLES = 25
PAGE_SIZE = 10
DEVICE = "synthetic-band-01"
SAMPLES_ENDPOINT = "GET /heart-rate"


def iso(t: datetime) -> str:
    return t.astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def sample_time(i: int) -> datetime:
    return T0 + timedelta(minutes=5 * i)


def sample(i: int) -> dict:
    # Same record shape as internal/connectors/example (normalized by the Go normalizer).
    return {"synthetic": True, "id": f"hr-{i}", "time": iso(sample_time(i)), "bpm": 55 + (i * 7) % 30, "device": DEVICE}


class Problem(Exception):
    """An RFC 9457 problem+json answer with a protocol `code`."""

    def __init__(self, status: int, code: str, title: str, **extra):
        super().__init__(title)
        self.status, self.code, self.title, self.extra = status, code, title, extra


def credentials(scenario: str, generation: int) -> dict:
    return {
        "access_token": f"synthetic-access-{scenario}-{generation}",
        "refresh_token": f"synthetic-refresh-{scenario}-{generation}",
        "expires_at": iso(datetime.now(timezone.utc) + timedelta(hours=1)),
    }


def parse_token(creds, kind: str) -> tuple[str, int]:
    m = TOKEN_RE.match(str((creds or {}).get(f"{kind}_token", "")))
    if not m or m[1] != kind:
        raise Problem(401, "reauth_required", f"{kind} token rejected")
    return m[2], int(m[3])


def describe() -> dict:
    return {
        "protocol": PROTOCOL,
        "provider": PROVIDER,
        "name": "Example sidecar",
        "version": "1",
        "official": False,
        "auth_kind": "interactive_mfa",
        "streams": [{"name": STREAM, "interval_s": 3600, "lookback_s": 0, "correction_every_s": 0, "max_backfill_s": 0, "unit_size_s": 0}],
        "rate_limits": [{"requests": 60, "per_s": 60}],
        "capabilities": {"incremental": True, "backfill": False, "webhooks": False, "manual_sync": True},
        "upstream": {
            "package": "vitamux-example-sidecar",
            "version": "1",
            "source_url": "https://github.com/KaanEmec/vitamux/tree/main/examples/sidecar-python",
        },
    }


def parse_cursor(cursor) -> tuple[datetime | None, int]:
    """A stream cursor is {"since"}; a page cursor adds {"offset"}. Both are opaque to the core."""
    if cursor is None:
        return None, 0
    try:
        since = datetime.strptime(cursor["since"], "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=timezone.utc) if "since" in cursor else None
        offset = cursor.get("offset", 0)
        if isinstance(offset, int) and not isinstance(offset, bool) and offset >= 0:
            return since, offset
    except (TypeError, ValueError, KeyError, AttributeError):
        pass
    raise Problem(400, "permanent", "unreadable cursor")


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    server_version = "vitamux-example-sidecar"

    def log_request(self, code="-", size="-"):
        # Method, path and status only: never headers or bodies, which carry secrets.
        sys.stderr.write(f"{self.command} {self.path.split('?')[0]} {code}\n")

    def log_error(self, fmt, *args):
        sys.stderr.write("error: " + (fmt % args) + "\n")

    @property
    def brk(self) -> str:
        return self.server.brk

    def do_GET(self):
        self.handle_request()

    def do_POST(self):
        self.handle_request()

    def handle_request(self):
        try:
            if self.path == "/healthz":  # unauthenticated, for the container healthcheck
                return self.send_body(200, b"ok\n", "text/plain")
            self.authorize()
            routes = {
                ("GET", "/v1/describe"): lambda: self.send_json(200, self.describe_response()),
                ("POST", "/v1/auth/begin"): self.auth_begin,
                ("POST", "/v1/auth/continue"): self.auth_continue,
                ("POST", "/v1/auth/refresh"): self.auth_refresh,
                ("POST", "/v1/fetch"): self.fetch,
            }
            route = routes.get((self.command, self.path))
            if route is None:
                raise Problem(404, "permanent", "unknown endpoint")
            route()
        except Problem as p:
            self.send_problem(p)
        except Exception:  # never echo the cause: it may hold request data
            sys.stderr.write("internal error\n")
            self.send_problem(Problem(500, "transient", "internal error"))

    def authorize(self):
        if self.brk == "no_auth_check":
            return
        got = self.headers.get("Authorization", "").removeprefix("Bearer ").encode()
        if not hmac.compare_digest(got, self.server.secret):
            raise Problem(401, "permanent", "bad sidecar secret")

    def read_json(self) -> dict:
        try:
            length = int(self.headers.get("Content-Length", ""))
        except ValueError:
            length = -1
        if not 0 <= length <= MAX_BODY:
            raise Problem(400, "permanent", "body missing or over 1 MiB")
        try:
            body = json.loads(self.rfile.read(length))
        except ValueError:
            body = None
        if not isinstance(body, dict):
            raise Problem(400, "permanent", "body is not a JSON object")
        return body

    # Responses.

    def send_body(self, status: int, body: bytes, content_type: str, headers: dict | None = None):
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(body)))
        if self.brk != "no_protocol_header":
            self.send_header("Vitamux-Protocol", PROTOCOL)
        for k, v in (headers or {}).items():
            self.send_header(k, v)
        self.end_headers()
        self.wfile.write(body)

    def send_json(self, status: int, obj: dict):
        self.send_body(status, json.dumps(obj).encode(), "application/json")

    def send_problem(self, p: Problem):
        code, extra, headers = p.code, dict(p.extra), {}
        if self.brk == "wrong_error_code":
            code = "permanent" if code == "transient" else "transient"
        if p.code == "rate_limited":
            if self.brk == "no_retry_after":
                extra.pop("retry_after_s", None)
            else:
                headers["Retry-After"] = str(extra.get("retry_after_s", RETRY_AFTER_S))
        if p.code == "schema_drift" and self.brk == "bad_drift":
            extra.clear()
        detail = p.title + (f" (bearer {self.server.secret.decode()})" if self.brk == "leaks_secret" else "")
        body = {"type": "about:blank", "title": p.title, "status": p.status, "detail": detail, "code": code, **extra}
        self.send_body(p.status, json.dumps(body).encode(), "application/problem+json", headers)

    # Auth. Prompts carry a signed session so a sidecar restart loses nothing and the core never
    # needs to understand it. A real sidecar may keep upstream login state in it (<= 64 KiB).

    def describe_response(self) -> dict:
        d = describe()
        if self.brk == "bad_describe":
            d["auth_kind"] = "password"
        return d

    def seal(self, state: dict) -> str:
        raw = json.dumps(state).encode()
        return base64.b64encode(hmac.new(self.server.secret, raw, "sha256").digest() + raw).decode()

    def unseal(self, session) -> dict:
        try:
            blob = base64.b64decode(session, validate=True)
            mac, raw = blob[:32], blob[32:]
            if hmac.compare_digest(mac, hmac.new(self.server.secret, raw, "sha256").digest()):
                return json.loads(raw)
        except (TypeError, ValueError):
            pass
        raise Problem(400, "permanent", "unreadable session")

    def prompt(self, message: str, fields: list[dict], state: dict) -> dict:
        return {"step": {"prompt": {"message": message, "fields": fields}, "session": self.seal(state)}}

    def auth_begin(self):
        self.read_json()
        step = self.prompt(
            "Sign in to the example source.",
            [{"name": "username", "label": "Username", "kind": "text"}, {"name": "password", "label": "Password", "kind": "password"}],
            {"stage": "password"},
        )
        if self.brk == "bad_auth_step":
            step["step"]["redirect_url"] = "https://example.invalid/login"
        self.send_json(200, step)

    def auth_continue(self):
        body = self.read_json()
        state = self.unseal(body.get("session"))
        values = body.get("values") or {}
        if state.get("stage") == "password":
            scenario = SCENARIOS.get(values.get("username"))
            if scenario is None or values.get("password") != PASSWORD:
                raise Problem(401, "reauth_required", "sign-in rejected")
            step = self.prompt("Enter the verification code.", [{"name": "code", "label": "Verification code", "kind": "code"}], {"stage": "code", "scenario": scenario})
            return self.send_json(200, step)
        if state.get("stage") == "code" and state.get("scenario") in SCENARIOS.values():
            if values.get("code") != CODE:
                raise Problem(401, "reauth_required", "verification code rejected")
            account = {"account_id": f"synthetic-account-{state['scenario']}", "credentials": credentials(state["scenario"], 1)}
            return self.send_json(200, {"authorized": account})
        raise Problem(400, "permanent", "unreadable session")

    def auth_refresh(self):
        creds = self.read_json().get("credentials")
        scenario, generation = parse_token(creds, "refresh")
        if scenario == "expired":
            raise Problem(401, "reauth_required", "refresh token expired")
        if self.brk == "no_rotation":
            return self.send_json(200, {"credentials": creds})
        self.send_json(200, {"credentials": credentials(scenario, generation + 1)})

    # Fetch: raw lines then one result line, as application/x-ndjson.

    def fetch(self):
        body = self.read_json()
        if body.get("stream") != STREAM:
            raise Problem(400, "permanent", "unknown stream")
        if body.get("mode") not in ("incremental", "manual"):
            raise Problem(400, "permanent", "mode not supported")
        scenario, generation = parse_token(body.get("credentials"), "access")
        since, offset = parse_cursor(body.get("cursor"))
        failures = {
            "expired": Problem(401, "reauth_required", "access token expired"),
            "limited": Problem(429, "rate_limited", "too many requests", retry_after_s=RETRY_AFTER_S),
            "flaky": Problem(503, "transient", "upstream unavailable"),
            "drift": Problem(502, "schema_drift", "upstream response changed shape", endpoint=SAMPLES_ENDPOINT, fingerprint=hashlib.sha256(b"synthetic-drift").hexdigest()),
        }
        if scenario in failures:
            raise failures[scenario]

        matching = [i for i in range(SAMPLES) if since is None or sample_time(i) > since]
        page = matching[offset : offset + PAGE_SIZE]
        done = offset + PAGE_SIZE >= len(matching)
        lines = []
        for i in page:
            s = sample(i)
            if self.brk == "unstable_replay":
                s["bpm"] = 40 + datetime.now().microsecond % 100
            lines.append({"type": "raw", "external_key": f"sample:{s['id']}", "content_type": "application/json", "body": s,
                          "request": {"method": "GET", "url": "https://example.invalid/heart-rate"}})
        result: dict = {"type": "result", "done": done}
        if done and matching:
            result["next_cursor"] = {"since": iso(sample_time(matching[-1]))}
        elif done:
            result["next_cursor"] = body.get("cursor")  # nothing new: stay where we were
        else:
            result["next_cursor"] = body.get("cursor") if self.brk == "bad_cursor" else {**({"since": iso(since)} if since else {}), "offset": offset + PAGE_SIZE}
        if page:
            result["high_watermark"] = iso(sample_time(page[-1]))
        if scenario == "rotate":
            result["credentials"] = credentials(scenario, generation + 1)
        out = [json.dumps(line) for line in lines]
        if self.brk == "malformed_line":
            out.append("{not json")
        if self.brk != "no_result_line":
            out.append(json.dumps(result))
        self.send_body(200, ("\n".join(out) + "\n").encode(), "application/x-ndjson")


def make_server(addr: tuple[str, int], secret: bytes, brk: str = "") -> ThreadingHTTPServer:
    if brk and brk not in BREAKS:
        raise ValueError(f"unknown BREAK {brk!r}; known: {', '.join(BREAKS)}")
    server = ThreadingHTTPServer(addr, Handler)
    server.secret, server.brk = secret, brk
    return server


def main() -> int:
    path = os.environ.get("SIDECAR_SECRET_FILE")
    try:
        if not path:
            raise ValueError("SIDECAR_SECRET_FILE is not set")
        with open(path, "rb") as f:
            secret = f.read().strip()
        if not secret:
            raise ValueError("secret file is empty")
        host, _, port = os.environ.get("SIDECAR_ADDR", "127.0.0.1:8090").rpartition(":")
        server = make_server((host, int(port)), secret, os.environ.get("BREAK", ""))
    except (OSError, ValueError) as e:
        sys.stderr.write(f"sidecar: {e}\n")
        return 1
    if server.brk:
        sys.stderr.write(f"sidecar: BREAK={server.brk}: deliberately non-conformant\n")
    sys.stderr.write(f"sidecar: serving {PROVIDER} on {server.server_address[0]}:{server.server_address[1]}\n")
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    return 0


if __name__ == "__main__":
    sys.exit(main())
