// relay-cli — run a fleet of relay CLI workers, and watch them work.
//
// One binary with no runtime dependencies. It reads ~/.relay/config, validates
// it, runs every worker's poll loop, and serves a local page showing each
// worker's state, every poll result, and the live event stream of whatever CLI
// session is running right now.
//
// The probe is native HTTP and the config parse is native JSON, which is why
// neither jq nor curl is needed. runtimes/*.sh, beside your config, remains the
// extension point for a CLI this binary has no native adapter for.
package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	// 0.x, and it stays there until the interface is settled. relay's own
	// surface is still evolving, so a release may still change configuration or
	// the worker contract. A 1.0 would be a claim that it will not, which is not
	// a claim this can make yet — see the versioning note in the root readme.
	// The `-SNAPSHOT` marker is what makes this constant honest between
	// releases. `master` always carries the *next* version with the marker on
	// it; `make release` takes it off for exactly one commit — the one the tag
	// points at — and puts it back on the next. So a binary that prints
	// `-SNAPSHOT` was built from a tree nobody published, which is a thing a
	// bug report needs to say and a bare number cannot.
	version = "0.1.2-SNAPSHOT"
	channel = "beta"

	snapshotSuffix = "-SNAPSHOT"

	// Everything relay-cli owns lives in ONE place:
	//
	//	~/.relay/
	//	  config      the worker list, 0600 — every relay_mcp is a live credential
	//	  state/      runtime state, removed on shutdown
	//	  logs/       archived sessions
	//
	// One location, and no flag to move it. A worker is a relay agent IDENTITY
	// holding a credential relay issued to a person, and identities are
	// user-scoped the way ~/.aws and ~/.kube are. A fleet also routinely spans
	// several checkouts, so there is no one repo it could belong beside — and
	// the config can never be committed anyway, which is the usual reason a tool
	// keeps its settings inside a project.
	//
	// Keeping it in exactly one place is also what makes "which config is this
	// actually running?" a question nobody has to ask.
	relayDirName   = ".relay"
	configFileName = "config"
	stateDirName   = "state"
	logsDirName    = "logs"

	// Uncommon on purpose: 3000, 5000, 8000 and 8080 are all likely to be busy on
	// a developer's machine. If it is taken anyway, Listen falls forward.
	defaultPort = 7717

	// Generous for one HTTP round trip, because the failure this bounds is a
	// host that never answers, and a slow relay answering in eight seconds is
	// not the thing `check` should report as broken.
	defaultCheckTimeoutSecs = 15

	// Doc links this CLI prints have to be openable by whoever reads them, and
	// that reader may hold nothing but this binary: there is no checkout for a
	// bare "docs/configuration.md" to resolve against, and ~/.relay/ — where the
	// config `init` writes lands — is not one either. So every doc pointer
	// relay-cli emits is a full URL, built from here so they cannot drift apart.
	//
	// The branch is master, this repo's default. A link to a branch that does
	// not exist is a 404 shipped inside somebody's config file, which is worse
	// than no link at all. If the default is ever renamed, GitHub redirects the
	// old name, so this keeps resolving until it is updated.
	repoURL  = "https://github.com/wizdown/relay-cli"
	docsBase = repoURL + "/blob/master/docs/"
)

// The relay protocol every worker's CLI is given. Embedded so the binary is
// self-contained, but the on-disk copy wins when there is one, so that editing
// the rules in a checkout reaches the workers you run from that checkout
// without rebuilding.
//
//go:embed assets/worker-rules.md
var embeddedRules string

// Supervisor owns the fleet: the validated config, the bus, and one runner per
// worker.
type Supervisor struct {
	cfg       *Config
	bus       *Bus
	runners   []*WorkerRunner
	startedAt time.Time
}

func (s *Supervisor) Statuses() []WorkerStatus {
	out := make([]WorkerStatus, 0, len(s.runners))
	for _, r := range s.runners {
		out = append(out, r.Status())
	}
	return out
}

// build is the tree this binary came from — `git describe --tags --always
// --dirty`, stamped by the Makefile. Empty when built outside a checkout, which
// is why nothing may depend on it being set.
//
// It exists because `-SNAPSHOT` says a build is unreleased without saying which
// build it is: every commit between two releases prints the same constant. This
// names the commit. A binary built from a release tag repeats the tag and is
// suppressed, so released output is exactly what the docs show.
var build string

