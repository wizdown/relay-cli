// relay-cli — run relay CLI workers, and watch them work.
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
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
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
	version = "0.4.1-SNAPSHOT"
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
// them, the unknown-command error names them, `relay help <command>` accepts
// them, and tests hold both the manual and docs/cli.md to it.
var commands = []string{"init", "check", "run", "version", "help"}

// Exit statuses. Scripts and CI wrappers branch on these, so they are fixed
// and documented in docs/cli.md.
const (
	exitOK    = 0 // the command did what it says
	exitFail  = 1 // the config, a runtime or a credential failed
	exitUsage = 2 // the command line itself was wrong
)

// shortHelpMaxLines is the ceiling a test holds shortHelp to: one screen, on
// the smallest terminal anyone actually uses.
const shortHelpMaxLines = 40

// shortHelp is what a bare `relay`, `-h` and `--help` print: one screen, no
// rationale.
//
// The manual below is the reference, and it is the right thing to have — but it
// is not what someone typing `relay` with no arguments is asking for. They want
// the command names, the flags, and the decisions the config will demand of
// them. Everything here earns its line by being one of those three; the reasons
// behind them are one `relay help` away.
const shortHelp = `relay ` + version + ` (` + channel + `) — a fleet of coding-CLI workers, driven by relay.

USAGE
  relay <command> [flags]
  relay help [command]

COMMANDS ------------------------------------------------------------------
  init      write ~/` + relayDirName + `/` + configFileName + ` (never overwrites an existing one)
  check     validate the config + probe every credential. Spends nothing
  run       start every worker, open the dashboard on 127.0.0.1:7717
  version   print the version
  help      the full manual. "relay help <command>" for one command

FLAGS ---------------------------------------------------------------------
  run     --port N (default 7717)  --no-open  --quiet  --no-archive
          --keep-awake  macOS: hold off sleep while on AC power
  check   --timeout N seconds (default 15)
  init    takes none

QUICKSTART ----------------------------------------------------------------
  1  relay init      write the starting config
  2  edit it         paste relay_mcp and repo_dir into each worker
  3  relay check     proves the wiring. Costs nothing
  4  relay run       start the fleet

WHAT YOU HAVE TO DECIDE, per worker ---------------------------------------
  relay_mcp   the agent's credential URL from relay. A SECRET, shown ONCE
  repo_dir    the checkout this agent works in. Its files and tooling are
              what the agent can do, and a headless run rewrites them
              with no prompt to answer
  runtime     claude or codex. Install and sign in to that CLI yourself;
              none is bundled
  model       stated outright, in that runtime's own words:
                claude: opus | sonnet | haiku
                codex:  sol | terra | luna

  Everything else is already bounded. Polls are free; only RUNS spend.
  Never commit ~/` + relayDirName + `/` + configFileName + `: every relay_mcp in it is live.

  relay help prints the full manual        ` + repoURL + `
`

