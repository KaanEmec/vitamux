"""Garmin Connect logic: sign-in, token refresh and the raw fetch of every stream.

No HTTP in here. server.py renders Failure as problem+json and the items that
fetch() yields as NDJSON lines. Raw means the endpoint's JSON verbatim, called
through the library's generic connectapi/download, never its reshaping helpers.
"""

import base64
import hashlib
import json
import logging
import os
import re
import secrets
import threading
import time
from datetime import UTC, date, datetime, timedelta
from importlib.metadata import version as _pkg_version
from typing import NamedTuple
from urllib.parse import quote
from zoneinfo import ZoneInfo

from garminconnect import (
    Garmin,
    GarminConnectAuthenticationError,
    GarminConnectConnectionError,
    GarminConnectNotFoundError,
    GarminConnectTooManyRequestsError,
)

log = logging.getLogger("vitamux.garmin")

VERSION = "0.1.0"
NOW = lambda: datetime.now(UTC)  # replaced under REPLAY=1 (src/replay.py)
DEFAULT_EXTRA = {}  # likewise: the profile fields a placeholder credential lacks
INITIAL_DAYS = 2  # a stream without a cursor starts this many days back; history is a backfill

DELAY = float(os.environ.get("VITAMUX_GARMIN_CALL_DELAY_S", "1"))  # between Garmin calls
MFA_TTL = 600  # seconds a pending MFA login is kept in memory
MAX_PENDING = 16
MAX_UNITS = 31  # units (days or 30-day blocks) per fetch call; the rest goes in next_cursor
BLOCK = 30  # days per range-stream unit
PAGE = 20  # activities per search page, as the Garmin Connect web app

PROFILE = "/userprofile-service/socialProfile"  # the library's own profile call


class Failure(Exception):
    """A typed connector error. detail is sidecar-authored, never library text.
    endpoint and fingerprint (a hash of the unexpected shape) belong to schema_drift."""

    def __init__(self, code, detail="", retry_after_s=None, endpoint=None, fingerprint=None):
        super().__init__(code)
        self.code, self.detail, self.retry_after_s = code, detail, retry_after_s
        self.endpoint, self.fingerprint = endpoint, fingerprint


def _shape(v):
    if isinstance(v, dict):
        return {k: _shape(v[k]) for k in sorted(v)}
    if isinstance(v, list):
        return [_shape(v[0])] if v else []
    return "null" if v is None else type(v).__name__


def fingerprint(v):
    """Hash of a JSON value's shape (keys and kinds, never values)."""
    return hashlib.sha256(json.dumps(_shape(v)).encode()).hexdigest()


# name: (interval, lookback, unit_size, max_backfill) in hours. Conservative guesses, see docs/providers/garmin.md.
_DAY = (1, 72, 24, 43800)
STREAMS = {
    "garmin.daily_summary": _DAY,
    "garmin.heart_rate": _DAY,
    "garmin.steps": _DAY,
    "garmin.stress_body_battery": _DAY,
    "garmin.sleep": _DAY,
    "garmin.hrv": _DAY,
    "garmin.respiration": _DAY,
    "garmin.spo2": _DAY,
    "garmin.training": (6, 168, 24, 43800),
    "garmin.body_composition": (6, 168, 720, 43800),
    "garmin.blood_pressure": (6, 168, 720, 43800),
    "garmin.activities": (2, 72, 720, 43800),
}
RANGE_STREAMS = {"garmin.body_composition", "garmin.blood_pressure"}


def describe():
    return {
        "provider": "garmin",
        "name": "Garmin Connect (unofficial)",
        "version": VERSION,
        "official": False,
        "auth_kind": "interactive_mfa",
        "upstream": {
            "package": "garminconnect",
            "version": _pkg_version("garminconnect"),
            "source_url": "https://github.com/cyberjunky/python-garminconnect",
        },
        "streams": [{"name": n, **{k: h * 3600 for k, h in zip(
                        ("interval_s", "lookback_s", "unit_size_s", "max_backfill_s"), s)}}
                    for n, s in STREAMS.items()],
        "rate_limits": [{"requests": 2, "per_s": 4}],  # one request per 2 s, bursts of 2
        "capabilities": {"incremental": True, "backfill": True, "manual_sync": True},
    }


# ---- errors ---------------------------------------------------------------


def _status(e):
    """HTTP status behind a library exception, or None."""
    if isinstance(e, GarminConnectAuthenticationError):
        m = re.fullmatch(r"DI token refresh failed: (\d{3})", str(e))  # refresh
    else:
        s = getattr(getattr(e, "response", None), "status_code", None)
        if isinstance(s, int):
            return s
        m = re.search(r"API Error (\d{3})", str(e))
    return int(m[1]) if m else None


