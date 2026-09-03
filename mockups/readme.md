# Mockups

Design proposals for the dashboard. Nothing in this directory ships, nothing is
embedded in the binary, and nothing here is wired to a worker: every figure is
invented.

| File | |
|---|---|
| [agent-activity.html](agent-activity.html) | Five options for showing what agents are working on, what they cost, and how they hand work to each other. Open it in a browser and use the tabs. |

Each option names the fields it would read from today's snapshot and the ones
that need new plumbing. The page copies its palette, type and components from
[the real dashboard](../cmd/relay-cli/ui/index.html), so a proposal can be
judged next to what is there now, and it keeps the read-only rule: no control
in any frame starts, stops or pauses anything.

The page links one webfont for its monospace text. Offline it falls back to the
same stack the dashboard uses. The dashboard itself stays self-contained and
offline; that rule is about the served page, and this file is not served.

What the dashboard shows today is in [docs/cli.md](../docs/cli.md).