// helpText is the whole manual, and `relay help` is what prints it.
//
// It has to stand alone: a user with only the binary has no checkout and no
// docs/. So it carries the config reference, the model list, the runtimes and
// the safeguards. It is a REFERENCE: a section states what a thing IS and what
// it DEFAULTS to. The reason a thing works that way belongs in
// docs/contributing/, and a reason that lives in two places will disagree.
const helpText = `relay ` + version + ` (` + channel + `) — run Relay CLI workers, and watch them work.

  one worker = one Relay agent identity x one directory x one CLI runtime

A worker polls Relay over HTTP for work delegated to its agent. A poll runs no
model and costs nothing. When a task is waiting, the worker launches one
headless CLI session that claims and works that task, then goes idle. A local
page shows every tool call and the cost so far.

BETA: 0.x until the interface settles. Spend is bounded by default; an upgrade
may need edits to your config.

USAGE -----------------------------------------------------------------------
  relay <command> [flags]
  relay help [command]         this manual, or one command's usage
  relay <command> --help       that command's usage

COMMANDS --------------------------------------------------------------------
  init      write ~/` + relayDirName + `/` + configFileName + ` with a starting config. Never overwrites
  check     validate the config, probe every credential, verify every runtime
            and report what each repo_dir gives its agent. Launches nothing,
            spends nothing
  run       start every worker in the config and open the dashboard
  version   print the version. Quote it in a bug report
  help      this manual

FLAGS -----------------------------------------------------------------------
  run    --port N       dashboard port on 127.0.0.1. Default 7717; if it is
                        taken, the next free port is used and the URL printed
         --no-open      do not open a browser (servers, containers, CI)
         --quiet        do not echo worker logs to stdout. The dashboard and
                        state/<name>/worker.log are unaffected
         --no-archive   do not archive worker logs to logs/ on shutdown
         --keep-awake   macOS: hold off system sleep for as long as the fleet
                        runs, while the Mac is on AC power. Closing the lid
                        still sleeps it. Ignored with a warning elsewhere
  check  --timeout N    seconds to wait for each credential probe. Default 15
  init   none           and it never overwrites an existing config

EXIT STATUS -----------------------------------------------------------------
  0   done. For check: every credential answered and every runtime is usable
  1   the config, a runtime or a credential failed. The message names the fix
  2   the command line was wrong: an unknown command, flag or argument

GETTING STARTED -------------------------------------------------------------
  You need this binary, a Relay agent credential, and a coding CLI you have
  installed and signed in to (see RUNTIMES). Relay is at
  https://relay.bytecurio.com/; the free workspace is enough.

  1  relay init       writes ~/` + relayDirName + `/` + configFileName + `: one worker per coding CLI on
                      PATH, with two placeholders in each
  2  fill them in     relay_mcp and repo_dir; see THE CONFIG FILE
  3  relay check      proves every credential and repo. Costs nothing
  4  relay run        starts the workers and opens the dashboard

  Then delegate a task to that agent in Relay and watch the run.

THE CONFIG FILE -------------------------------------------------------------
  ~/` + relayDirName + `/` + configFileName + ` is JSON listing your workers. // comments are allowed.
  Five fields per worker are required; relay init fills in all but relay_mcp
  and repo_dir. Everything else has a bounded default.

    {
      "poll_seconds": 30,          // fleet-wide. default 30, min 5
      "idle_poll_seconds": 120,    // fleet-wide. default 120, 2x-3600

      "workers": [
        {
          "name": "wizhub-claude",                    // REQUIRED, unique
          "relay_mcp": "https://…/relay/mcp/c/wzh_…", // REQUIRED, a SECRET
          "repo_dir": "~/code/wizhub",                // REQUIRED
          "runtime": "claude",                        // REQUIRED

          "max_runs_per_hour": 6,      // default 12: caps what you SPEND
          "max_seconds_per_run": 900,  // default 900: kill for one session

          "runtime_config": {          // settings the RUNTIME understands
            "model": "sonnet"          // REQUIRED, see CHOOSING A MODEL
          }
        }
      ]
    }

  name        unique in the file. Names the state directory, the log and the
              dashboard row
  relay_mcp   the agent's credential URL. In Relay, add the agent
              (onboard_agent) and issue its credential (issue_agent_credential);
              leave its capabilities off. The secret is in the URL, and it is
              shown ONCE
  repo_dir    the directory the agent works in. Its CLAUDE.md or AGENTS.md,
              skills and tooling are what the agent gets. An empty directory
              is valid. A run cannot ask before changing files, so choose a
              checkout you are willing to have rewritten. Preparing one:
              ` + docsBase + `working-directory.md
  runtime     claude or codex. Install and sign in to that CLI yourself

  Fields OUTSIDE runtime_config are enforced by relay-cli and mean the same for
  every runtime. Fields INSIDE are that CLI's own:

  claude   model             REQUIRED    opus | sonnet | haiku
           max_usd_per_run   default 5   dollar cap inside one run, enforced
                                         by the CLI. 0 removes it

  codex    model             REQUIRED    sol | terra | luna
           reasoning_effort  default medium
                                         minimal | low | medium | high | xhigh.
                                         The biggest dial on what a run costs
           sandbox           default workspace-write
                                         read-only | workspace-write (repo_dir
                                         and temp files) | danger-full-access
           network_access    default true
                                         may the agent's commands reach the
                                         network. Off, git push and installs fail
           web_search        default true
                                         may the agent search the web

  codex has NO per-run spend cap. A codex worker is bounded by
  max_seconds_per_run, max_runs_per_hour, reasoning_effort and the plan limits
  of the signed-in account.

  Every key is checked by name at every level of the file, and every problem
  is reported at once. A key this version does not accept is refused with the
  key you probably meant, or with where that setting went.

  Full reference, per runtime:
    ` + docsBase + `configuration.md

  NEVER COMMIT IT: each relay_mcp is a live credential.

CHOOSING A MODEL ------------------------------------------------------------
  Required for both runtimes. Write the short name; relay-cli pins it to the
  id beside it, and the full id is accepted too. Logs and the dashboard show
  the resolved id.

    opus     claude-opus-5      open-ended briefs, wide blast radius
    sonnet   claude-sonnet-5    the usual default
    haiku    claude-haiku-4-5   mechanical, well-specified work

    sol      gpt-5.6-sol        ambiguous, high-value work
    terra    gpt-5.6-terra      the balanced one
    luna     gpt-5.6-luna       narrow, repeatable work

  Any other name is refused when the config loads. The lists are what each
  vendor offered when this build was made; for a newer model, see ENVIRONMENT.
  Pair a bigger model with a tighter max_runs_per_hour.

RUNTIMES: no CLI is bundled -------------------------------------------------
  relay-cli ships adapters, not CLIs. Install the CLI, sign in, and relay-cli
  finds it on PATH.

    claude    SUPPORTED   live session feed; per-run spend cap, enforced by
                          the CLI
                          https://claude.com/claude-code
    codex     SUPPORTED   live session feed; NO per-run spend cap
                          https://developers.openai.com/codex/cli

  SIGN IN FIRST: "claude auth login", or "codex login" with your ChatGPT
  account. A worker runs the CLI as you.

  Before anything starts, relay-cli checks that each CLI the config names is
  installed, accepts the flags the adapter needs, and is signed in. A failure
  stops run and fails check, and names the fix. A CLI too old to be asked
  about sign-in warns and continues, and so does a credential in the
  environment. See ENVIRONMENT.

ENVIRONMENT -----------------------------------------------------------------
  RELAY_CLI_SKIP_RUNTIME_CHECK=1    skip the flag, sign-in and model checks
  ` + modelCheckEnv + `=1      pass an unlisted model through, with a
                                    warning naming the worker
  ANTHROPIC_API_KEY, CODEX_API_KEY  authenticate a CLI by key. The sign-in
                                    check stands down for that CLI; relay-cli
                                    cannot tell whether the key is valid

COST AND SAFEGUARDS ---------------------------------------------------------
  A POLL asks Relay "do I have a task?" and runs no model. A RUN is one CLI
  session, and is what costs money. Every ceiling counts RUNS.

    poll_seconds          default 30    fleet-wide, minimum 5
    idle_poll_seconds     default 120   fleet-wide, 2x poll_seconds to 3600
    max_runs_per_hour     default 12    runs started, per worker, per hour
    max_seconds_per_run   default 900   wall-clock kill for one session
    max_usd_per_run       default 5     claude only, enforced by the CLI
    relaunch cooldown     60s, fixed    between one run ending and the next

  Set any per-worker ceiling to 0 to remove it. A poll_seconds below 5 is
  rejected.

  A worker polls at poll_seconds while it has work, and for 5 minutes after its
  last task. Each quiet poll after that doubles the wait, up to
  idle_poll_seconds, and work resets it to poll_seconds. Every change of rate
  is one line in worker.log and on the dashboard. idle_poll_seconds is at
  least twice poll_seconds, and 0 is not accepted.

  A worker pauses itself after 10 consecutive probe failures, after 2 spend or
  usage-limit kills in a row, or when the same task has needed its attention
  across 3 consecutive completed runs. Each writes a PAUSED file naming its fix.

WHILE IT RUNS ---------------------------------------------------------------
  Ctrl-C                 stop every worker, archive logs to logs/, remove state/
  Pause one worker       touch ~/` + relayDirName + `/state/<name>/PAUSED
  Resume it              rm ~/` + relayDirName + `/state/<name>/PAUSED
  Apply a config edit    restart; state/ is rebuilt on every start

  ~/` + relayDirName + `/
    ` + configFileName + `    your workers. 0600; every relay_mcp in it is a live credential
    state/    runtime state while a fleet is up; removed on shutdown
    logs/     archived sessions

  One location, and no flag moves it.

  The dashboard is READ-ONLY and binds 127.0.0.1 only. Connector secrets are
  redacted before they reach the page. It serves two JSON routes: GET
  /api/snapshot for the current state, and /api/stream for live events.

Source and full documentation:
  ` + repoURL + `
`