// versionLine is what `relay version` prints, and what belongs in a bug report.
func versionLine() string {
	line := "relay " + version + " (" + channel + ")"
	if build != "" && build != "v"+version {
		line += " [" + build + "]"
	}
	return line
}

// baseVersion is the version this tree would be released as: the constant with
// its `-SNAPSHOT` marker removed. `make release` is what removes it for real.
func baseVersion() string { return strings.TrimSuffix(version, snapshotSuffix) }

// commands is the command list, in one place: the switch in main dispatches
// them, the unknown-command error names them, and tests hold both the manual
// and docs/cli.md to it.
var commands = []string{"init", "check", "run", "version", "help"}

// helpText is the whole manual. It is long on purpose.
//
// This CLI launches autonomous agents that spend money, inside a repo checkout,
// with no prompt to answer — so the cost of someone (or some agent) guessing at
// how it works is not a confusing error message, it is a fleet doing something
// unintended. Everything needed to use it correctly is here, in one place, at
// zero cost to read.
const helpText = `relay ` + version + ` (` + channel + `) — run a fleet of relay CLI workers, and watch them work.

Each worker polls relay over plain HTTP for work assigned to its agent. A poll
runs no model and costs nothing. Only when relay actually has a task does the
worker launch one headless CLI session, which claims and works exactly that one
task and then goes idle again. While a session runs, relay-cli serves a local
page showing it happen: every tool call with its target, every assistant turn,
and the cost so far.

  one worker = one relay agent identity × one repo checkout × one CLI runtime

This is BETA and stays on 0.x until the interface settles. It is safe to run —
spend is bounded by default — but relay is still evolving, so an upgrade may
need edits to your config.

USAGE
  relay <command> [flags]

GETTING STARTED
  You need two things: this binary, and a relay agent credential. Relay is at
  https://relay.bytecurio.com/ — sign in with Google or Microsoft, and the free
  demo workspace is enough. Nothing else is installed from here: the coding CLI
  is separate (see RUNTIMES).

    1. relay init
       Creates ~/.relay/ with a starting config in it. Everything relay-cli
       keeps lives there and nowhere else.

    2. Fill in the two placeholders it wrote.
       "relay_mcp" — in your relay workspace, add the agent (onboard_agent),
       then issue its credential (issue_agent_credential). Leave its
       capabilities off; a first worker needs none. The secret is embedded in
       the URL it returns and is shown EXACTLY ONCE.
       "repo_dir" — the directory this agent should work in. Its CLAUDE.md,
       skills and tooling are what the agent gets, so this is the choice that
       decides what the worker is actually able to do. An empty directory is a
       valid start, and this is the ladder from there:
       ` + docsBase + `working-directory.md
       Point it somewhere you are willing to have rewritten: a headless run is
       fully autonomous and cannot answer an approval prompt.

    3. relay check
       Validates the config, tests every credential, and prints what each
       worker's repo_dir would give its agent — the CLAUDE.md, skills and
       subagents the CLI will actually find there. Launches nothing and spends
       nothing, so it is the cheap way to find a mistake.

    4. relay run
       Starts the workers and opens the dashboard on 127.0.0.1.

  Then delegate a task to that agent in relay and watch the run happen.

COMMANDS
  init         create ~/.relay/ with a starting config
  check        validate the config, test every credential and report what each
               repo_dir holds, launching nothing
  run          start every worker in the config and open the dashboard
  version      print the version
  help         show this message

WHERE EVERYTHING LIVES
  ~/.relay/
    config      your workers. 0600 — every relay_mcp in it is a live credential
    state/      runtime state while a fleet is up; removed on shutdown
    logs/       archived sessions

  One location, and no flag moves it. A worker is a relay agent identity holding
  a credential issued to you, which is user-scoped the way ~/.aws and ~/.kube
  are — and a fleet routinely spans several checkouts, so there is no one repo
  it would belong beside.

FLAGS (for run)
  --port N        dashboard port on 127.0.0.1. Default 7717. If it is taken,
                  the next free port is used and the real URL is printed.
  --no-open       do not open a browser (servers, containers, CI).
  --quiet         do not echo worker logs to stdout. The dashboard and
                  state/<name>/worker.log are unaffected.
  --no-archive    do not archive worker logs to logs/ on shutdown.

FLAGS (for check)
  --timeout N     seconds to wait for each credential probe. Default 15.

  init takes no flags. It never overwrites an existing config: that file holds
  credentials relay showed only once.

EXAMPLES
  relay init                 # start here — writes a starting config
  relay check                # is everything wired up? costs nothing
  relay run
  relay run --port 8080 --no-open
  relay run --quiet          # dashboard only, quiet terminal

THE CONFIG FILE
  ~/.relay/config is JSON listing your workers, and // comments are allowed.
  Four fields per worker are required — a name, a credential, a repo and a
  runtime — because each is a decision relay-cli cannot make for you. Everything
  else has a bounded default:

    {
      "poll_seconds": 30,          // fleet-wide. default 30, min 5

      "workers": [
        {
          "name": "wizhub-claude",                    // REQUIRED, unique
          "relay_mcp": "https://…/relay/mcp/c/wzh_…", // REQUIRED, a SECRET
          "repo_dir": "~/code/wizhub",                // REQUIRED
          "runtime": "claude",                        // REQUIRED

          "max_runs_per_hour": 6,      // default 12  — caps what you SPEND
          "max_seconds_per_run": 900,  // default 900 — kill for one session

          "runtime_config": {          // settings the RUNTIME understands
            "model": "sonnet",         // REQUIRED for claude
            "max_usd_per_run": 5       // default 5 — cap inside one run
          }
        }
      ]
    }

  The fields outside "runtime_config" are enforced by relay-cli and mean the
  same thing for every runtime. The ones inside are that CLI's own vocabulary,
  so they differ per runtime. A codex worker's block reads:

          "runtime_config": {
            "model": "gpt-5.1-codex",       // REQUIRED for codex
            "reasoning_effort": "medium",   // default medium
            "sandbox": "workspace-write",   // default workspace-write
            "network_access": true,         // default true
            "web_search": true              // default true
          }

  codex has no per-run spend cap to set: "max_usd_per_run" is claude's, and a
  codex worker is bounded by max_seconds_per_run, max_runs_per_hour and the
  plan limits of the account it is signed in as.

  EVERY key is checked by name, at every level of the file, and one this version
  does not accept is refused when the config loads — with the key you probably
  meant, or with where that setting belongs. A key relay-cli does not read is a
  ceiling you believe is in force and is not, so it is not ignored for the life
  of the fleet. Values are checked for sanity too: a ceiling counts whole
  things and cannot be negative, "relay_mcp" has to be an http(s) URL, and
  "repo_dir" has to be an absolute path that exists. Every problem in the file
  is reported at once.

  Full reference, per runtime:
    ` + docsBase + `configuration.md
  Preparing a repo_dir:
    ` + docsBase + `working-directory.md

  NEVER COMMIT IT: each relay_mcp is a live credential, and each is shown by
  relay exactly once.

CHOOSING A MODEL
  For claude, "runtime_config"."model" takes opus, sonnet or haiku — each alias
  tracks the latest model in that family — or a full id like claude-opus-5 to
  pin an exact version.

    opus     open-ended briefs, wide blast radius, work you would review closely
    sonnet   the usual default — capable, and cheaper per run
    haiku    mechanical, well-specified work where turnaround is the win

  It is required rather than defaulted on purpose. The CLI has its own default,
  but that default moves between CLI versions — so an unchanged config would
  quietly change what a worker costs the next time you upgraded claude. An
  unattended process that spends money should say what it runs.

  Pair a bigger model with a tighter max_runs_per_hour and a lower
  max_usd_per_run. A wrong model name fails inside the run, not at startup, so
  "claude --help" is the authority on what is currently accepted.

  For codex, "model" is required for the same reason and passed through the same
  way, with "codex --help" as the authority. The dial beside it is
  "reasoning_effort" — minimal, low, medium, high or xhigh. It is the largest
  influence on what a codex run costs, and since codex enforces no spend cap,
  effort and max_runs_per_hour are the two knobs you have.

TWO CLOCKS
  A POLL is curl-equivalent: it asks relay "do I have a task?" and runs no
  model, so an idle worker costs nothing however often it ticks.
  A RUN is one CLI session, and is the part that costs money.
  max_runs_per_hour limits RUNS, not polls. Lowering it does not make a worker
  check less often — it makes it act less often.

WHILE IT RUNS
  Ctrl-C            stop every worker, archive logs to logs/, remove state/
  Pause one worker  touch ~/.relay/state/<name>/PAUSED
  Resume it         rm ~/.relay/state/<name>/PAUSED
  A worker also pauses itself after repeated probe failures, two spend-cap
  kills in a row, or an attention stall — each explains the fix in its log.

THE DASHBOARD
  Read-only. There is no route that can pause a worker, launch a run, or edit a
  ceiling, so a page open on your machine can only show what already happened.
  It binds 127.0.0.1 only and no flag changes that: session output can contain
  anything the agent read. Connector secrets are redacted server-side and never
  reach the page.

RUNTIMES — no CLI is bundled, install them yourself
  relay-cli ships adapters, not CLIs. An adapter is the code that knows how to
  invoke one coding CLI and read what it prints; the CLI itself is a separate
  program you install, and relay-cli finds it on PATH.

    claude    SUPPORTED. Adapter compiled in, live session feed, and a hard
              per-run spend cap the CLI enforces itself.
              Install the CLI separately: https://claude.com/claude-code
    codex     SUPPORTED. Adapter compiled in, live session feed — and NO per-run
              spend cap, because the CLI has none to set. A codex worker is
              bounded by max_seconds_per_run, max_runs_per_hour and its
              account's plan limits, and nothing here can cut a run off at a
              dollar figure. Install the CLI separately:
              https://developers.openai.com/codex/cli
    <other>   nothing else is offered today. An unsupported "runtime" value is
              refused when the config loads, rather than failing inside a run
              you have already paid for.

  At startup, every worker's runtime must prove its CLI is installed and usable,
  and relay-cli refuses to start if one is not — a missing CLI is reported once,
  by name, rather than failing on every cycle in a background log. Each adapter
  also checks that the installed build accepts the flags it needs (streaming
  output, the MCP wiring, the sandbox or the spend cap); set
  RELAY_CLI_SKIP_RUNTIME_CHECK=1 to bypass that flag check.

  SIGN IN TO THE CLI FIRST. A worker launches it as you, so it authenticates the
  way your own sessions do — "claude auth login", or "codex login" with your
  ChatGPT account.

  Both are VERIFIED before anything starts, and a CLI that is installed but not
  signed in stops the start by name. Each one can be asked from its own stored
  credentials without spending anything, so this costs nothing and catches the
  fleet that would otherwise launch a worker per cycle, fail in the same second
  every time, and say so only in state/<name>/worker.log. A CLI too old to be
  asked warns instead: unverifiable is not the same as signed out. If you
  authenticate with a key in the environment (ANTHROPIC_API_KEY, CODEX_API_KEY)
  the check stands down for that CLI — the key works whatever its stored sign-in
  says.

EVERYTHING COSTS WHAT IT SAYS
  Polls are free and runs are not, and every ceiling here counts runs. The
  defaults bound you before you configure anything: 12 runs an hour, $5 inside
  one run, a 15-minute kill. Set any of them to 0 to remove it — deliberately.

  The one bound that is not yours to remove is the 5-second floor under
  poll_seconds. That one protects relay rather than you: an empty poll costs you
  nothing, but relay still has to answer it. A config below the floor is
  rejected instead of clamped.

  A worker also pauses ITSELF rather than failing forever: after repeated probe
  failures (a revoked credential, a dead host), after two spend-cap kills in a
  row, or when the same task has needed its attention across consecutive runs
  with nothing changing. Each explains its own fix in the worker's log.

Source and full documentation:
  ` + repoURL + `
`

