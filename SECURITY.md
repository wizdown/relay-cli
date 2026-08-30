# Security

## Reporting a vulnerability

Please **do not open a public issue** for a security problem.

Use GitHub's private vulnerability reporting — the **Security** tab → **Report a
vulnerability** — which opens a private advisory visible only to the maintainers.

Please include what you did, what happened, and what you expected. If it involves
a credential, redact it: describe the shape rather than pasting the value.

Expect an acknowledgement within a few days. This is a small project without a
formal SLA.

## Credentials, and why this matters here

**Every relay connector URL is a live credential.** The secret is embedded in the
URL itself and is shown exactly once, when the credential is issued:

```
https://relay.example.com/relay/mcp/c/wzh_<secret>
```

Anything holding one can act as that agent. Concretely, that means:

| File | Contains | Protection |
| --- | --- | --- |
| `~/.relay/config` | every worker's endpoint | written `0600`, in a `0700` directory, outside every checkout |
| `~/.relay/state/<name>/mcp.json` | one endpoint, generated at launch | written `0600`, deleted on shutdown |
| `~/.relay/logs/`, `~/.relay/state/` | session output | outside every checkout |

The config lives in `~/.relay/`, so it is never inside a repository and cannot be
committed by accident. A copy made in a checkout to test with is exactly as
dangerous, so `.relay/` and `.worker-config` are both matched by the `.gitignore`
at any path and refused by the pre-commit hook — the moment to catch that is
before the commit rather than after the revocation.

**If a credential leaks, revoke it in relay and issue a new one.** Rotation is the
fix; the whole URL changes.

Use `relay.example.com` and an obvious placeholder (`wzh_REPLACE_ME`) in anything
you commit, tests included — and in commit messages, pull request titles and
bodies, and release notes. Those are as public as the code and outlive the branch
they were written on, so redact the URL and describe the shape instead: "HTTP 401
from the configured endpoint" says everything the value would.

## What a worker can do

Be deliberate about this before you point one at a checkout:

- **A headless run is fully autonomous inside `repo_dir`.** It cannot answer an
  approval prompt, so it runs with permissions pre-granted and anything not
  pre-allowed is silently denied. Point a worker at a checkout you are willing to
  have rewritten, not at your only copy of anything.
- **Session output can contain anything the agent read**, including file contents
  from that checkout. Archived logs under `logs/` inherit that.
- **Spend is bounded by default** — 12 runs/hour, $5 per run, a 15-minute kill —
  and each of those can be set to `0` to remove the bound. That is a deliberate
  choice, not a default you can drift into.

## What the dashboard exposes

`relay run` serves a page on `127.0.0.1` only. No flag changes that.

It is **read-only**: there is no route that pauses a worker, starts a run, or
edits a ceiling — not a hidden one, not a disabled button. A page that can spend
money is a different thing to reason about than a page that can only show what
already happened.

Connector secrets are redacted **server-side**. The raw endpoint is never in an
HTTP response, and every log line, event and error is scrubbed on the way out. If
you find a path where a secret reaches the page, an archived log, or a terminal,
that is a vulnerability — please report it.

## Supported versions

This project is **0.x** and will stay there until the interface is settled — see
[Versioning](readme.md#versioning). Fixes land on `master`; there are no
backported release branches yet, so the supported version is the latest one.
