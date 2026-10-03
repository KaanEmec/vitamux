# E03 Security foundation: secrets, auth, tokens

Release: MVP · Depends on: E02 · [Plan index](../README.md)
Read first: [security#keys-and-secrets](../../architecture/security.md#keys-and-secrets), [security#threat-model](../../architecture/security.md#threat-model), [api#conventions](../../architecture/api.md#conventions)

**Objective:** Secrets, owner auth, scoped tokens, redaction and a hardened HTTP baseline exist before any credential is stored.

**Outputs:** Vault and master key, owner login with TOTP, API keys and client tokens, redacting logger, safe HTTP client, HTTP baseline, audit helper.

## Acceptance
- Vault round-trips with tamper detection.
- Every non-public route rejects anonymous requests.
- Tokens are hashed at rest.
- Log capture over auth flows contains no sentinel secrets.

## Jobs
| Job | Title | Depends on | Gate |
| --- | --- | --- | --- |
| [J03.1](J03.1-vault.md) | Master key and vault (ADR-011) | J02.3 | None |
| [J03.2](J03.2-owner-auth.md) | Owner account, sessions, TOTP | J02.2, J03.4 | None |
| [J03.3](J03.3-tokens.md) | API keys and client tokens | J02.2, J02.3 | None |
| [J03.4](J03.4-redaction-audit.md) | Redaction, safe HTTP client, audit helper | J01.2, J02.2 | None |
| [J03.5](J03.5-http-baseline.md) | HTTP server baseline | J01.2 | None |