func usage(w *os.File) { fmt.Fprint(w, helpText) }

// displayConfigPath is the config path as a human would write it. Errors that
// tell someone which file to edit read better with the ~ they typed than with
// their expanded home directory.
func displayConfigPath() string {
	return "~/" + relayDirName + "/" + configFileName
}

func main() {
	// A bare invocation prints help rather than starting the fleet. Starting is
	// not a neutral default here — it launches autonomous sessions that spend
	// money — so it has to be asked for by name.
	if len(os.Args) < 2 {
		usage(os.Stdout)
		return
	}

	switch os.Args[1] {
	case "help", "-h", "--help":
		usage(os.Stdout)
		return
	case "version", "-v", "--version":
		fmt.Println(versionLine())
		return
	case "run":
		runCommand(os.Args[2:])
		return
	case "check":
		checkCommand(os.Args[2:])
		return
	case "init":
		initCommand(os.Args[2:])
		return
	}

	// A flag where the command should be is the likeliest mistake, and the fix
	// is one word — so say the whole corrected line rather than just refusing.
	if strings.HasPrefix(os.Args[1], "-") {
		fmt.Fprintf(os.Stderr, "error: %q is a flag, not a command. Did you mean:\n\n  relay run %s\n\nRun \"relay help\" for the full manual.\n",
			os.Args[1], strings.Join(os.Args[1:], " "))
		os.Exit(2)
	}
	fmt.Fprintf(os.Stderr, "error: unknown command %q. Commands are: %s.\n", os.Args[1], strings.Join(commands, ", "))
	os.Exit(2)
}

