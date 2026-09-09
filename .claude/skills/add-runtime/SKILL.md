---
name: add-runtime
description: Use when wiring in a new coding CLI as a runtime, or changing an existing adapter's check, argv, line parsing or exit classification. Runtime-specific behaviour belongs in an adapter, never in the worker loop.
---

# Adding or changing an adapter

The contract is [Adapters](../../../docs/contributing/adapters.md). The reasons
native is the only shape that ships are in
[Design](../../../docs/contributing/design.md). Copy `runtime_codex.go`, the
more recent of the two.

## The six methods

1. **`ConfigFields()`** every setting the CLI takes, typed, defaulted and
   documented in one table. Use the `config-field` skill for the docs each key
   then owes.
2. **`Check()`** installed, accepts the flags this adapter uses, signed in.
   Read the CLI's own `--help` rather than gating on a version, and ask about
   sign-in in a way that spends nothing. Signed in passes, signed out fails the
   start, and a CLI that cannot be asked warns and continues.
3. **`BuildCmd()`** the exact argv, with whatever spells "fully autonomous" set
   unconditionally. A headless run cannot answer an approval prompt.
4. **`ParseLine()`** one output line to session events. Anything unparsed is a
   single `raw` event, which is still live in the UI.
5. **`ClassifyExit()`** what the exit meant. Return `outcomeBudget` for a
   limit; it is the one outcome the loop acts on.
6. Optionally **`InspectWorkdir()`**, **`Version()`** and **`Path()`**, for
   `relay check` and the startup banner.

Then add it to `supportedRuntimes()` in `runtime.go`. `ResolveRuntime` reads
that list, and so does the docs test.

## Then

- `make check`. It names every page you still owe: the comparison table in
  `docs/runtimes.md`, the field tables in `docs/configuration.md`, the manual.
- `make check-fresh`. An adapter's `Check()` is exactly what breaks the
  fresh-clone property, because your machine has the CLI installed.
- Never add `if runtime == "…"` to the worker loop. If that is the shape the
  change wants, it belongs in an adapter:
  [Where runtime-specific behaviour goes](../../../docs/contributing/adapters.md#where-runtime-specific-behaviour-goes).
- The argv is not overridable from the config. A setting worth having is a
  declared key in `ConfigFields()`.
- "Supported" means verified against a real CLI and given a bound, not that an
  argv can be built.
- The bash bridge is gated off by `bashAdaptersEnabled` and has to keep
  compiling. Do not break its test to land a native adapter.

Finish with the `docs-change` skill, then `pre-pr`.
