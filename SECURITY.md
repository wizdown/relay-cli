# Security

## Reporting a vulnerability

Do not open a public issue. Use GitHub's private vulnerability reporting:
the **Security** tab → **Report a vulnerability**. Include what you did, what
happened, and what you expected. If a credential is involved, describe its
shape rather than pasting it.

Expect an acknowledgement within a few days. This is a small project without a
formal SLA.

## Credentials

Every Relay connector URL is a live credential. The secret is part of the URL,
and Relay shows it once:

```
https://relay.example.com/relay/mcp/c/wzh_<secret>
```

Anyone holding the URL can act as that agent.

| File | Contains | Protection |
| --- | --- | --- |
| `~/.relay/config` | every worker's connector URL | written `0600` in a `0700` directory, outside every checkout |
| `~/.relay/state/<name>/mcp.json` | one connector URL, generated at launch | `0600`, deleted on shutdown |
| `~/.relay/logs/`, `~/.relay/state/` | session output | outside every checkout |

Never commit a relay-cli config. `.relay/` and `.worker-config` are gitignored
at any path and refused by the pre-commit hook. In anything you commit, in
commit messages, and in pull requests, use `relay.example.com` and
`wzh_REPLACE_ME`, and describe a failure as "HTTP 401 from the configured
endpoint" rather than quoting the URL.

If a credential leaks, revoke it in Relay and issue a new one.

## What a worker can do

- A headless run is fully autonomous inside `repo_dir`. It cannot answer an
  approval prompt, so point a worker at a checkout you are willing to have
  rewritten.
- Session output, and the archived logs under `~/.relay/logs/`, can contain
  anything the agent read.
- Spend is bounded by default (12 runs/hour, $5 per run on claude, a 15-minute
  kill). Each bound can be set to `0` to remove it.

## The dashboard

`relay run` serves the dashboard on `127.0.0.1` only. It is read-only: no
route pauses a worker, starts a run, or edits a ceiling. Connector secrets are
redacted server-side, so the raw URL never appears in an HTTP response, a log
line, or an error. If you find a path where a secret reaches the page, a log,
or a terminal, report it as a vulnerability.

## Supported versions

The project is 0.x (see [Versioning](docs/cli.md#versioning)). Fixes land on
`master`, and the supported version is the latest release.
