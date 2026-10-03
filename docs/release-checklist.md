# Release checklist

How a version ships. Policy: [project#versioning-and-releases](architecture/project.md#versioning-and-releases). Automation: [`.github/workflows/release.yml`](../.github/workflows/release.yml) runs on every `v*` tag pushed from `main`.

1. **Prepare.** `main` is green (CI and the nightly fuzz run). [`KNOWN_LIMITATIONS.md`](release-notes/KNOWN_LIMITATIONS.md) is current. First public release only: scrub `docs/architecture/migration-reference.md` and its history (J14.4 T14.4.5) and make the GHCR package public after step 3.
2. **Tag a release candidate.** `git tag -a v0.1.0-rc.1 -m v0.1.0-rc.1 && git push origin v0.1.0-rc.1`.
3. **Check the artifacts** on the GitHub pre-release:
   - image `ghcr.io/kaanemec/vitamux:vX.Y.Z-rc.N` for linux/amd64 and linux/arm64 (`docker buildx imagetools inspect`), with no `vX.Y` or `latest` move;
   - one SPDX SBOM per platform, `vitamux-compose-vX.Y.Z-rc.N.tar.gz`, `checksums.txt` (plus `checksums.txt.sigstore.json` when signing is on);
   - notes with the commits since the last final release and Known limitations;
   - the `upgrade-test` job is green (previous release, or the `main` merge-base before the first one, upgraded with data). Locally: `make upgrade-test OLD_IMAGE=… NEW_IMAGE=…`.
   - `sha256sum -c checksums.txt` and the `cosign` checks in [security#supply-chain](security.md#supply-chain) pass.
4. **Clean installs, from the docs only.** Unpack the rc bundle and follow its `docs/install.md` and nothing else, with no prior state, on:
   - a fresh Linux VM (amd64);
   - macOS (Docker Desktop or Colima, arm64);
   - a fresh Coolify instance (not the reference VPS).

   Each install: stack up, owner created, sign-in with TOTP, `/readyz` ready, one backup taken, then an upgrade to the next rc if there is one. Write down every friction point as you go.
5. **Fix the friction.** Each point becomes a docs or code fix, or a line in `KNOWN_LIMITATIONS.md`. A code or Compose change means a new rc: back to step 2.
6. **Changelog.** `make changelog TAG=vX.Y.Z`, edit the section (upgrade notes go under "Breaking changes"), commit it as `chore(release): vX.Y.Z`.
7. **Tag the final release.** `git tag -a vX.Y.Z -m vX.Y.Z && git push origin vX.Y.Z`. The workflow refuses a final tag without its CHANGELOG section, uses that section as the notes, and moves `vX.Y` and `latest` (only for the highest release).
8. **After.** The release page has every file, `docker pull ghcr.io/kaanemec/vitamux:vX.Y.Z` works without signing in, and the plan job is updated.

Signing is on by default (keyless, GitHub OIDC). Set the repository variable `VITAMUX_SIGN_RELEASES` to `false` to skip it.
