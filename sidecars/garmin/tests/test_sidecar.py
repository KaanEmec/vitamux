"""Sidecar tests: stdlib unittest, the Garmin client is faked, no network.

    python -m unittest -v
"""

import base64
import hashlib
import io
import json
import logging
import threading
import unittest
from datetime import datetime, timezone
from pathlib import Path
import urllib.error
import urllib.request
import zipfile
from unittest import mock

from garminconnect import (
    Garmin,
    GarminConnectAuthenticationError,
    GarminConnectConnectionError,
    GarminConnectNotFoundError,
    GarminConnectTooManyRequestsError,
)

from src import garmin, replay, server

SECRET = "test-bearer-secret"
PASSWORD = "sentinel-password"
CODE = "424242"
DISPLAY = "11111111-2222-3333-4444-555555555555"
PROFILE = {"displayName": DISPLAY, "profileId": 12345}
REDIRECT = "http://localhost:8080/api/v1/connections/callback"


class Fake(Garmin):
    """A Garmin whose network calls are scripted; everything else is the real class."""

    mfa = False
    handler = None  # (path, params) -> response, or an exception to raise
    downloads = {}  # path -> bytes or exception
    rotate_to = None  # token the "library" refreshes to during a call
    instances = []

    def __init__(self, *a, **kw):
        super().__init__(*a, **kw)
        self.calls = []
        Fake.instances.append(self)

    def _sign_in(self):
        self.client.di_token, self.client.di_refresh_token = "access-A", "refresh-A"
        self.client.di_client_id = "client-id"

    def login(self, tokenstore=None):
        if self.password != PASSWORD:
            raise GarminConnectAuthenticationError("Authentication failed (401 Unauthorized)")
        if Fake.mfa:
            return "needs_mfa", None
        self._sign_in()
        return None, None

    def resume_login(self, state, code):
        if code != CODE:
            raise GarminConnectAuthenticationError("MFA verification failed: ['x: INVALID']")
        self._sign_in()
        return None, None

    def connectapi(self, path, **kw):
        self.calls.append((path, kw.get("params")))
        if Fake.rotate_to:
            self.client.di_token = Fake.rotate_to
        if path == garmin.PROFILE:
            return PROFILE
        out = Fake.handler(path, kw.get("params")) if Fake.handler else {}
        if isinstance(out, Exception):
            raise out
        return out

    def download(self, path, **kw):
        out = Fake.downloads[path]
        if isinstance(out, Exception):
            raise out
        return out


def signed_in_credentials():
    g = Fake()
    g._sign_in()
    return garmin._creds(g, {"display_name": DISPLAY, "profile_id": 12345})


