"""HTTP side of the sidecar: protocol vitamux-connector/1 (api/connector-sidecar.v1.yaml).

All protocol handling lives here; the Garmin logic is in garmin.py. Never log
request bodies, credentials, passwords or MFA codes.
"""

import base64
import hashlib
import hmac
import json
import logging
from itertools import chain
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from . import garmin

PROTOCOL = "vitamux-connector/1"
MAX_BODY = 1 << 20

STATUS = {"reauth_required": 401, "rate_limited": 429, "transient": 503,
          "schema_drift": 502, "permanent": 500}
TITLE = {"reauth_required": "Sign-in required", "rate_limited": "Rate limited",
         "transient": "Temporary failure", "schema_drift": "Unexpected response shape",
         "permanent": "Permanent failure"}


def dumps(obj):
    return json.dumps(obj, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode()


def raw_line(item):
    """NDJSON raw line: the JSON body verbatim, or a binary item as base64 with its sha256."""
    line = {"type": "raw", "external_key": item["external_key"], "request": item["request"]}
    if "data" in item:
        line.update(content_type=item["content_type"], sha256=hashlib.sha256(item["data"]).hexdigest(),
                    body_base64=base64.b64encode(item["data"]).decode())
    else:
        line.update(content_type="application/json", body=item["body"])
    return dumps(line) + b"\n"


def result_line(r):
    line = {"type": "result", "done": r.done, "next_cursor": r.next_cursor,
            "high_watermark": r.high_watermark, "credentials": r.credentials}
    return dumps({k: v for k, v in line.items() if v is not None}) + b"\n"


class Handler(BaseHTTPRequestHandler):
    server_version = "vitamux-garmin"
    secret = b""

    def log_request(self, code="-", size="-"):
        logging.info("%s %s %s", self.command, self.path, code)

    def reply(self, status, body, ctype="application/json", headers=()):
        self.send_response(status)
        self.send_header("Content-Type", ctype)
        self.send_header("Vitamux-Protocol", PROTOCOL)
        self.send_header("Content-Length", str(len(body)))
        for k, v in headers:
            self.send_header(k, v)
        self.end_headers()
        self.wfile.write(body)

    def problem(self, code, detail="", status=None, retry_after_s=None, endpoint=None, fingerprint=None):
        status = status or STATUS[code]
        p = {"type": f"urn:vitamux:connector:{code}", "title": TITLE[code], "status": status, "code": code}
        if detail:
            p["detail"] = detail
        if retry_after_s is not None:
            p["retry_after_s"] = retry_after_s
        if code == "schema_drift":
            p["endpoint"] = endpoint or "unknown"
            p["fingerprint"] = fingerprint or hashlib.sha256(detail.encode()).hexdigest()
        extra = [("Retry-After", str(retry_after_s))] if retry_after_s is not None else []
        self.reply(status, dumps(p), "application/problem+json", extra)

    def authorized(self):
        got = self.headers.get("Authorization", "").encode()
        return hmac.compare_digest(got, b"Bearer " + self.secret)

    def json_body(self):
        try:
            n = int(self.headers.get("Content-Length", ""))
            body = json.loads(self.rfile.read(n)) if 0 < n <= MAX_BODY else None
        except ValueError:
            body = None
        return body if isinstance(body, dict) else None

    def do_GET(self):
        if self.path == "/healthz":
            return self.reply(200, b'{"status":"ok"}')
        if not self.authorized():
            return self.problem("permanent", "missing or wrong bearer secret", status=401)
        if self.path == "/v1/describe":
            return self.reply(200, dumps({"protocol": PROTOCOL, **garmin.describe()}))
        self.problem("permanent", "not found", status=404)

    def do_POST(self):
        if not self.authorized():
            return self.problem("permanent", "missing or wrong bearer secret", status=401)
        body = self.json_body()
        if body is None:
            return self.problem("permanent", "body must be a JSON object", status=400)
        try:
            if self.path == "/v1/fetch":
                return self.fetch(body)
            if self.path == "/v1/auth/begin":
                out = garmin.begin()
            elif self.path == "/v1/auth/continue":
                values = body["values"]  # the sign-in, or with a session the MFA code
                out = (garmin.cont(body["session"], values["code"]) if body.get("session")
                       else garmin.login(values["username"], values["password"]))
            elif self.path == "/v1/auth/refresh":
                out = garmin.refresh(body["credentials"])
            else:
                return self.problem("permanent", "not found", status=404)
        except (KeyError, TypeError):
            return self.problem("permanent", "missing or malformed field", status=400)
        except garmin.Failure as f:
            return self.fail(f)
        except Exception as e:  # a bug here: say so without echoing anything
            logging.error("%s %s crashed: %s", self.command, self.path, type(e).__name__)
            return self.problem("permanent", "internal error")
        self.reply(200, dumps(out))

    def fail(self, f):
        logging.warning("%s %s -> %s", self.command, self.path, f.code)
        self.problem(f.code, f.detail, retry_after_s=f.retry_after_s, endpoint=f.endpoint, fingerprint=f.fingerprint)

    def fetch(self, body):
        stream = garmin.fetch(body)
        try:
            first = next(stream)  # runs up to the first item, so errors still get a status
        except garmin.Failure as f:
            return self.fail(f)
        self.send_response(200)
        self.send_header("Content-Type", "application/x-ndjson")
        self.send_header("Vitamux-Protocol", PROTOCOL)
        self.send_header("Connection", "close")  # streamed until close
        self.end_headers()
        self.close_connection = True
        try:
            for x in chain([first], stream):
                self.wfile.write(result_line(x) if isinstance(x, garmin.Result) else raw_line(x))
                self.wfile.flush()
        except Exception as e:  # headers are out: cut the stream, the core sees no result line
            logging.warning("fetch aborted: %s", type(e).__name__)


def make_server(addr, secret):
    host, _, port = addr.rpartition(":")
    handler = type("H", (Handler,), {"secret": secret.encode()})
    srv = ThreadingHTTPServer((host or "0.0.0.0", int(port)), handler)
    srv.daemon_threads = True
    return srv