// synopsis is the one-line usage of each command, printed at the top of its
// help and under a usage error.
var synopsis = map[string]string{
	"init":    "relay init",
	"check":   "relay check [--timeout N]",
	"run":     "relay run [--port N] [--no-open] [--quiet] [--no-archive] [--keep-awake]",
	"version": "relay version",
	"help":    "relay help [command]",
}

// summary is the one sentence each command's help opens with.
var summary = map[string]string{
	"init":    "Create ~/" + relayDirName + "/ with a starting config in it. Never overwrites one.",
	"check":   "Validate the config, probe every credential and verify every runtime.\nLaunches nothing and spends nothing.",
	"run":     "Start every worker in " + displayConfigPath() + " and open the dashboard.\nCtrl-C stops the fleet and archives its logs.",
	"version": "Print the version. Quote it in a bug report.",
	"help":    "Print the full manual, or one command's usage.",
}

// commandFlags returns the flag set a command parses, or nil for a command
// with none. The help for a command is built from this, so a flag added to the
// set is documented without a second copy to keep in step.
func commandFlags(name string) *flag.FlagSet {
	switch name {
	case "run":
		return runFlags(&runOpts{})
	case "check":
		return checkFlags(&checkOpts{})
	case "init":
		return initFlags()
	}
	return nil
}