// checkOpts is the check command's flags, split out for the same reason
// runOpts is: a test walks them and proves the manual still documents each one.
type checkOpts struct {
	timeout int
}

func checkFlags(o *checkOpts) *flag.FlagSet {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `
usage: relay check [--timeout N]

  Reads %s. Run "relay help" for the full manual.
`, displayConfigPath())
	}
	fs.IntVar(&o.timeout, "timeout", defaultCheckTimeoutSecs, "seconds to wait for each credential probe")
	return fs
}

func checkCommand(args []string) {
	var o checkOpts
	fs := checkFlags(&o)

	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "error: unexpected argument %q. Run \"relay help\" for usage.\n", fs.Arg(0))
		os.Exit(2)
	}

	path, err := DefaultConfigPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if err := check(path, time.Duration(o.timeout)*time.Second, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// check answers "would `run` work?" without launching anything.
//
// It runs every startup check run runs — LoadConfig validates the file, the
// runtimes and the repo directories — and then asks relay one question per
// worker using the same token-free probe the poll loop uses. No CLI is started,
// so a credential can be tested for nothing.
//
// That matters because the alternative is to start the fleet and watch: a
// revoked credential and an empty queue look identical from the outside until a
// worker has been running long enough to be trusted, and finding out by
// launching sessions is the expensive way to ask.
func check(configPath string, timeout time.Duration, out io.Writer) error {
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "relay %s (%s) — checking %d worker(s) from %s\n", version, channel, len(cfg.Workers), cfg.Path)
	for _, line := range runtimeBanner(cfg) {
		fmt.Fprintln(out, line)
	}
	fmt.Fprintln(out)

	// The probes are independent and each is one short HTTP round trip, so a
	// fleet of twenty costs the time of its slowest member, not their sum.
	type result struct {
		worker *Worker
		queue  QueueState
		err    error
	}
	results := make([]result, len(cfg.Workers))
	var wg sync.WaitGroup
	for i, w := range cfg.Workers {
		wg.Add(1)
		go func(i int, w *Worker) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			q, err := NewProber(w.Endpoint).GetAvailableTasks(ctx)
			results[i] = result{worker: w, queue: q, err: err}
		}(i, w)
	}
	wg.Wait()

	failed, unauthorized, answered := 0, false, false
	for _, r := range results {
		if r.err != nil {
			failed++
			// Scrub: a probe error can quote the URL it failed on, and that URL
			// is the credential.
			msg := oneLine(Scrub(r.err.Error()), 160)
			switch {
			case strings.Contains(msg, "401"):
				unauthorized = true
			case strings.Contains(msg, "HTTP "):
				// Something answered — so the host is right and reachable, and
				// the part of the URL that is wrong is the rest of it.
				answered = true
			}
			fmt.Fprintf(out, "  %-24s FAIL  %s\n", r.worker.Name, msg)
			writeWorkdirLine(out, r.worker, cfg.RelayDir)
			continue
		}
		fmt.Fprintf(out, "  %-24s ok    queue: resume %d · attention %d · todo %d\n",
			r.worker.Name, r.queue.Resume, r.queue.Attention, r.queue.Todo)
		writeWorkdirLine(out, r.worker, cfg.RelayDir)
	}
	fmt.Fprintln(out)

	if failed > 0 {
		// The advice is conditional because the failures have different fixes,
		// and offering all of them makes each one less believable: a 401 is a
		// credential to reissue, an answer that is not a 401 is a URL that
		// reached the right host and the wrong endpoint — the shape a truncated
		// paste makes — and neither is a refused connection or a DNS miss,
		// which is a host to correct.
		hint := "       The endpoint could not be reached at all — check the host in relay_mcp,\n" +
			"       and that this machine can reach it."
		switch {
		case unauthorized:
			hint = "       HTTP 401 means that worker's relay_mcp is wrong or its credential was\n" +
				"       revoked — issue a new one in relay (issue_agent_credential) and replace\n" +
				"       the whole URL, secret included."
		case answered:
			hint = "       The host answered, but not as a relay connector. It was reached, so the\n" +
				"       host in relay_mcp is right and the rest of the URL is not — paste the\n" +
				"       whole connector_url again, secret included, and check nothing was cut off."
		}
		return fmt.Errorf("%d of %d worker(s) could not check in with relay.\n%s", failed, len(cfg.Workers), hint)
	}

	// A zero queue is the healthy answer here, and saying so is the point: it is
	// the reading people most often mistake for a failure.
	fmt.Fprintf(out, "all %d worker(s) ready. A queue of 0 means the credential works and there is\n"+
		"simply no work waiting. Nothing was launched and nothing was spent.\n", len(cfg.Workers))
	return nil
}