def _retry_after(e):
    headers = getattr(getattr(e, "response", None), "headers", None) or {}
    try:
        return max(1, int(headers.get("Retry-After", 300)))
    except (TypeError, ValueError):
        return 300


def _causes(e):
    while e is not None:
        yield e
        e = e.__cause__ or e.__context__


def classify(e):
    """Map any library or network exception to a Failure."""
    if isinstance(e, Failure):
        return e
    status = _status(e)
    if isinstance(e, GarminConnectTooManyRequestsError) or status == 429:
        return Failure("rate_limited", retry_after_s=_retry_after(e))
    if status is not None and status >= 500:
        return Failure("transient")
    if isinstance(e, GarminConnectAuthenticationError) or status == 401:
        return Failure("reauth_required")
    chain = list(_causes(e))
    if any(isinstance(c, ValueError) for c in chain):  # the body was not JSON
        return Failure("schema_drift", "response is not valid JSON")
    if any(isinstance(c, OSError) for c in chain) or (
        isinstance(e, GarminConnectConnectionError) and status is None
    ):
        return Failure("transient")
    return Failure("permanent")


# ---- credentials ----------------------------------------------------------
# Credentials are {access_token, refresh_token, extra}: the library's DI tokens, and in extra its
# client id plus the profile ids, so a fetch needs no extra profile calls.


def _creds(g, extra):
    d = json.loads(g.client.dumps())
    return {"access_token": d["di_token"], "refresh_token": d["di_refresh_token"],
            "extra": {**extra, "di_client_id": d["di_client_id"]}}


def _client(credentials):
    c = credentials if isinstance(credentials, dict) else {}
    extra = {**DEFAULT_EXTRA, **(c.get("extra") if isinstance(c.get("extra"), dict) else {})}
    needed = (c.get("access_token"), c.get("refresh_token"), extra.get("di_client_id"), extra.get("display_name"))
    if not all(isinstance(v, str) and v for v in needed):
        raise Failure("reauth_required", "credentials are unreadable")
    g = Garmin(retry_attempts=0)  # the core owns retries and backoff
    g.client.loads(json.dumps({"di_token": c["access_token"], "di_refresh_token": c["refresh_token"],
                               "di_client_id": extra["di_client_id"]}))
    g.display_name = extra["display_name"]
    return g, extra


# ---- sign-in --------------------------------------------------------------
# A pending MFA login lives on the Garmin client (HTTP session, cookies, CSRF
# state) and cannot be serialized, so it stays in memory for MFA_TTL seconds and
# the opaque `session` is only its key. A restart of the sidecar in between
# means the user signs in again.

_pending = {}  # session id -> (deadline, Garmin)
_lock = threading.Lock()


def _field(name, label, kind):
    return {"name": name, "label": label, "kind": kind}


def _done(g):
    try:
        profile = g.connectapi(PROFILE)
    except Exception as e:
        raise classify(e) from None
    name = profile.get("displayName") if isinstance(profile, dict) else None
    pid = profile.get("profileId") if isinstance(profile, dict) else None
    c = g.client
    if not (name and isinstance(pid, int)):
        raise Failure("schema_drift", "profile has no displayName or profileId",
                      endpoint=PROFILE, fingerprint=fingerprint(profile))
    if not (c.di_token and c.di_refresh_token and c.di_client_id):
        raise Failure("permanent", "Garmin issued a session the sidecar cannot persist")
    return {"authorized": {"account_id": str(pid),
                           "credentials": _creds(g, {"display_name": name, "profile_id": pid})}}


def begin():
    """The first step asks for the sign-in; it does not call Garmin."""
    return {"step": {"prompt": {
        "message": "Sign in to Garmin Connect (unofficial access with your own account).",
        "fields": [_field("username", "Email", "text"), _field("password", "Password", "password")]}}}


def login(username, password):
    g = Garmin(username, password, return_on_mfa=True, retry_attempts=0)
    try:
        needs_mfa, _ = g.login()
    except Exception as e:
        raise classify(e) from None
    finally:
        g.password = None
    if needs_mfa != "needs_mfa":
        return _done(g)
    now = time.monotonic()
    sid = secrets.token_bytes(32)
    with _lock:
        for k in [k for k, (t, _) in _pending.items() if t < now]:
            del _pending[k]
        while len(_pending) >= MAX_PENDING:
            del _pending[next(iter(_pending))]
        _pending[sid] = (now + MFA_TTL, g)
    return {"step": {"prompt": {"message": "Enter the verification code Garmin sent you.",
                                "fields": [_field("code", "Verification code", "code")]},
                     "session": base64.b64encode(sid).decode()}}