// commandHelp is what `relay <command> --help` and `relay help <command>`
// print: the synopsis, one sentence, the flags with their defaults, and where
// the rest is.
func commandHelp(name string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "usage: %s\n\n%s\n", synopsis[name], summary[name])
	if fs := commandFlags(name); fs != nil {
		lines := flagLines(name, fs)
		if len(lines) > 0 {
			b.WriteString("\n")
			for _, l := range lines {
				b.WriteString(l + "\n")
			}
		}
	}
	switch name {
	case "run", "check":
		fmt.Fprintf(&b, "\nReads %s. Exits 1 when the config, a runtime or a credential\nfails, and 2 on a usage error.\n", displayConfigPath())
	}
	b.WriteString("\nRun \"relay help\" for the full manual.\n")
	return b.String()
}

// flagLines renders a flag set as the manual does: `--name N` for a valued
// flag, the usage, and the default where there is one. Flags come out in the
// order the synopsis names them, so the list reads the way the usage line does.
func flagLines(name string, fs *flag.FlagSet) []string {
	byName := map[string]*flag.Flag{}
	fs.VisitAll(func(f *flag.Flag) { byName[f.Name] = f })
	var out []string
	for _, m := range flagNameRe.FindAllStringSubmatch(synopsis[name], -1) {
		f, ok := byName[m[1]]
		if !ok {
			continue
		}
		delete(byName, m[1])
		out = append(out, flagLine(f))
	}
	// A flag the synopsis does not name still gets a line; a test says the
	// synopsis should name it.
	fs.VisitAll(func(f *flag.Flag) {
		if _, left := byName[f.Name]; left {
			out = append(out, flagLine(f))
		}
	})
	return out
}

var flagNameRe = regexp.MustCompile(`--([a-z-]+)`)

func flagLine(f *flag.Flag) string {
	name := "--" + f.Name
	if _, isBool := f.Value.(interface{ IsBoolFlag() bool }); !isBool {
		name += " N"
	}
	line := fmt.Sprintf("  %-14s %s", name, f.Usage)
	if f.DefValue != "" && f.DefValue != "false" {
		line += ". Default " + f.DefValue
	}
	return line
}

// displayConfigPath is the config path as a human would write it. Errors that
// tell someone which file to edit read better with the ~ they typed than with
// their expanded home directory.
func displayConfigPath() string {
	return "~/" + relayDirName + "/" + configFileName
}

// main is one line: everything else returns an exit status, so a test can
// drive the whole command surface through dispatch without a process. A test
// holds this to being the only os.Exit in the package.
func main() {
	os.Exit(dispatch(os.Args[1:], os.Stdout, os.Stderr))
}

// dispatch routes one command line to its command and returns the exit status.
// Help goes to stdout and exits 0; a wrong command line goes to stderr with an
// `error:` prefix and exits 2; a command that ran and failed exits 1.
func dispatch(args []string, stdout, stderr io.Writer) int {
	// A bare invocation prints help rather than starting the fleet. Starting is
	// not a neutral default here — it launches autonomous sessions that spend
	// money — so it has to be asked for by name.
	if len(args) == 0 {
		fmt.Fprint(stdout, shortHelp)
		return exitOK
	}

	switch args[0] {
	case "-h", "--help":
		fmt.Fprint(stdout, shortHelp)
		return exitOK
	case "help":
		return helpCommand(args[1:], stdout, stderr)
	case "version", "-v", "--version":
		if code, stop := rejectArguments("version", args[1:], stdout, stderr); stop {
			return code
		}
		fmt.Fprintln(stdout, versionLine())
		return exitOK
	case "run":
		return runCommand(args[1:], stdout, stderr)
	case "check":
		return checkCommand(args[1:], stdout, stderr)
	case "init":
		return initCommand(args[1:], stdout, stderr)
	}

	// A flag where the command should be is the likeliest mistake, and the fix
	// is one word — so say the whole corrected line rather than just refusing.
	if strings.HasPrefix(args[0], "-") {
		fmt.Fprintf(stderr, "error: %q is a flag, not a command. Did you mean:\n\n  relay run %s\n\nRun \"relay help\" for the full manual.\n",
			args[0], strings.Join(args, " "))
		return exitUsage
	}
	fmt.Fprintf(stderr, "error: unknown command %q. Commands are: %s.\n", args[0], strings.Join(commands, ", "))
	return exitUsage
}