// writeWorkdirLine prints what this worker's repo_dir would give the agent.
//
// The credential probe above proves relay is reachable; this proves the other
// half — that the CLAUDE.md, skills and subagents someone wrote are where the
// CLI will actually look for them. Both questions are why `check` exists, and
// answering only the first is how a worker starts, spends and knows nothing.
//
// A runtime that cannot describe a directory prints nothing rather than a
// placeholder, and nothing here can fail the check: an empty directory is a
// valid setup.
func writeWorkdirLine(out io.Writer, w *Worker, relayDir string) {
	rt, err := ResolveRuntime(w.Runtime, relayDir)
	if err != nil {
		return
	}
	insp, ok := rt.(workdirInspector)
	if !ok {
		return
	}
	if summary := insp.InspectWorkdir(w.RepoDir); summary != "" {
		fmt.Fprintf(out, "    repo %s   %s\n", w.RepoDir, summary)
	}
}

// runOpts is the run command's flags, split out from runCommand so a test can
// walk them and prove the manual above still documents every one.
type runOpts struct {
	port      int
	noOpen    bool
	noArchive bool
	quiet     bool
}

func runFlags(o *runOpts) *flag.FlagSet {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	// A mistyped flag gets a short pointer, not the whole manual: dumping ninety
	// lines after a one-word typo buries the error that explains it.
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `
usage: relay run [--port N] [--no-open] [--quiet] [--no-archive]

  Reads %s. Run "relay help" for the full manual.
`, displayConfigPath())
	}

	fs.IntVar(&o.port, "port", defaultPort, "dashboard port on 127.0.0.1 (falls forward if taken)")
	fs.BoolVar(&o.noOpen, "no-open", false, "do not open a browser at startup")
	fs.BoolVar(&o.noArchive, "no-archive", false, "do not archive worker logs to logs/ on shutdown")
	fs.BoolVar(&o.quiet, "quiet", false, "do not echo worker logs to stdout")
	return fs
}

