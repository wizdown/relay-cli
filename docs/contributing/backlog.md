# Backlog

Product ideas for relay-cli, recorded so they are not re-derived. Nothing here
is committed to, scheduled or designed. Each line is a todo, and a line that
turns out to be wrong is deleted rather than annotated.

The reasons behind what already ships are in [Design](design.md). This page is
only what does not.

## The boundary

[Who owns what](design.md#who-owns-what) splits every idea below into two
groups, and the split is not negotiable:

| Group | Scope | Where it lands |
|---|---|---|
| **relay-cli** | when and where a CLI runs | this repo |
| **Relay** | what an agent is for, and the task state machine | <https://relay.bytecurio.com/> |

An idea in the second group is recorded here as a one-line ask, with no
attempt to describe how Relay works. Relay serves its own workflow.

## The problem these answer

Two things, and only the first is this repo's.

**A filed task does not start quickly.** The probe made an idle fleet cheap,
not fast: it costs one HTTP call instead of ~55k tokens
([Why the probe exists](design.md#why-the-probe-exists)). Cost is solved.
Latency is not, and
[the backoff ladder](design.md#why-the-poll-rate-adapts) trades it away on
purpose — a worker quiet for five minutes doubles its wait up to
`idle_poll_seconds`. So the quieter the org, the longer the first task of the
morning sits. A fleet is slowest exactly when the human is most likely to be
waiting on it.

**One human is the bottleneck.** An org with a single operator spends more of
its wall-clock waiting on that person than on any model. Most of what would
fix that is Relay's, not this binary's.

## relay-cli: cut the wait

- [ ] **Take a wake signal from relay, and keep the probe.** The probe is not
      the cost problem; the timer in front of it is the latency problem. A
      worker that can be told "there is work" stops waiting out an interval it
      chose while the queue was empty.
- [ ] **Reset the backoff ladder on a wake.** A worker woken at
      `idle_poll_seconds` should come back at `poll_seconds`, the same as a
      poll that found work.
- [ ] **Long-poll the probe.** If a push path is not available, a probe that
      can block server-side for a bounded window cuts the same delay without a
      new transport, and keeps the no-model property.
- [ ] **Bound the first poll after a start.** A worker that has just launched
      knows nothing about how busy the agent is, and should not inherit a slow
      rate.
- [ ] **Log the wait a task actually incurred.** Time from filed to claimed,
      per worker, so the backoff can be argued about with numbers rather than
      impressions.

## relay-cli: reliability

- [ ] **Heartbeat a running session.** A worker that has gone quiet mid-session
      is currently indistinguishable from one doing slow work. The wall-clock
      timeout catches it eventually, and eventually is the complaint.
- [ ] **Report a session that produced nothing.** A run that exits clean having
      touched no files and posted no result is a failure the fleet currently
      counts as a success.
- [ ] **Resume a session killed by the timeout.** The lock and the lease are
      released correctly; the work done inside the checkout is lost.
- [ ] **A spend ceiling for codex.** `claude` has a per-run cap the CLI
      enforces and `codex` has none
      ([Why both shipped adapters are native](design.md#why-both-shipped-adapters-are-native)).
      The asymmetry is documented, which is not the same as fixed.
- [ ] **Surface the breaker in the dashboard.** The tenth consecutive probe
      failure trips it and writes a line nobody is reading.

## relay-cli: what a worker reports

- [ ] **A per-worker scorecard.** Runs, outcomes, rework, cost, and wall-clock
      waited. Enough to retire a worker on evidence.
- [ ] **A one-screen fleet digest.** What finished, what is stuck, what is
      waiting on the operator, since the operator last looked. The dashboard
      shows now; nobody can see the interval.
- [ ] **Cost per task, not per run.** A task that took four relaunches costs
      four runs, and the card shows one.

## Asks for Relay

Recorded here because this is where they were thought of. None of them is this
repo's to build, and a config field that duplicates any of them is rejected by
name.

**Cut the wait**

- [ ] Push an assignment to a worker instead of waiting for it to ask.
- [ ] Hand off directly between agents, so finishing work wakes whoever was
      blocked on it.
- [ ] Standing interests, so an agent is matched to work it did not ask about.
- [ ] A deadline on a task, and an escalation when it passes.
- [ ] A blocking question between agents, separate from filing a task.
- [ ] Let an idle agent take over a task stalled past its heartbeat.

**Make one human scale**

- [ ] A decision queue carrying, per item: the question, the options, a
      recommendation, what is blocked behind it, and the cost of being wrong.
- [ ] A default that applies on silence, with a timer, so an absent operator
      is not a stopped org.
- [ ] Capture an answer as a reusable rule, scoped and cited when applied, so
      the same question is asked once.
- [ ] Count how often each agent needs the operator, and score it.
- [ ] Standing authority per agent, by risk or cost, instead of per-event
      approval.
- [ ] Let an agent file work for the operator, with a deadline and the context
      attached.

**Agent collaboration**

- [ ] Route by capability and load rather than by name.
- [ ] Peer review between agents before a result reaches the operator.
- [ ] Acceptance criteria on a task, so done is checkable.
- [ ] Let an agent decline, split or renegotiate a task it was handed.
- [ ] A shared place for decisions and postmortems that agents write back to.

**Org instruments**

- [ ] Tasks completed per operator interrupt, over time.
- [ ] Rework rate and decision reversal rate, as the counter-signal to the
      above. An org that stops asking and an org that is confidently wrong look
      identical from outside.

## Out of scope

Not oversights. Each is a rule in [AGENTS.md](../../AGENTS.md#hard-rules).

| Idea | Why not |
|---|---|
| A control in the dashboard to start a run or pause a worker | The dashboard is read-only |
| A config field for an agent's prompt, capabilities or claim limits | Agent identity is Relay's, and five such keys are rejected by name |
| Relay's task states, leases or delegation graph, explained here | Relay serves its own workflow |
| Tracking task state in this binary | Relay owns the state machine |
