"""REPLAY=1: answer from a synthetic cassette (testdata/replay.json) instead of calling Garmin.

The real wrapper runs unchanged: only the library's network calls are replaced. A cassette entry
matches when its "path" is a prefix of the requested path and, if it names a "token", the access
token is that one, so the conformance scenario's synthetic tokens can provoke each typed error.
The first match wins; an unrecorded path is a 404 (permanent).
"""

import json
from datetime import datetime

from garminconnect import (
    Garmin,
    GarminConnectAuthenticationError,
    GarminConnectConnectionError,
    GarminConnectNotFoundError,
    GarminConnectTooManyRequestsError,
)

ERRORS = {
    "reauth_required": lambda: GarminConnectAuthenticationError("synthetic"),
    "rate_limited": lambda: GarminConnectTooManyRequestsError("synthetic"),
    "transient": lambda: GarminConnectConnectionError("API Error 503"),
}
ROTATING = "synthetic-rotate"  # its first call "refreshes" the token, as the library does when one nears expiry
FRESH = "synthetic-fresh-access"


def install(garmin, path):
    cassette = json.loads(path.read_text())

    class Replay(Garmin):
        def __init__(self, *a, **kw):
            super().__init__(*a, **kw)
            self.client._refresh_di_token = self._refresh

        def _refresh(self):
            self.client.di_token = FRESH

        def connectapi(self, path, **kw):
            if self.client.di_token == ROTATING:
                self._refresh()
            for e in cassette["responses"]:
                if path.startswith(e["path"]) and e.get("token", self.client.di_token) == self.client.di_token:
                    if "error" in e:
                        raise ERRORS[e["error"]]()
                    return e["body"]
            raise GarminConnectNotFoundError("API Error 404")

        def download(self, path, **kw):
            raise GarminConnectNotFoundError("API Error 404")

    garmin.Garmin = Replay
    garmin.DELAY = 0
    garmin.NOW = lambda: datetime.fromisoformat(cassette["now"])
    garmin.DEFAULT_EXTRA = {"di_client_id": "synthetic-client", "display_name": "synthetic-profile"}
