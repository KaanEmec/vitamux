# Upstream: garminconnect

Read by the license gate (`go run ./tools/notices -check`), Dependabot automation and humans.
Rules: [docs/sidecars.md#license-gate](../../docs/sidecars.md#license-gate).

- Repository: https://github.com/cyberjunky/python-garminconnect
- Package: garminconnect
- Version: 0.3.17
- Release tag: 0.3.17
- License: MIT
- Status: unofficial
- License review: not needed for the upstream itself. Its dependency tree is permissive too: `curl_cffi` MIT, `requests` Apache-2.0, `ua-generator` Apache-2.0, `cffi` MIT-0, `urllib3` MIT, `certifi` MPL-2.0 (unmodified, file-level copyleft), `charset-normalizer` MIT, `idna` and `pycparser` BSD-3-Clause

## Notes

- `garminconnect` on PyPI, pinned exactly in `pyproject.toml`; `uv.lock` pins the hashes.
- **Unofficial.** It signs in through Garmin's mobile SSO flow and calls the Garmin Connect web services; Garmin has no public API for personal data without a developer program. A new connection starts paused.
- Active: 17 releases from 0.3.0 (2026-04-02) to 0.3.17 (2026-09-29). Dependabot checks `sidecars/garmin` daily with a 1-day cooldown; a failing bump stays open and the last good image stays `stable`. The nightly canary builds against the upstream default branch.

What the sidecar touches:

- Public: `Garmin(email, password, return_on_mfa=True, retry_attempts=0)`, `Garmin.login()`, `Garmin.resume_login()`, `Garmin.connectapi()`, `Garmin.download()`, `Client.dumps()`, `Client.loads()`, the exception classes, and the endpoint path attributes (`g.garmin_connect_*`).
- Private, so first to check on a bump: `Client._refresh_di_token()` (forced token refresh; there is no public one) and the `API Error NNN` and `DI token refresh failed: NNN` message formats that `src/garmin.py` parses to tell 429 and 5xx from other failures.
- Never used: the library's reshaping helpers (`get_heart_rates` and others), which raise on empty days or edit values. Raw is the endpoint's JSON as `connectapi` returns it.