class Base(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.srv = server.make_server("127.0.0.1:0", SECRET)
        cls.base = "http://127.0.0.1:%d" % cls.srv.server_address[1]
        threading.Thread(target=cls.srv.serve_forever, daemon=True).start()

    @classmethod
    def tearDownClass(cls):
        cls.srv.shutdown()
        cls.srv.server_close()

    def setUp(self):
        Fake.mfa, Fake.handler, Fake.downloads, Fake.rotate_to = False, None, {}, None
        Fake.instances = []
        garmin._pending.clear()
        for p in (mock.patch.object(garmin, "Garmin", Fake), mock.patch.object(garmin, "DELAY", 0)):
            p.start()
            self.addCleanup(p.stop)
        self.logs = io.StringIO()
        h = logging.StreamHandler(self.logs)
        logging.getLogger().addHandler(h)
        self.addCleanup(logging.getLogger().removeHandler, h)

    def call(self, method, path, body=None, auth=True):
        data = json.dumps(body).encode() if body is not None else None
        req = urllib.request.Request(self.base + path, data=data, method=method)
        if auth:
            req.add_header("Authorization", "Bearer " + SECRET)
        if data:
            req.add_header("Content-Type", "application/json")
        try:
            with urllib.request.urlopen(req) as r:
                return r.status, r.headers, r.read()
        except urllib.error.HTTPError as e:
            with e:
                return e.code, e.headers, e.read()

    def post(self, path, body):
        status, headers, raw = self.call("POST", path, body)
        return status, headers, json.loads(raw)

    def problem(self, path, body, code, status):
        st, headers, p = self.post(path, body)
        self.assertEqual((st, p["code"], p["status"]), (status, code, status), p)
        self.assertEqual(headers["Content-Type"], "application/problem+json")
        return p, headers

    def fetch(self, start="2026-10-01", end="2026-10-02", timezone="UTC", **over):
        """A window request (correction or backfill): [start, end) in the owner's timezone."""
        req = {"stream": "garmin.heart_rate", "from": start, "to": end,
               "credentials": signed_in_credentials(), "config": {"timezone": timezone}, **over}
        status, headers, raw = self.call("POST", "/v1/fetch", req)
        if status != 200:
            return status, headers, json.loads(raw)
        self.assertEqual(headers["Content-Type"], "application/x-ndjson")
        self.assertEqual(headers["Vitamux-Protocol"], "vitamux-connector/1")
        lines = [json.loads(x) for x in raw.splitlines()]
        return status, raw, lines


class ProtocolTest(Base):
    def test_healthz_is_open_everything_else_needs_the_secret(self):
        self.assertEqual(self.call("GET", "/healthz", auth=False)[0], 200)
        status, headers, raw = self.call("GET", "/v1/describe", auth=False)
        self.assertEqual((status, json.loads(raw)["code"], headers["Vitamux-Protocol"]),
                         (401, "permanent", "vitamux-connector/1"))
        self.assertEqual(self.call("POST", "/v1/fetch", {}, auth=False)[0], 401)

    def test_describe(self):
        status, _, raw = self.call("GET", "/v1/describe")
        d = json.loads(raw)
        self.assertEqual(status, 200)
        self.assertEqual((d["protocol"], d["provider"], d["name"], d["official"], d["auth_kind"]),
                         ("vitamux-connector/1", "garmin", "Garmin Connect (unofficial)", False, "interactive_mfa"))
        self.assertEqual(d["capabilities"], {"incremental": True, "backfill": True, "manual_sync": True})
        self.assertEqual(d["upstream"]["package"], "garminconnect")
        self.assertRegex(d["upstream"]["version"], r"^0\.3\.\d+$")
        self.assertEqual(d["upstream"]["source_url"], "https://github.com/cyberjunky/python-garminconnect")
        names = [s["name"] for s in d["streams"]]
        self.assertEqual(len(names), 12)
        self.assertIn("garmin.activities", names)
        for s in d["streams"]:
            self.assertEqual(set(s), {"name", "interval_s", "lookback_s", "unit_size_s", "max_backfill_s"})
            self.assertTrue(all(isinstance(v, int) for v in s.values() if not isinstance(v, str)))
            self.assertLessEqual(s["unit_size_s"], s["max_backfill_s"])
        self.assertEqual(d["streams"][0], {"name": "garmin.daily_summary", "interval_s": 3600, "lookback_s": 259200,
                                           "unit_size_s": 86400, "max_backfill_s": 157680000})
        self.assertEqual(d["rate_limits"], [{"requests": 2, "per_s": 4}])

    def test_malformed_requests(self):
        self.assertEqual(self.call("POST", "/v1/auth/continue", {"values": {}})[0], 400)
        self.assertEqual(self.call("POST", "/v1/auth/continue", {"session": "x"})[0], 400)
        self.assertEqual(self.call("POST", "/v1/nope", {})[0], 404)
        self.assertEqual(self.call("POST", "/v1/auth/begin", None)[0], 400)


class AuthTest(Base):
    def login(self, password=PASSWORD):
        return self.post("/v1/auth/continue", {"redirect_url": REDIRECT, "values": {"username": "me@example.com", "password": password}})

    def code(self, session, code=CODE):
        return self.post("/v1/auth/continue", {"redirect_url": REDIRECT, "session": session, "values": {"code": code}})

    def check_authorized(self, out):
        a = out["authorized"]
        self.assertEqual(a["account_id"], "12345")
        self.assertEqual(a["credentials"], {"access_token": "access-A", "refresh_token": "refresh-A",
                                            "extra": {"display_name": DISPLAY, "profile_id": 12345,
                                                      "di_client_id": "client-id"}})

    def test_begin_asks_for_the_sign_in_without_calling_garmin(self):
        status, headers, out = self.post("/v1/auth/begin", {"redirect_url": REDIRECT, "state": "s"})
        self.assertEqual(status, 200)
        self.assertEqual([(f["name"], f["kind"]) for f in out["step"]["prompt"]["fields"]],
                         [("username", "text"), ("password", "password")])
        self.assertNotIn("session", out["step"])
        self.assertEqual(Fake.instances, [])

    def test_login_without_mfa(self):
        status, _, out = self.login()
        self.assertEqual(status, 200)
        self.check_authorized(out)
        self.assertIsNone(Fake.instances[0].password)  # not kept after sign-in

    def test_login_with_mfa(self):
        Fake.mfa = True
        status, _, out = self.login()
        self.assertEqual(status, 200)
        step = out["step"]
        self.assertEqual(step["prompt"]["fields"], [{"name": "code", "label": "Verification code", "kind": "code"}])
        self.assertEqual(len(base64.b64decode(step["session"], validate=True)), 32)  # standard base64
        self.assertIsNone(Fake.instances[0].password)
        status, _, done = self.code(step["session"])
        self.assertEqual(status, 200)
        self.check_authorized(done)
        self.assertEqual(garmin._pending, {})

    def test_wrong_code_then_retry(self):
        Fake.mfa = True
        sid = self.login()[2]["step"]["session"]
        self.problem("/v1/auth/continue", {"session": sid, "values": {"code": "000000"}}, "reauth_required", 401)
        status, _, done = self.code(sid)
        self.assertEqual(status, 200)
        self.check_authorized(done)

    def test_wrong_password(self):
        self.problem("/v1/auth/continue", {"values": {"username": "u", "password": "wrong"}}, "reauth_required", 401)

    def test_unknown_and_expired_session(self):
        for sid in ("Z2FyYmFnZQ==", "not base64!"):
            self.problem("/v1/auth/continue", {"session": sid, "values": {"code": CODE}}, "reauth_required", 401)
        Fake.mfa = True
        sid = self.login()[2]["step"]["session"]
        with mock.patch.object(garmin, "MFA_TTL", -1):  # a session begun now is already stale
            Fake.mfa = True
            stale = self.login()[2]["step"]["session"]
        self.problem("/v1/auth/continue", {"session": stale, "values": {"code": CODE}}, "reauth_required", 401)
        self.assertNotIn(base64.b64decode(stale), garmin._pending)
        self.assertEqual(self.code(sid)[0], 200)

    def test_pending_sessions_are_bounded(self):
        Fake.mfa = True
        for _ in range(garmin.MAX_PENDING + 3):
            self.login()
        self.assertEqual(len(garmin._pending), garmin.MAX_PENDING)

    def test_refresh_rotates_credentials(self):
        creds = signed_in_credentials()

        def rotate(self):
            self.di_token, self.di_refresh_token = "access-B", "refresh-B"

        with mock.patch("garminconnect.client.Client._refresh_di_token", rotate):
            status, _, out = self.post("/v1/auth/refresh", {"credentials": creds})
        self.assertEqual(status, 200)
        new = out["credentials"]
        self.assertEqual((new["access_token"], new["refresh_token"], new["extra"]["display_name"]),
                         ("access-B", "refresh-B", DISPLAY))

    def test_refresh_errors(self):
        creds = signed_in_credentials()
        cases = [("DI token refresh failed: 400", "reauth_required", 401),
                 ("DI token refresh failed: 429", "rate_limited", 429),
                 ("DI token refresh failed: 503", "transient", 503)]
        for msg, code, status in cases:
            with self.subTest(msg), mock.patch(
                "garminconnect.client.Client._refresh_di_token",
                side_effect=GarminConnectAuthenticationError(msg),
            ):
                self.problem("/v1/auth/refresh", {"credentials": creds}, code, status)
        with mock.patch("garminconnect.client.Client._refresh_di_token", side_effect=ConnectionResetError()):
            self.problem("/v1/auth/refresh", {"credentials": creds}, "transient", 503)

    def test_unreadable_credentials(self):
        self.problem("/v1/auth/refresh", {"credentials": {}}, "reauth_required", 401)
        self.problem("/v1/auth/refresh", {"credentials": {"access_token": "x", "refresh_token": "y"}}, "reauth_required", 401)

    def test_no_secret_is_logged_or_echoed(self):
        Fake.mfa = True
        sid = self.login()[2]["step"]["session"]
        raws = [self.call("POST", "/v1/auth/continue", {"values": {"username": "me@example.com", "password": "bad-" + PASSWORD}}),
                self.call("POST", "/v1/auth/continue", {"session": sid, "values": {"code": "999999"}}),
                self.call("POST", "/v1/auth/continue", {"session": sid, "values": {"code": CODE}})]
        for _, _, body in raws[:2]:
            self.assertNotIn(PASSWORD.encode(), body)
            self.assertNotIn(b"999999", body)
        logged = self.logs.getvalue()
        for secret in (PASSWORD, CODE, "999999", "access-A", "refresh-A", "me@example.com", sid):
            self.assertNotIn(secret, logged)


class ErrorTest(Base):
    def run_error(self, exc):
        Fake.handler = lambda path, params: exc
        return self.fetch()

    def test_error_classes(self):
        timeout = GarminConnectConnectionError("Connection error: boom")
        timeout.__cause__ = TimeoutError()
        badjson = GarminConnectConnectionError("Connection error: Expecting value")
        badjson.__cause__ = json.JSONDecodeError("Expecting value", "", 0)
        cases = [
            (GarminConnectAuthenticationError("Authentication failed: 401"), "reauth_required", 401),
            (GarminConnectConnectionError("API Error 401"), "reauth_required", 401),
            (GarminConnectTooManyRequestsError("Rate limit exceeded"), "rate_limited", 429),
            (GarminConnectConnectionError("API Error 429"), "rate_limited", 429),
            (GarminConnectConnectionError("API Error 503 - down"), "transient", 503),
            (GarminConnectConnectionError("Connection error: reset"), "transient", 503),
            (timeout, "transient", 503),
            (ConnectionError(), "transient", 503),
            (badjson, "schema_drift", 502),
            (GarminConnectConnectionError("API Error 403"), "permanent", 500),
            (GarminConnectNotFoundError("API Error 404"), "permanent", 500),
            (RuntimeError("anything else"), "permanent", 500),
        ]
        for exc, code, status in cases:
            with self.subTest(str(exc) or type(exc).__name__):
                st, _, p = self.run_error(exc)
                self.assertEqual((st, p["code"], p["status"]), (status, code, status), p)
                self.assertNotIn("detail", p if code != "schema_drift" else {})

    def test_rate_limited_carries_retry_after(self):
        st, headers, p = self.run_error(GarminConnectTooManyRequestsError("x"))
        self.assertEqual((p["retry_after_s"], headers["Retry-After"]), (300, "300"))
        exc = GarminConnectConnectionError("API Error 429")
        exc.response = mock.Mock(status_code=429, headers={"Retry-After": "42"})
        st, headers, p = self.run_error(exc)
        self.assertEqual((p["retry_after_s"], headers["Retry-After"]), (42, "42"))

    def test_unexpected_top_level_type_is_schema_drift(self):
        for stream, bad in [("garmin.heart_rate", [1]), ("garmin.steps", {"a": 1}), ("garmin.sleep", "x")]:
            with self.subTest(stream):
                Fake.handler = lambda path, params: bad
                st, _, p = self.fetch(stream=stream)
                self.assertEqual((st, p["code"]), (502, "schema_drift"))

    def test_bad_requests(self):
        self.assertEqual(self.fetch(stream="garmin.nope")[2]["code"], "permanent")
        self.assertEqual(self.fetch(timezone="Mars/Base")[2]["code"], "permanent")
        self.assertEqual(self.fetch(start="yesterday")[2]["code"], "permanent")
        self.assertEqual(self.fetch(credentials="")[2]["code"], "reauth_required")


class FetchTest(Base):
    def test_day_stream(self):
        verbatim = {"calendarDate": "2026-10-01", "x": 1.0, "n": None, "s": "é"}
        Fake.handler = lambda path, params: {} if params["date"] == "2026-10-02" else verbatim
        st, raw, lines = self.fetch(start="2026-10-01", end="2026-10-04")
        self.assertEqual(st, 200)
        *items, result = lines
        self.assertEqual([i["external_key"] for i in items],
                         [f"garmin.heart_rate:2026-10-0{d}" for d in (1, 2, 3)])
        first = items[0]
        self.assertEqual(first["type"], "raw")
        self.assertEqual(first["content_type"], "application/json")
        self.assertEqual(first["body"], {"unit": {"date": "2026-10-01"}, "response": verbatim})
        self.assertEqual(first["request"], {"endpoint": f"/wellness-service/wellness/dailyHeartRate/{DISPLAY}",
                                            "params": {"date": "2026-10-01"}})
        for i in items:  # a JSON body goes out as is, with no hash
            self.assertNotIn("sha256", i)
            self.assertIn(b'"body":' + server.dumps(i["body"]), next(l for l in raw.splitlines() if i["external_key"].encode() in l))
        self.assertEqual(items[1]["body"]["response"], {})  # an empty day is still the day's raw
        self.assertEqual(set(result), {"type", "done", "high_watermark"})  # a finished window leaves the stream cursor alone
        self.assertEqual((result["type"], result["done"]), ("result", True))
        self.assertTrue(result["high_watermark"].startswith("2026-10-04T00:00:00"))

    def test_timezone_picks_the_local_days(self):
        Fake.handler = lambda path, params: {}
        # 22:00Z on 2026-10-01 is already 2026-10-02 in Istanbul (UTC+3); the window is one local day.
        _, _, lines = self.fetch(start="2026-10-01T22:00:00Z", end="2026-10-02T21:00:00Z", timezone="Europe/Istanbul")
        self.assertEqual([l["external_key"] for l in lines[:-1]], ["garmin.heart_rate:2026-10-02"])
        self.assertTrue(lines[-1]["high_watermark"].endswith("+03:00"))

    def test_every_day_stream_hits_its_endpoint(self):
        expect = {
            "garmin.daily_summary": f"/usersummary-service/usersummary/daily/{DISPLAY}",
            "garmin.steps": f"/wellness-service/wellness/dailySummaryChart/{DISPLAY}",
            "garmin.stress_body_battery": "/wellness-service/wellness/dailyStress/2026-10-01",
            "garmin.sleep": f"/wellness-service/wellness/dailySleepData/{DISPLAY}",
            "garmin.hrv": "/hrv-service/hrv/2026-10-01",
            "garmin.respiration": "/wellness-service/wellness/daily/respiration/2026-10-01",
            "garmin.spo2": "/wellness-service/wellness/daily/spo2/2026-10-01",
        }
        Fake.handler = lambda path, params: [] if "dailySummaryChart" in path else {}
        for stream, path in expect.items():
            with self.subTest(stream):
                _, _, lines = self.fetch(stream=stream)
                self.assertEqual(lines[0]["request"]["endpoint"], path)
                self.assertEqual(lines[0]["external_key"], f"{stream}:2026-10-01")

    def test_training_has_two_items_per_day(self):
        Fake.handler = lambda path, params: [{}] if "maxmet" in path else []
        _, _, lines = self.fetch(stream="garmin.training")
        self.assertEqual([l["external_key"] for l in lines[:-1]],
                         ["garmin.training:2026-10-01:vo2max", "garmin.training:2026-10-01:readiness"])
        self.assertEqual(lines[0]["request"]["endpoint"], "/metrics-service/metrics/maxmet/daily/2026-10-01/2026-10-01")
        self.assertEqual(lines[1]["request"]["endpoint"], "/metrics-service/metrics/trainingreadiness/2026-10-01")

    def test_range_streams_use_stable_30_day_blocks(self):
        Fake.handler = lambda path, params: {"dateWeightList": []}
        _, _, a = self.fetch(stream="garmin.body_composition", start="2026-10-02", end="2026-10-05")
        _, _, b = self.fetch(stream="garmin.body_composition", start="2026-10-03", end="2026-10-08")
        self.assertEqual(a[0]["external_key"], b[0]["external_key"])  # same block, same key
        unit = a[0]["body"]["unit"]
        self.assertEqual(set(unit), {"start", "end"})
        self.assertEqual(a[0]["request"]["endpoint"], "/weight-service/weight/dateRange")
        self.assertEqual(a[0]["request"]["params"], {"startDate": unit["start"], "endDate": unit["end"]})
        self.assertEqual(a[0]["external_key"], "garmin.body_composition:%s_%s" % (unit["start"], unit["end"]))
        self.assertLessEqual(unit["start"], "2026-10-02")
        self.assertGreaterEqual(unit["end"], "2026-10-02")
        _, _, bp = self.fetch(stream="garmin.blood_pressure", start="2026-10-02", end="2026-10-05")
        u = bp[0]["body"]["unit"]
        self.assertEqual(bp[0]["request"]["endpoint"], f"/bloodpressure-service/bloodpressure/range/{u['start']}/{u['end']}")
        _, _, two = self.fetch(stream="garmin.blood_pressure", start="2026-09-20", end="2026-10-20")
        self.assertGreaterEqual(len(two) - 1, 2)

    def test_units_per_call_are_capped_and_resumable(self):
        Fake.handler = lambda path, params: {}
        with mock.patch.object(garmin, "MAX_UNITS", 2):
            _, _, lines = self.fetch(start="2026-10-01", end="2026-10-06")
            self.assertEqual(len(lines) - 1, 2)
            self.assertEqual((lines[-1]["done"], lines[-1]["next_cursor"]), (False, {"day": "2026-10-03"}))
            self.assertTrue(lines[-1]["high_watermark"].startswith("2026-10-03T00:00:00"))
            _, _, rest = self.fetch(start="2026-10-01", end="2026-10-06", cursor=lines[-1]["next_cursor"])
            self.assertEqual([l["external_key"][-10:] for l in rest[:-1]], ["2026-10-03", "2026-10-04"])
            _, _, last = self.fetch(start="2026-10-01", end="2026-10-06", cursor=rest[-1]["next_cursor"])
            self.assertEqual((len(last), last[-1]["done"]), (2, True))

    def test_empty_window(self):
        _, _, lines = self.fetch(start="2026-10-02", end="2026-10-02")
        self.assertEqual(lines, [{"type": "result", "done": True}])

    def test_failure_after_progress_is_a_partial_result(self):
        def handler(path, params):
            if params["date"] == "2026-10-02":
                return GarminConnectTooManyRequestsError("slow down")
            return {}
        Fake.handler = handler
        st, _, lines = self.fetch(start="2026-10-01", end="2026-10-04")
        self.assertEqual(st, 200)
        self.assertEqual(len(lines), 2)
        self.assertEqual((lines[-1]["done"], lines[-1]["next_cursor"]), (False, {"day": "2026-10-02"}))
        st, _, p = self.fetch(start="2026-10-02", end="2026-10-04")  # no progress: the error itself
        self.assertEqual((st, p["code"]), (429, "rate_limited"))

    def test_rotated_credentials_come_back_on_the_result_line(self):
        creds = signed_in_credentials()
        Fake.handler = lambda path, params: {}
        Fake.rotate_to = "access-B"
        _, _, lines = self.fetch(credentials=creds)
        rotated = lines[-1]["credentials"]
        self.assertEqual(rotated["access_token"], "access-B")
        self.assertEqual(rotated["extra"]["display_name"], DISPLAY)

    def test_activities_page_through_the_range_with_fit_files(self):
        acts = [{"activityId": 100 + i, "activityName": f"run {i}", "startTimeGMT": "2026-10-01 06:00:00"} for i in range(3)]
        zipped = io.BytesIO()
        with zipfile.ZipFile(zipped, "w") as z:
            z.writestr("100_ACTIVITY.fit", b"\x0e\x10synthetic")
        fit = zipped.getvalue()
        Fake.downloads = {
            "/download-service/files/activity/100": fit,
            "/download-service/files/activity/101": GarminConnectNotFoundError("API Error 404"),  # manual entry
            "/download-service/files/activity/102": fit,
        }
        seen = []

        def handler(path, params):
            seen.append(params)
            return acts[int(params["start"]):int(params["start"]) + int(params["limit"])]

        Fake.handler = handler
        with mock.patch.object(garmin, "PAGE", 2):
            st, _, p1 = self.fetch(stream="garmin.activities", start="2026-10-01", end="2026-10-08")
            self.assertEqual(st, 200)
            self.assertEqual(seen[0], {"startDate": "2026-10-01", "endDate": "2026-10-07", "start": "0",
                                       "limit": "2", "sortOrder": "asc"})
            self.assertEqual([l["external_key"] for l in p1[:-1]],
                             ["garmin.activities:100", "garmin.activities:100:fit", "garmin.activities:101"])
            self.assertEqual(p1[0]["body"], {"unit": {"activity_id": 100}, "response": acts[0]})
            fit_line = p1[1]
            self.assertEqual((fit_line["content_type"], fit_line["sha256"]), ("application/zip", hashlib.sha256(fit).hexdigest()))
            self.assertEqual(base64.b64decode(fit_line["body_base64"]), fit)
            self.assertNotIn("body", fit_line)
            self.assertEqual((p1[-1]["done"], p1[-1]["next_cursor"]), (False, {"offset": 2}))
            _, _, p2 = self.fetch(stream="garmin.activities", start="2026-10-01", end="2026-10-08",
                                  cursor=p1[-1]["next_cursor"])
            self.assertEqual([l["external_key"] for l in p2[:-1]], ["garmin.activities:102", "garmin.activities:102:fit"])
            self.assertEqual((p2[-1]["done"], "next_cursor" in p2[-1]), (True, False))
            self.assertEqual(seen[-1]["start"], "2")

    def test_activity_without_id_is_schema_drift(self):
        Fake.handler = lambda path, params: [{"name": "x"}]
        st, _, p = self.fetch(stream="garmin.activities")
        self.assertEqual((st, p["code"]), (502, "schema_drift"))
        self.assertEqual(p["endpoint"], "/activitylist-service/activities/search/activities")
        self.assertRegex(p["fingerprint"], r"^[0-9a-f]{64}$")

    def test_schema_drift_names_the_endpoint_and_a_shape_fingerprint(self):
        Fake.handler = lambda path, params: [1]  # a list where the stream has an object
        _, _, a = self.fetch()
        Fake.handler = lambda path, params: [2, 3]  # same shape, other values
        _, _, b = self.fetch()
        Fake.handler = lambda path, params: ["x"]
        _, _, c = self.fetch()
        self.assertEqual(a["endpoint"], f"/wellness-service/wellness/dailyHeartRate/{DISPLAY}")
        self.assertEqual(a["fingerprint"], b["fingerprint"])
        self.assertNotEqual(a["fingerprint"], c["fingerprint"])
        self.assertNotIn('"x"', json.dumps(c))  # values never appear


class IncrementalTest(Base):
    """An incremental or manual run sends no window: the stored cursor {since} to today."""

    def setUp(self):
        super().setUp()
        Fake.handler = lambda path, params: {}
        p = mock.patch.object(garmin, "NOW", lambda: datetime(2026, 10, 3, 22, 30, tzinfo=timezone.utc))
        p.start()
        self.addCleanup(p.stop)

    def run_fetch(self, cursor=None, **over):
        req = {"stream": "garmin.heart_rate", "mode": "incremental", "credentials": signed_in_credentials(),
               "config": {"timezone": "UTC"}, "cursor": cursor, **over}
        _, _, lines = self.call_lines(req)
        return lines

    def call_lines(self, req):
        status, _, raw = self.call("POST", "/v1/fetch", req)
        self.assertEqual(status, 200, raw)
        return status, raw, [json.loads(x) for x in raw.splitlines()]

    def test_first_run_looks_back_two_days_and_stores_since_today(self):
        lines = self.run_fetch()
        self.assertEqual([l["external_key"] for l in lines[:-1]],
                         [f"garmin.heart_rate:2026-10-0{d}" for d in (1, 2, 3)])
        self.assertEqual(lines[-1]["next_cursor"], {"since": "2026-10-03"})
        self.assertTrue(lines[-1]["done"])

    def test_next_run_starts_at_since_and_refetches_that_day(self):
        lines = self.run_fetch({"since": "2026-10-03"})
        self.assertEqual([l["external_key"] for l in lines[:-1]], ["garmin.heart_rate:2026-10-03"])
        self.assertEqual(lines[-1]["next_cursor"], {"since": "2026-10-03"})

    def test_the_owners_timezone_decides_today(self):
        lines = self.run_fetch({"since": "2026-10-03"}, config={"timezone": "Europe/Istanbul"})  # 01:30 on the 4th there
        self.assertEqual([l["external_key"] for l in lines[:-1]], ["garmin.heart_rate:2026-10-03", "garmin.heart_rate:2026-10-04"])
        self.assertEqual(lines[-1]["next_cursor"], {"since": "2026-10-04"})

    def test_pages_keep_since_and_resume_at_day(self):
        with mock.patch.object(garmin, "MAX_UNITS", 2):
            first = self.run_fetch()
            self.assertEqual((first[-1]["done"], first[-1]["next_cursor"]), (False, {"since": "2026-10-01", "day": "2026-10-03"}))
            rest = self.run_fetch(first[-1]["next_cursor"])
        self.assertEqual([l["external_key"] for l in rest[:-1]], ["garmin.heart_rate:2026-10-03"])
        self.assertEqual((rest[-1]["done"], rest[-1]["next_cursor"]), (True, {"since": "2026-10-03"}))

    def test_range_streams_refetch_the_block_holding_since(self):
        Fake.handler = lambda path, params: {"dateWeightList": []}
        lines = self.run_fetch({"since": "2026-10-03"}, stream="garmin.body_composition")
        unit = lines[0]["body"]["unit"]
        self.assertTrue(unit["start"] <= "2026-10-03" <= unit["end"])

    def test_activities_keep_since_across_offset_pages(self):
        acts = [{"activityId": 100 + i} for i in range(3)]
        Fake.handler = lambda path, params: acts[int(params["start"]):int(params["start"]) + int(params["limit"])]
        Fake.downloads = {f"/download-service/files/activity/{100 + i}": GarminConnectNotFoundError("API Error 404") for i in range(3)}
        with mock.patch.object(garmin, "PAGE", 2):
            first = self.run_fetch({"since": "2026-10-02"}, stream="garmin.activities")
            self.assertEqual(first[-1]["next_cursor"], {"since": "2026-10-02", "offset": 2})
            rest = self.run_fetch(first[-1]["next_cursor"], stream="garmin.activities")
        self.assertEqual(rest[-1]["next_cursor"], {"since": "2026-10-03"})
        self.assertNotIn("high_watermark", rest[-1])

    def test_bad_cursor_is_permanent(self):
        for cursor in ("x", {"since": "yesterday"}, {"offset": "1"}):
            status, _, raw = self.call("POST", "/v1/fetch", {"stream": "garmin.heart_rate", "cursor": cursor,
                                                               "credentials": signed_in_credentials()})
            self.assertEqual((status, json.loads(raw)["code"]), (500, "permanent"))


class ReplayTest(Base):
    def test_replay_serves_the_cassette_without_garmin(self):
        with mock.patch.multiple(garmin, Garmin=garmin.Garmin, DELAY=garmin.DELAY, NOW=garmin.NOW, DEFAULT_EXTRA=garmin.DEFAULT_EXTRA):
            replay.install(garmin, Path(__file__).parent.parent / "testdata" / "replay.json")
            req = {"stream": "garmin.heart_rate", "credentials": {"access_token": "synthetic-access-token", "refresh_token": "synthetic-refresh-token"}}
            status, _, raw = self.call("POST", "/v1/fetch", req)
            lines = [json.loads(x) for x in raw.splitlines()]
        self.assertEqual(status, 200)
        self.assertEqual([l["external_key"] for l in lines[:-1]], [f"garmin.heart_rate:2026-09-1{d}" for d in (2, 3, 4)])
        self.assertTrue(lines[0]["body"]["response"]["synthetic"])
        self.assertEqual(lines[-1]["next_cursor"], {"since": "2026-09-14"})


if __name__ == "__main__":
    unittest.main()