def cont(session, code):
    try:
        sid = base64.b64decode(session, validate=True)
    except (TypeError, ValueError):
        sid = b""
    with _lock:
        entry = _pending.get(sid)
        if entry and entry[0] < time.monotonic():
            del _pending[sid]
            entry = None
    if not entry:
        raise Failure("reauth_required", "MFA session expired or unknown, sign in again")
    g = entry[1]
    try:  # a wrong code keeps the session so the user can retry within the TTL
        g.resume_login(None, code)
    except Exception as e:
        raise classify(e) from None
    with _lock:
        _pending.pop(sid, None)
    return _done(g)


def refresh(credentials):
    g, extra = _client(credentials)
    try:
        g.client._refresh_di_token()  # upstream-private: no public forced refresh
    except Exception as e:
        raise classify(e) from None
    return {"credentials": _creds(g, extra)}


# ---- fetch ----------------------------------------------------------------


class Result(NamedTuple):
    next_cursor: object
    done: bool
    high_watermark: str | None
    credentials: str | None


def _when(s, tz):
    d = datetime.fromisoformat(s)
    return d.replace(tzinfo=tz) if d.tzinfo is None else d.astimezone(tz)


def _get(g, path, params, types):
    """One verbatim GET. An empty body (HTTP 204 or null) is the day's raw too."""
    try:
        resp = g.connectapi(path, params=params)
    except Exception as e:
        f = classify(e)
        if f.code == "schema_drift":
            f.endpoint, f.fingerprint = path, fingerprint(None)
        raise f from None
    time.sleep(DELAY)
    if resp not in (None, {}, []) and not isinstance(resp, types):
        raise Failure("schema_drift", "unexpected top-level JSON type", endpoint=path, fingerprint=fingerprint(resp))
    return resp


def _item(key, path, params, body):
    return {"external_key": key, "request": {"endpoint": path, "params": params}, "body": body}


def _calls(name, g, a, b=None):
    """(key suffix, path, params, accepted types) of a unit starting at ISO date a.
    Range streams span a..b inclusive."""
    dn = quote(g.display_name, safe="")
    match name:
        case "garmin.daily_summary":
            return [("", f"{g.garmin_connect_daily_summary_url}/{dn}", {"calendarDate": a}, (dict,))]
        case "garmin.heart_rate":
            return [("", f"{g.garmin_connect_heartrates_daily_url}/{dn}", {"date": a}, (dict,))]
        case "garmin.steps":
            return [("", f"{g.garmin_connect_user_summary_chart}/{dn}", {"date": a}, (list,))]
        case "garmin.stress_body_battery":
            return [("", f"{g.garmin_connect_daily_stress_url}/{a}", {}, (dict,))]
        case "garmin.sleep":
            return [("", f"{g.garmin_connect_daily_sleep_url}/{dn}",
                     {"date": a, "nonSleepBufferMinutes": 60}, (dict,))]
        case "garmin.hrv":
            return [("", f"{g.garmin_connect_hrv_url}/{a}", {}, (dict,))]
        case "garmin.respiration":
            return [("", f"{g.garmin_connect_daily_respiration_url}/{a}", {}, (dict,))]
        case "garmin.spo2":
            return [("", f"{g.garmin_connect_daily_spo2_url}/{a}", {}, (dict,))]
        case "garmin.training":
            return [(":vo2max", f"{g.garmin_connect_metrics_url}/{a}/{a}", {}, (dict, list)),
                    (":readiness", f"{g.garmin_connect_training_readiness_url}/{a}", {}, (list,))]
        case "garmin.body_composition":
            return [("", f"{g.garmin_connect_weight_url}/weight/dateRange",
                     {"startDate": a, "endDate": b}, (dict,))]
        case "garmin.blood_pressure":
            return [("", f"{g.garmin_connect_blood_pressure_endpoint}/{a}/{b}",
                     {"includeAll": True}, (dict,))]


def _unit_items(g, name, start):
    """Raw items of one unit: a day, or a 30-day block (aligned, so keys stay stable)."""
    a = start.isoformat()
    b = (start + timedelta(BLOCK - 1)).isoformat() if name in RANGE_STREAMS else None
    unit = {"date": a} if b is None else {"start": a, "end": b}
    key = f"{name}:{a}" if b is None else f"{name}:{a}_{b}"
    for suffix, path, params, types in _calls(name, g, a, b):
        yield _item(key + suffix, path, params, {"unit": unit, "response": _get(g, path, params, types)})


def _activity_items(g, activity, search):
    aid = activity.get("activityId") if isinstance(activity, dict) else None
    if not isinstance(aid, int):
        raise Failure("schema_drift", "activity without an integer activityId",
                      endpoint=g.garmin_connect_activities, fingerprint=fingerprint(activity))
    key = f"garmin.activities:{aid}"
    yield _item(key, g.garmin_connect_activities, search, {"unit": {"activity_id": aid}, "response": activity})
    path = f"{g.garmin_connect_fit_download}/{aid}"
    try:
        data = g.download(path)  # the original FIT, zipped
    except GarminConnectNotFoundError:
        return  # manual activities have no file
    time.sleep(DELAY)
    yield {"external_key": key + ":fit", "request": {"endpoint": path, "params": {}},
           "content_type": "application/zip", "data": data}


