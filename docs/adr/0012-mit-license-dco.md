# ADR-0012 MIT license with DCO sign-off

Status: Accepted · Date: 2026-10-03 · Deciders: owner

## Context
Vitamux is open source and self-hosted. Planned dependencies are permissively licensed. See [project.md › License and notices](../architecture/project.md#license-and-notices).

## Decision
License the core, the Swift package, and the iOS app under **MIT**. Contributors certify their work with the Developer Certificate of Origin (`Signed-off-by:` trailer, checked in CI). There is no CLA. Third-party notices are generated into `THIRD_PARTY_NOTICES.md` and copied into images at `/licenses`.

## Alternatives considered
- **Apache-2.0**: adds an explicit patent grant, but the owner chose the simpler MIT.
- **AGPL-3.0**: forces network-service modifications to be shared, but deters adoption and contribution.

## Consequences
- No patent grant from contributors.
- The license CI gate (J14.1) flags any copyleft dependency for review.