// helpCommand prints the manual, or one command's help when named.
func helpCommand(args []string, stdout, stderr io.Writer) int {
	switch {
	case len(args) == 0:
		fmt.Fprint(stdout, helpText)
	case len(args) == 1 && (args[0] == "-h" || args[0] == "--help"):
		fmt.Fprint(stdout, commandHelp("help"))
	case len(args) == 1 && contains(commands, args[0]):
		fmt.Fprint(stdout, commandHelp(args[0]))
	case len(args) == 1:
		fmt.Fprintf(stderr, "error: unknown command %q. Commands are: %s.\n", args[0], strings.Join(commands, ", "))
		return exitUsage
	default:
		fmt.Fprintf(stderr, "error: \"relay help\" takes one command name, not %d arguments.\n\n  usage: %s\n", len(args), synopsis["help"])
		return exitUsage
	}
	return exitOK
}

// rejectArguments handles a command that takes nothing: `--help` prints its
// help, anything else is a usage error rather than a silent ignore. stop is
// true when the caller should return code.
func rejectArguments(name string, args []string, stdout, stderr io.Writer) (code int, stop bool) {
	if len(args) == 0 {
		return exitOK, false
	}
	if args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(stdout, commandHelp(name))
		return exitOK, true
	}
	fmt.Fprintf(stderr, "error: \"relay %s\" takes no arguments, got %q.\n\n  usage: %s\n", name, args[0], synopsis[name])
	return exitUsage, true
}

// parseFlags parses a command's flags the way every command should: `--help`
// prints that command's help on stdout and stops with 0, a wrong flag or a
// stray argument is one line on stderr naming the fix and stops with 2.
//
// The flag package's own output is discarded: its messages name flags with one
// dash and its Usage hook prints after the error, which reads as noise.
func parseFlags(name string, fs *flag.FlagSet, args []string, stdout, stderr io.Writer) (code int, stop bool) {
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	err := fs.Parse(args)
	if err == flag.ErrHelp {
		fmt.Fprint(stdout, commandHelp(name))
		return exitOK, true
	}
	if err != nil {
		return usageError(name, flagError(name, err), stderr), true
	}
	if fs.NArg() > 0 {
		if name == "init" {
			return usageError(name, fmt.Sprintf("\"relay init\" takes no arguments, got %q. It always writes to %s", fs.Arg(0), displayConfigPath()), stderr), true
		}
		return usageError(name, fmt.Sprintf("\"relay %s\" takes no arguments, got %q", name, fs.Arg(0)), stderr), true
	}
	return exitOK, false
}

func usageError(name, msg string, stderr io.Writer) int {
	fmt.Fprintf(stderr, "error: %s.\n\n  usage: %s\n\nRun \"relay help %s\" for its flags.\n", msg, synopsis[name], name)
	return exitUsage
}

// flagError rewrites the flag package's parse errors into the words the docs
// use: double-dash names, and what the flag wanted instead of "parse error".
func flagError(name string, err error) string {
	msg := err.Error()
	if rest, ok := strings.CutPrefix(msg, "flag provided but not defined: -"); ok {
		return fmt.Sprintf("\"relay %s\" does not take --%s", name, strings.TrimPrefix(rest, "-"))
	}
	if rest, ok := strings.CutPrefix(msg, "flag needs an argument: -"); ok {
		return fmt.Sprintf("--%s needs a value", strings.TrimPrefix(rest, "-"))
	}
	if strings.HasPrefix(msg, "invalid value ") {
		// invalid value "abc" for flag -port: parse error
		value, after, _ := strings.Cut(strings.TrimPrefix(msg, "invalid value "), " for flag -")
		flagName, _, _ := strings.Cut(strings.TrimPrefix(after, "-"), ":")
		return fmt.Sprintf("--%s takes a number, not %s", flagName, value)
	}
	return msg
}

