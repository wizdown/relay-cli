# `.claude/`

What a Claude Code session in this repo gets before anyone types anything.
[docs/working-directory.md](../docs/working-directory.md) tells a user to give
their agent exactly this; the repo carries its own.

Everything here links rather than restates. `AGENTS.md` is the source of truth,
and two files with overlapping instructions become two files that disagree. The
doc tests walk every `.md` file under here, so a link that rots fails the build.

## `settings.json`

- **`SessionStart` runs `make hooks`.** Every agent session is a fresh clone,
  and `core.hooksPath` is per clone and unset in a new one, so the credential
  and build-output checks would not run until someone typed it. The command is
  idempotent and prints two lines. See
  [The git hooks](../docs/contributing/development.md#the-git-hooks).
- **The allow-list is the read-only and check commands**, the ones an agent
  runs on a loop. Nothing on it writes to the repository or the network.
- **The deny-list is `~/.relay/` and any `.relay/` in a checkout.** Every
  `relay_mcp` in a config there is a live credential. It covers the `Read`
  tool, not a `cat` from a shell, which is why the hooks scan as well.

The file is strict JSON and cannot carry comments, which is why these notes are
here.

## Codex

Codex reads `AGENTS.md` and gets every rule that matters. There is no
`.codex/config.toml` here: nothing in this repo has run a codex worker against
itself, and a config guessed at rather than tested is a file that claims a
setup nobody has. Add one when there is a codex worker to prove it.
