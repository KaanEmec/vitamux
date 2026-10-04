# Upstream: <name>

Read by the license gate (`go run ./tools/notices -check`), Dependabot automation and humans. Keep
the `- Field: value` lines; every field below except the optional ones must be filled in.
Rules: [docs/sidecars.md#license-gate](../../docs/sidecars.md#license-gate).

- Repository: https://github.com/<owner>/<repo>
- Package: <pypi or npm package name>
- Lockstep packages: <other packages released together with it, comma separated; optional>
- Canary source: <git+https://github.com/<owner>/<repo>[#subdirectory=...]; optional, default git+Repository>
- Version: <locked version, as in the lockfile>
- Release tag: <upstream git tag of that version>
- License: <SPDX expression, e.g. MIT>
- Status: <official | unofficial>
- License review: <not needed for a permissive allowlisted license; otherwise "reviewed YYYY-MM-DD by <who>: <outcome and why a separate container is acceptable>">

## Notes

What this wrapper uses from the upstream, which endpoints are unofficial, and what the upstream
does when the provider changes shape.
