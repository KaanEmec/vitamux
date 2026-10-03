# Security policy

Vitamux stores personal health data and provider credentials, so security reports are taken seriously.

## Reporting a vulnerability

- **Do not open a public issue.** Use GitHub's private vulnerability reporting (**Security → Report a vulnerability**) on this repository.
- Include the affected version or commit, reproduction steps, and impact. Never include real health data or real credentials; use synthetic data.
- Expect an acknowledgement within 7 days. Fixes are released before details are published.

## Supported versions

Before 1.0, only the latest release receives security fixes.

## Scope

In scope: the `vitamux` server, the official sidecars and images, the HealthBridge Swift package and app, and the reference Compose files. The security model (assets, actors, controls, residual risks) is in [`docs/security.md`](docs/security.md); the design details are in [`docs/architecture/security.md`](docs/architecture/security.md).