func runCommand(args []string) {
	var o runOpts
	fs := runFlags(&o)

	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "error: unexpected argument %q. Run \"relay help\" for usage.\n", fs.Arg(0))
		os.Exit(2)
	}

	path, err := DefaultConfigPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if err := run(path, o.port, o.noOpen, o.noArchive, o.quiet); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(configPath string, port int, noOpen, noArchive, quiet bool) error {
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return err
	}

	liveDir := filepath.Join(cfg.RelayDir, stateDirName)
	logsDir := filepath.Join(cfg.RelayDir, logsDirName)

	if err := checkNothingElseRunning(liveDir); err != nil {
		return err
	}

	// Every start begins fresh: archive whatever a previous run left, then clear
	// the state directory. This is also what clears a PAUSED file, which the
	// breakers' messages promise.
	if err := archiveAndClear(liveDir, logsDir, noArchive); err != nil {
		return err
	}

	rules, rulesFile := loadWorkerRules(cfg.RelayDir)

	bus := NewBus(!quiet)
	sup := &Supervisor{cfg: cfg, bus: bus, startedAt: time.Now().UTC()}

	for _, w := range cfg.Workers {
		rt, err := ResolveRuntime(w.Runtime, cfg.RelayDir)
		if err != nil {
			return err // already validated in LoadConfig; belt and braces
		}
		runner := NewWorkerRunner(cfg, w, rt, bus, rules, rulesFile)
		if err := os.MkdirAll(runner.Dir(), 0o755); err != nil {
			return err
		}
		// Opened before the loop starts so a browser attaching immediately finds
		// every worker present and quiet, rather than half of them missing.
		if err := bus.OpenWorker(w.Name, runner.Dir()); err != nil {
			return err
		}
		sup.runners = append(sup.runners, runner)
	}

	if err := writePidFile(liveDir); err != nil {
		return err
	}

	ln, err := Listen(port)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("http://%s/", ln.Addr().String())

	srv := NewServer(sup)
	go func() {
		// The listener is loopback-only; a serve error after startup means the
		// dashboard is gone, not that the fleet should stop working.
		if err := http.Serve(ln, srv.mux); err != nil {
			fmt.Fprintf(os.Stderr, "dashboard stopped: %v\n", err)
		}
	}()

	fmt.Printf("relay %s (%s) — %d worker(s) from %s\n", version, channel, len(cfg.Workers), cfg.Path)
	// Which CLI each worker will actually drive, and where it was found. relay-cli
	// bundles no CLI, so "which claude is this using?" is a question worth
	// answering before the first run rather than after a surprising one.
	for _, line := range runtimeBanner(cfg) {
		fmt.Println(line)
	}
	fmt.Printf("  polling every %gs\n", cfg.PollSeconds)
	for _, w := range cfg.Workers {
		fmt.Printf("  %-24s runtime %-8s runs/h %d  repo %s\n",
			w.Name, w.Runtime, w.MaxRunsPerHour, w.RepoDir)
	}
	fmt.Printf("\ndashboard: %s\n", url)
	fmt.Printf("stop with Ctrl-C (workers stop, logs are archived to logs/)\n\n")

	if !noOpen {
		openBrowser(url)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var wg sync.WaitGroup
	for _, r := range sup.runners {
		wg.Add(1)
		go func(r *WorkerRunner) {
			defer wg.Done()
			r.Run(ctx)
		}(r)
	}

	<-ctx.Done()
	fmt.Println("\nstopping workers…")
	wg.Wait()

	bus.Flush()
	for _, w := range cfg.Workers {
		bus.CloseWorker(w.Name)
	}

	// Keep the evidence. Everything else in state/ is regenerable state
	// (and mcp.json holds a live connector secret), but a worker's log is the
	// only record of what a cycle actually did.
	if err := archiveAndClear(liveDir, logsDir, noArchive); err != nil {
		return err
	}
	fmt.Println("all workers stopped.")
	return nil
}

