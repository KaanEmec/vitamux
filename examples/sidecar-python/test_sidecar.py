"""Run with: python -m unittest (from this directory). Starts the sidecar on a free local port."""

import json
import pathlib
import threading
import unittest
import urllib.error
import urllib.request

import sidecar

SECRET = b"synthetic-test-secret"
sidecar.Handler.log_request = lambda *args, **kwargs: None  # keep test output quiet
GOLDEN = pathlib.Path(__file__).parents[2] / "internal/connectors/example/testdata/example_sidecar.heart_rate/normal.raw.json"


class Server:
    def __init__(self, brk: str = ""):
        self.srv = sidecar.make_server(("127.0.0.1", 0), SECRET, brk)
        self.url = "http://127.0.0.1:%d" % self.srv.server_address[1]
        threading.Thread(target=self.srv.serve_forever, kwargs={"poll_interval": 0.01}, daemon=True).start()

    def close(self):
        self.srv.shutdown()
        self.srv.server_close()

    def call(self, path: str, body: dict | None = None, secret: bytes = SECRET):
        """Returns (status, headers, bytes)."""
        req = urllib.request.Request(self.url + path, data=None if body is None else json.dumps(body).encode(), method="GET" if body is None else "POST")
        req.add_header("Authorization", "Bearer " + secret.decode())
        try:
            with urllib.request.urlopen(req) as r:
                return r.status, r.headers, r.read()
        except urllib.error.HTTPError as e:
            return e.code, e.headers, e.read()

    def json(self, path, body=None):
        status, _, raw = self.call(path, body)
        return status, json.loads(raw)

    def sign_in(self, username="synthetic-user"):
        _, step = self.json("/v1/auth/begin", {})
        _, step = self.json("/v1/auth/continue", {"session": step["step"]["session"], "values": {"username": username, "password": "synthetic-pass"}})
        _, done = self.json("/v1/auth/continue", {"session": step["step"]["session"], "values": {"code": "123456"}})
        return done["authorized"]["credentials"]

    def fetch(self, creds, cursor=None):
        status, headers, raw = self.call("/v1/fetch", {"stream": sidecar.STREAM, "mode": "incremental", "cursor": cursor, "credentials": creds})
        lines = [json.loads(line) for line in raw.decode().splitlines()] if status == 200 else []
        return status, headers, raw, lines


class Base(unittest.TestCase):
    brk = ""

    def setUp(self):
        self.s = Server(self.brk)
        self.addCleanup(self.s.close)


class Conformant(Base):
    def test_describe(self):
        status, headers, raw = self.s.call("/v1/describe")
        d = json.loads(raw)
        self.assertEqual((status, headers["Vitamux-Protocol"], d["provider"], d["streams"][0]["name"]), (200, sidecar.PROTOCOL, "example_sidecar", sidecar.STREAM))

    def test_secret_is_required(self):
        status, _, raw = self.s.call("/v1/describe", secret=b"wrong")
        self.assertEqual(status, 401)
        self.assertEqual(json.loads(raw)["code"], "permanent")
        self.assertNotIn(SECRET, raw)

    def test_auth_flow_and_rejections(self):
        creds = self.s.sign_in()
        self.assertTrue(creds["access_token"].startswith("synthetic-access-ok-"))
        _, step = self.s.json("/v1/auth/begin", {})
        self.assertEqual([f["name"] for f in step["step"]["prompt"]["fields"]], ["username", "password"])
        status, p = self.s.json("/v1/auth/continue", {"session": step["step"]["session"], "values": {"username": "synthetic-user", "password": "nope"}})
        self.assertEqual((status, p["code"]), (401, "reauth_required"))
        status, p = self.s.json("/v1/auth/continue", {"session": "AAAA", "values": {}})
        self.assertEqual((status, p["code"]), (400, "permanent"))

    def test_refresh_rotates(self):
        creds = self.s.sign_in()
        status, out = self.s.json("/v1/auth/refresh", {"credentials": creds})
        self.assertEqual(status, 200)
        self.assertNotEqual(out["credentials"]["refresh_token"], creds["refresh_token"])
        status, p = self.s.json("/v1/auth/refresh", {"credentials": {"refresh_token": "garbage"}})
        self.assertEqual((status, p["code"]), (401, "reauth_required"))

    def test_paging_and_replay(self):
        creds, cursor, keys, pages = self.s.sign_in(), None, [], []
        while True:
            status, headers, raw, lines = self.s.fetch(creds, cursor)
            self.assertEqual((status, headers["Content-Type"]), (200, "application/x-ndjson"))
            *raws, result = lines
            self.assertEqual(result["type"], "result")
            self.assertTrue(all(r["type"] == "raw" for r in raws))
            keys += [r["external_key"] for r in raws]
            pages.append((cursor, raw))
            if result["done"]:
                break
            cursor = result["next_cursor"]
        self.assertEqual((len(keys), len(set(keys)), len(pages)), (sidecar.SAMPLES, sidecar.SAMPLES, 3))
        self.assertEqual(result["high_watermark"], "2026-09-14T09:00:00Z")
        for cursor, raw in pages:  # replaying a cursor returns the same bytes
            self.assertEqual(self.s.fetch(creds, cursor)[2], raw)
        _, _, _, lines = self.s.fetch(creds, result["next_cursor"])  # nothing new, cursor stays put
        self.assertEqual(lines, [{"type": "result", "done": True, "next_cursor": result["next_cursor"]}])
        self.assertEqual(self.s.json("/v1/fetch", {"stream": sidecar.STREAM, "mode": "incremental", "cursor": {"since": 1}, "credentials": creds})[1]["code"], "permanent")

    def test_scenarios(self):
        for user, status, code in [("synthetic-expired", 401, "reauth_required"), ("synthetic-limited", 429, "rate_limited"), ("synthetic-flaky", 503, "transient")]:
            got, headers, raw, _ = self.s.fetch(self.s.sign_in(user))
            p = json.loads(raw)
            self.assertEqual((got, p["code"]), (status, code), user)
            if code == "rate_limited":
                self.assertEqual((headers["Retry-After"], p["retry_after_s"]), ("7", 7))
        # Drift: a quarantined record, then the error line, in a 200 page.
        got, _, _, lines = self.s.fetch(self.s.sign_in("synthetic-drift"))
        self.assertEqual(got, 200)
        self.assertEqual([line["type"] for line in lines], ["raw", "error"])
        self.assertTrue(lines[0]["quarantine"])
        self.assertTrue(lines[1]["code"] == "schema_drift" and lines[1]["endpoint"] and lines[1]["fingerprint"])
        creds = self.s.sign_in("synthetic-rotate")
        *_, lines = self.s.fetch(creds)
        self.assertNotEqual(lines[-1]["credentials"]["access_token"], creds["access_token"])

    def test_sample_matches_go_golden(self):  # the Go normalizer's input is what this sidecar emits
        self.assertEqual(sidecar.sample(3), json.loads(GOLDEN.read_text()))