def _unit_steps(g, name, first, last):
    """(cursor after the step, producer) per day or block, at most MAX_UNITS."""
    if last < first:
        return []
    if name in RANGE_STREAMS:
        o = first.toordinal()
        units = [date.fromordinal(x) for x in range(o - o % BLOCK, last.toordinal() + 1, BLOCK)]
    else:
        units = [first + timedelta(i) for i in range((last - first).days + 1)]
    nxt = lambda i: {"day": units[i + 1].isoformat()} if i + 1 < len(units) else None
    return [(nxt(i), lambda u=u: _unit_items(g, name, u)) for i, u in enumerate(units[:MAX_UNITS])]


def _activity_steps(g, first, last, cursor):
    """(cursor after the activity, producer) for one page of the activity search."""
    offset = cursor.get("offset", 0)
    if last < first:
        return []
    params = {"startDate": first.isoformat(), "endDate": last.isoformat(),
              "start": str(offset), "limit": str(PAGE), "sortOrder": "asc"}
    page = _get(g, g.garmin_connect_activities, params, (list,)) or []
    more = len(page) == PAGE
    return [({"offset": offset + i + 1} if more or i + 1 < len(page) else None,
             lambda a=a: _activity_items(g, a, params)) for i, a in enumerate(page)]


def _rotated(g, extra, credentials):
    """The credentials as they are now, when the library refreshed them during the call."""
    out = _creds(g, extra)
    same = (out["access_token"], out["refresh_token"]) == (credentials["access_token"], credentials["refresh_token"])
    return None if same else out


def _watermark(tz, last, next_cursor):
    nxt = date.fromisoformat(next_cursor["day"]) if next_cursor else last + timedelta(1)
    return min(datetime.combine(nxt, datetime.min.time(), tz), NOW()).isoformat()


def _window(req, tz):
    """First and last day to fetch, and the incremental start. Correction and backfill send
    from/to ([from, to)); an incremental or manual run sends only the stored cursor {since},
    and runs from that day (default: INITIAL_DAYS back) through today."""
    cursor = req.get("cursor") or {}
    if not isinstance(cursor, dict) or not isinstance(cursor.get("offset", 0), int):
        raise ValueError
    if req.get("from"):
        start, end = _when(req["from"], tz), _when(req["to"], tz)
        return start.date(), (end - timedelta(microseconds=1)).date(), cursor, None
    today = NOW().astimezone(tz).date()
    since = date.fromisoformat(cursor["since"]) if "since" in cursor else today - timedelta(INITIAL_DAYS)
    return since, today, cursor, since


def fetch(req):
    """Yield raw items, then one Result. A Failure before the first item is raised;
    one after progress ends the call early with a cursor to resume from.
    Cursors: a stream cursor is {"since": day}; a page cursor adds {"day": next unit}
    or {"offset": n} (activities), and for a window run is only that part."""
    name = req.get("stream")
    if name not in STREAMS:
        raise Failure("permanent", "unknown stream")
    try:
        tz = ZoneInfo((req.get("config") or {}).get("timezone") or "UTC")
        first, last, cursor, since = _window(req, tz)
        if "day" in cursor and name != "garmin.activities":
            first = max(first, date.fromisoformat(cursor["day"]))
    except (KeyError, TypeError, ValueError, OSError, AttributeError):
        raise Failure("permanent", "bad from, to, timezone or cursor") from None
    credentials = req.get("credentials")
    g, extra = _client(credentials)

    next_cursor, progressed = None, False
    try:
        steps = (_activity_steps(g, first, last, cursor) if name == "garmin.activities"
                 else _unit_steps(g, name, first, last))
        for after, produce in steps:
            try:
                items = list(produce())
            except Exception as e:
                if not progressed:
                    raise
                log.warning("fetch stopped early: %s", classify(e).code)
                break  # keep what we have; the next call resumes at next_cursor
            yield from items
            progressed, next_cursor = True, after
    except Exception as e:
        raise classify(e) from None
    done = next_cursor is None
    if done:  # a finished incremental run stores {since: last day}, a window run leaves the cursor alone
        out = {"since": last.isoformat()} if since and first <= last else None
    else:
        out = {**({"since": since.isoformat()} if since else {}), **next_cursor}
    mark = None if last < first or name == "garmin.activities" else _watermark(tz, last, next_cursor)
    yield Result(out, done, mark, _rotated(g, extra, credentials))