// runtimeBanner reports one line per distinct runtime in the config, naming the
// CLI version and path it resolved to.
func runtimeBanner(cfg *Config) []string {
	var out []string
	seen := map[string]bool{}
	for _, w := range cfg.Workers {
		if seen[w.Runtime] {
			continue
		}
		seen[w.Runtime] = true
		rt, err := ResolveRuntime(w.Runtime, cfg.RelayDir)
		if err != nil {
			continue
		}
		v, ok := rt.(versioned)
		if !ok {
			out = append(out, fmt.Sprintf("  runtime %-8s runtimes/%s.sh", w.Runtime, w.Runtime))
			continue
		}
		out = append(out, strings.TrimRight(fmt.Sprintf("  runtime %-8s %s %s", w.Runtime, v.Version(), v.Path()), " "))
	}
	return out
}

// checkNothingElseRunning refuses to start on top of a live fleet. Two fleets
// on one worker would double-claim tasks and fight over the same state
// directory; the lock directory would catch some of that, and none of it should
// have to be caught.
func checkNothingElseRunning(liveDir string) error {
	// A worker left behind by the retired bash poller. Its scripts are gone from
	// this repo, but a checkout upgraded while a fleet was running still has the
	// live processes and their state directory, and they would double-claim.
	matches, _ := filepath.Glob(filepath.Join(liveDir, "*", "worker.pid"))
	for _, p := range matches {
		if pid, ok := livePid(p); ok {
			return fmt.Errorf("worker %q is already running as a bash poller (pid %d),\n"+
				"       left over from the retired start.sh. Stop it with: kill %d",
				filepath.Base(filepath.Dir(p)), pid, pid)
		}
	}
	if pid, ok := livePid(filepath.Join(liveDir, "relay-cli.pid")); ok {
		return fmt.Errorf("relay is already running (pid %d). Stop it first", pid)
	}
	return nil
}

