# Security policy

Workshell receives data from Roblox game servers, and through them from players' clients, which exploiters control. Security reports are welcome and taken seriously.

## Reporting a vulnerability

**Please don't open a public issue.** Report privately through GitHub:

1. Go to the repository's [**Security** tab](https://github.com/Shellworks-Development/workshell/security).
2. Click **Report a vulnerability**.

Include what you can of:

- the affected component (server, dashboard, SDK, `workshell release`, `install.sh`) and version (`workshell version`),
- what an attacker needs: an ingest key, a dashboard account, a game client, or nothing,
- steps to reproduce, or a proof of concept,
- the impact as you understand it.

We'll acknowledge the report, keep you updated while we work on it, and credit you in the release notes when the fix ships, unless you'd rather stay anonymous. Please give us a reasonable chance to release a fix before you disclose the issue publicly.

## Supported versions

Fixes go into the latest release. Workshell is pre-1.0, so please upgrade to the newest release before reporting; older releases don't get backported fixes.

| Version | Supported |
| --- | --- |
| Latest `0.x` release | Yes |
| Older releases | No |

## Scope

In scope:

- **Ingest:** anything a game server or game client can send that bypasses validation, rate limits, request signing or replay protection, corrupts other projects' data, or crashes or stalls the server.
- **Dashboard:** cross-site scripting (including through data sent by a game), authentication or session flaws, access to another project's data, CSRF, or privilege escalation between users.
- **Secrets:** ingest keys, signing secrets, webhook secrets, GitHub tokens or the encryption key leaking through the API, logs or the dashboard.
- **Roblox erasure webhooks:** accepting a notification without a valid signature, or erasing the wrong player.
- **SDK:** a client being able to forge server-only data (detections, purchases, `$`-prefixed events) or impersonate another player.
- **Distribution:** `install.sh` and the release workflow, such as anything that lets a download skip its checksum check.

Out of scope:

- Problems that need an attacker to already control the Workshell host, its data directory, or an admin account.
- Volumetric denial of service against an instance (put it behind a proxy or CDN that rate-limits).
- Deployments that ignore the [hardening checklist](https://workshell.shellworks.dev/docs/self-hosting/deployment#hardening-checklist), such as serving the dashboard over plain HTTP.
- Missing security headers on the marketing site, and reports from automated scanners without a demonstrated impact.

## Security model

In short: everything from a game client is untrusted, everything a game sends is rendered as text, and secrets are encrypted at rest. For the details (signed requests, flood limits, quarantined client errors, the Content-Security-Policy, pseudonymous player ids and data erasure), see [Security and exploits](https://workshell.shellworks.dev/docs/features/security) and [Privacy and data erasure](https://workshell.shellworks.dev/docs/features/privacy).

## Verifying releases

Every release includes `SHA256SUMS`. `install.sh` checks the download against it before installing. To check a manual download:

```sh
sha256sum --ignore-missing -c SHA256SUMS
```