// checkOpts is the check command's flags, split out for the same reason
// runOpts is: a test walks them and proves the manual still documents each one.
type checkOpts struct {
	timeout int
}

func checkFlags(o *checkOpts) *flag.FlagSet {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.IntVar(&o.timeout, "timeout", defaultCheckTimeoutSecs, "seconds to wait for each credential probe")
	return fs
}

func checkCommand(args []string, stdout, stderr io.Writer) int {
	var o checkOpts
	if code, stop := parseFlags("check", checkFlags(&o), args, stdout, stderr); stop {
		return code
	}
	path, err := DefaultConfigPath()
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return exitFail
	}
	if err := check(path, time.Duration(o.timeout)*time.Second, stdout); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return exitFail
	}
	return exitOK
}

// check answers "would `run` work?" without launching anything.
//
// It runs every startup check run runs — ParseConfig validates the file and the
// repo directories, CheckRuntimes the CLIs — and asks relay one question per
// worker using the same token-free probe the poll loop uses. No CLI is started,
// so a credential can be tested for nothing.
//
// The probes run whether or not the runtime check passed, and both are
// reported. A signed-out CLI is a one-command fix; a wrong credential is a trip
// back to relay, and hiding the second behind the first costs a round of
// edit-and-rerun.
//
// That matters because the alternative is to start the fleet and watch: a
// revoked credential and an empty queue look identical from the outside until a
// worker has been running long enough to be trusted, and finding out by
// launching sessions is the expensive way to ask.
func check(configPath string, timeout time.Duration, out io.Writer) error {
	cfg, err := ParseConfig(configPath)
	if err != nil {
		return err
	}
	// Asked now, because the banner below names the version and path each CLI
	// resolved to, and reported after the probes so a signed-out CLI does not
	// hide what relay said.
	runtimeErr := CheckRuntimes(cfg)

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
		// The counts alone would read as work about to be picked up. An agent at
		// its parallel-claim limit is offered none of it, and check is where
		// someone looks first when a fleet is idle with a backlog.
		atLimit := ""
		if r.queue.AtLimit {
			atLimit = " · at limit, none claimable"
		}
		fmt.Fprintf(out, "  %-24s ok    queue: resume %d · attention %d · todo %d%s\n",
			r.worker.Name, r.queue.Resume, r.queue.Attention, r.queue.Todo, atLimit)
		writeWorkdirLine(out, r.worker, cfg.RelayDir)
	}
	fmt.Fprintln(out)

	var failures []string
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
		failures = append(failures, fmt.Sprintf("%d of %d worker(s) could not check in with relay.\n%s", failed, len(cfg.Workers), hint))
	}
	if runtimeErr != nil {
		failures = append(failures, runtimeErr.Error())
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "\n\n"))
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
	keepAwake bool
}

func runFlags(o *runOpts) *flag.FlagSet {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.IntVar(&o.port, "port", defaultPort, "dashboard port on 127.0.0.1, or the next free one")
	fs.BoolVar(&o.noOpen, "no-open", false, "do not open a browser at startup")
	fs.BoolVar(&o.quiet, "quiet", false, "do not echo worker logs to stdout")
	fs.BoolVar(&o.noArchive, "no-archive", false, "do not archive worker logs to logs/ on shutdown")
	fs.BoolVar(&o.keepAwake, "keep-awake", false, "macOS: hold off system sleep while on AC power")
	return fs
}

func runCommand(args []string, stdout, stderr io.Writer) int {
	var o runOpts
	if code, stop := parseFlags("run", runFlags(&o), args, stdout, stderr); stop {
		return code
	}
	path, err := DefaultConfigPath()
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return exitFail
	}
	if err := run(path, o.port, o.noOpen, o.noArchive, o.quiet, o.keepAwake); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return exitFail
	}
	return exitOK
}

func run(configPath string, port int, noOpen, noArchive, quiet, keepAwake bool) error {
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
	fmt.Printf("  polling every %gs, %gs when idle\n", cfg.PollSeconds, cfg.IdlePollSeconds)
	for _, w := range cfg.Workers {
		fmt.Printf("  %-24s runtime %-8s runs/h %d  repo %s\n",
			w.Name, w.Runtime, w.MaxRunsPerHour, w.RepoDir)
	}
	if keepAwake {
		// The call runs now and reports on the banner; the release it hands back
		// is what the defer holds until shutdown.
		defer announceKeepAwake(os.Stdout, os.Stderr)()
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
