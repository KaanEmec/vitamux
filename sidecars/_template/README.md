# Sidecar template

Copy this directory to `sidecars/<name>/` and follow [docs/sidecars.md](../../docs/sidecars.md#wrap-a-library-as-a-sidecar).
`<name>` is the provider code (`^[a-z][a-z0-9_]*$` in the core's settings; the directory name may use `-`
instead of `_`). The template is skipped by the license gate and by the sidecar workflows.

| File | What you fill in |
| --- | --- |
| `Dockerfile` | Base image digests, upstream license label. Stages `build`, `test` and the final image. |
| `UPSTREAM.md` | Repo, package, locked version and tag, license, official or unofficial, license review. |
| `compose.yaml` | Replace `<name>`; keep the limits, read-only root, healthcheck and `frontend`-only network. |
| `pyproject.toml` + `uv.lock` (or `package.json` + `package-lock.json`) | Not in the template: create them with the upstream package as a dependency and commit the lockfile. |
| `src/`, `tests/`, `testdata/` | Your wrapper, unit tests, and synthetic recorded upstream responses (cassettes, `synthetic: true`). |

Then add the directory's entry to `.github/dependabot.yml` (see the commented template there).
