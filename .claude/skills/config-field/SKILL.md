---
name: config-field
description: Use when adding, renaming, removing or defaulting a field in ~/.relay/config, including a runtime_config key on the claude or codex adapter. A field that lands in the code alone is a field nobody can discover.
---

# Changing a config field

The reasons are in
[Changing the config](../../../docs/contributing/config-fields.md). This is the
order to work in. Run `make lint-docs` after each step: it names the surface you
have not reached yet, which is faster than reading the whole loop first.

## 1. Decide which side of the line it is on

Outside `runtime_config` means relay-cli enforces it. Inside means it is one
CLI's vocabulary, declared by that adapter. The table is
[First: which kind of field is it?](../../../docs/contributing/config-fields.md#first-which-kind-of-field-is-it).
Ask who kills the run.

## 2. A runtime setting

One place: that adapter's `ConfigFields()`, then a row in the runtime's table
in `docs/configuration.md`, then use it in `BuildCmd`. See
[Adding a runtime setting](../../../docs/contributing/config-fields.md#adding-a-runtime-setting).

## 3. A relay-cli field

`config.go` (the `Worker` struct, `workerKeys`, a `default…` constant that is a
bound, parsing, validation appended to `problems`), then the worker table in
`docs/configuration.md` with the default in the last column, then the
`THE CONFIG FILE` block in `helpText`, and `shortHelp` too if it is required.
See
[Adding a relay-cli field](../../../docs/contributing/config-fields.md#adding-a-relay-cli-field).

## 4. Removing, renaming or defaulting

A removed key goes in `removedKeys` mapped to what to use instead, and every
trace of it leaves the user pages. A changed default is the constant, the last
column of the docs table, and the inline comment in the manual. See
[Removing or renaming a field](../../../docs/contributing/config-fields.md#removing-or-renaming-a-field)
and
[Changing a default](../../../docs/contributing/config-fields.md#changing-a-default).

## 5. Let the tests tell you what is missing

`make check`. These are the ones that fail, and each names the document to fix:

| Test | Fails when |
|---|---|
| `TestEveryWorkerFieldIsDocumented` | a json tag is missing from the docs table or `helpText` |
| `TestEveryRuntimeConfigFieldIsDocumented` | a runtime key is undocumented, or has no `Doc` line |
| `TestConfigDocsQuoteTheRealDefaults` | a docs default disagrees with the code |
| `TestEveryRemovedKeyExplainsItself` | a removed key has no replacement text |
| `TestConfigDocsExampleValidates` | the example config no longer loads |
| `TestWorkerKeysMatchTheStruct` | `workerKeys` and the struct disagree |

Do not relax one. Fix the document it names, and add the `CHANGELOG.md` entry
under Unreleased in the same commit. Then run the `docs-change` skill.