func livePid(path string) (int, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return 0, false
	}
	// Signal 0 tests for existence without touching the process.
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return 0, false
	}
	return pid, true
}

func writePidFile(liveDir string) error {
	if err := os.MkdirAll(liveDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(liveDir, "relay-cli.pid"),
		[]byte(strconv.Itoa(os.Getpid())+"\n"), 0o644)
}

// archiveAndClear moves each worker's logs to logs/<name>-<stamp>.log and then
// removes state/ entirely. The structured record goes with it: an
// archived .events.ndjson is what lets a run be replayed after the fact, which
// the prose log cannot do.
func archiveAndClear(liveDir, logsDir string, skipArchive bool) error {
	if _, err := os.Stat(liveDir); err != nil {
		return nil
	}
	if !skipArchive {
		stamp := time.Now().UTC().Format("20060102T150405Z")
		for _, name := range []string{"worker.log", "events.ndjson"} {
			matches, _ := filepath.Glob(filepath.Join(liveDir, "*", name))
			for _, p := range matches {
				info, err := os.Stat(p)
				if err != nil || info.Size() == 0 {
					continue
				}
				worker := filepath.Base(filepath.Dir(p))
				if err := os.MkdirAll(logsDir, 0o755); err != nil {
					return err
				}
				dest := filepath.Join(logsDir, fmt.Sprintf("%s-%s.log", worker, stamp))
				if name == "events.ndjson" {
					dest = filepath.Join(logsDir, fmt.Sprintf("%s-%s.events.ndjson", worker, stamp))
				}
				if err := os.Rename(p, dest); err != nil {
					return err
				}
			}
		}
	}
	return os.RemoveAll(liveDir)
}

// loadWorkerRules prefers the on-disk copy so that editing the rules in a
// checkout takes effect without rebuilding, and falls back to the embedded one
// so a downloaded binary works on a machine holding nothing but itself.
func loadWorkerRules(relayDir string) (string, string) {
	onDisk := filepath.Join(relayDir, "worker-rules.md")
	if b, err := os.ReadFile(onDisk); err == nil && len(b) > 0 {
		return string(b), onDisk
	}
	// The rules used to live under internal/. Silently falling back to the
	// embedded copy would quietly discard someone's edits — the one failure mode
	// of this override that gives no sign it happened.
	if legacy := filepath.Join(relayDir, "internal", "worker-rules.md"); fileHasContent(legacy) {
		fmt.Fprintf(os.Stderr, "warning: %s is no longer read — worker rules now live beside your\n"+
			"         config. The embedded copy is being used instead; to keep your\n"+
			"         edits, run: mv %s %s\n", legacy, legacy, onDisk)
	}
	// A bash adapter may want the rules as a path, so the embedded copy is
	// materialised into a temp file for WORKER_RULES_FILE to point at.
	tmp, err := os.CreateTemp("", "relay-worker-rules-*.md")
	if err != nil {
		return embeddedRules, ""
	}
	tmp.WriteString(embeddedRules)
	tmp.Close()
	return embeddedRules, tmp.Name()
}

// fileHasContent reports whether a path is a non-empty regular file — the same
// bar loadWorkerRules applies to the copy it actually reads.
func fileHasContent(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	// Best effort: a machine with no browser (a server, a container) is a normal
	// place to run this, and the URL is already printed.
	_ = cmd.Start()
}