class BreakVariants(unittest.TestCase):
    """Each BREAK value breaks exactly the behaviour named in sidecar.BREAKS."""

    def run_break(self, brk):
        s = Server(brk)
        self.addCleanup(s.close)
        return s

    def test_every_break_is_covered(self):
        self.assertEqual(set(self.CHECKS), set(sidecar.BREAKS))

    def test_unknown_break(self):
        with self.assertRaises(ValueError):
            sidecar.make_server(("127.0.0.1", 0), SECRET, "nope")

    def test_breaks(self):
        for brk, check in self.CHECKS.items():
            with self.subTest(brk):
                check(self, self.run_break(brk))

    def check_bad_describe(self, s):
        self.assertEqual(s.json("/v1/describe")[1]["auth_kind"], "password")

    def check_bad_auth_step(self, s):
        step = s.json("/v1/auth/begin", {})[1]["step"]
        self.assertTrue("redirect_url" in step and "prompt" in step)

    def check_no_auth_check(self, s):
        self.assertEqual(s.call("/v1/describe", secret=b"wrong")[0], 200)

    def check_no_protocol_header(self, s):
        self.assertIsNone(s.call("/v1/describe")[1]["Vitamux-Protocol"])

    def check_no_result_line(self, s):
        self.assertTrue(all(line["type"] == "raw" for line in s.fetch(s.sign_in())[3]))

    def check_malformed_line(self, s):
        creds = s.sign_in()
        _, _, raw = s.call("/v1/fetch", {"stream": sidecar.STREAM, "mode": "incremental", "credentials": creds})
        with self.assertRaises(ValueError):
            [json.loads(line) for line in raw.decode().splitlines()]

    def check_bad_cursor(self, s):
        *_, lines = s.fetch(s.sign_in(), {"offset": 10})
        self.assertEqual((lines[-1]["done"], lines[-1]["next_cursor"]), (False, {"offset": 10}))

    def check_unstable_replay(self, s):
        creds = s.sign_in()
        self.assertGreater(len({s.fetch(creds)[2] for _ in range(5)}), 1)

    def check_no_rotation(self, s):
        creds = s.sign_in()
        self.assertEqual(s.json("/v1/auth/refresh", {"credentials": creds})[1]["credentials"], creds)

    def check_wrong_error_code(self, s):
        status, _, raw, _ = s.fetch(s.sign_in("synthetic-expired"))
        self.assertEqual((status, json.loads(raw)["code"]), (401, "transient"))

    def check_no_retry_after(self, s):
        status, headers, raw, _ = s.fetch(s.sign_in("synthetic-limited"))
        self.assertEqual((status, headers["Retry-After"], "retry_after_s" in json.loads(raw)), (429, None, False))

    def check_bad_drift(self, s):
        err = s.fetch(s.sign_in("synthetic-drift"))[3][-1]
        self.assertEqual((err["code"], "endpoint" in err, "fingerprint" in err), ("schema_drift", False, False))

    def check_leaks_secret(self, s):
        self.assertIn(SECRET, s.call("/v1/nope")[2])

    CHECKS = {
        "bad_describe": check_bad_describe, "bad_auth_step": check_bad_auth_step, "no_auth_check": check_no_auth_check,
        "no_protocol_header": check_no_protocol_header, "no_result_line": check_no_result_line, "malformed_line": check_malformed_line,
        "bad_cursor": check_bad_cursor, "unstable_replay": check_unstable_replay, "no_rotation": check_no_rotation,
        "wrong_error_code": check_wrong_error_code, "no_retry_after": check_no_retry_after, "bad_drift": check_bad_drift,
        "leaks_secret": check_leaks_secret,
    }


if __name__ == "__main__":
    unittest.main()
