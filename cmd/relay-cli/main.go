// relay-cli — run a fleet of relay CLI workers, and watch them work.
//
// One binary with no runtime dependencies. It reads .worker-config, validates
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
	version = "0.2.0"
	channel = "beta"

	// The config's directory is the poller root — live-workers/ and logs/ are
	// created beside the config, not beside the binary. That is what lets one
	// downloaded binary serve several checkouts, each with its own fleet.
	defaultConfigName = ".worker-config"

	// What `relay-cli init` creates, and where check and run look when they
	// are given no --config:
	//
	//	relay-cli-workers/
	//	  .worker-config     the worker list, 0600 — every endpoint is a secret
	//	  agent-workspace/   what one worker's CLI runs in
	//	  live-workers/      runtime state, removed on shutdown
	//	  logs/              archived sessions
	//
	// The workspace is a directory of its own rather than the poller root
	// because the poller root holds the credentials of every worker in the
	// fleet, and a session's working directory is the one place its own tools
	// are pointed at. That is hygiene, not containment: a headless run is not
	// jailed to repo_dir, and the boundary that actually holds is relay's —
	// one credential per agent, scoped to what that agent may be handed.
	homeDirName      = "relay-cli-workers"
	workspaceDirName = "agent-workspace"

	// Uncommon on purpose: 3000, 5000, 8000 and 8080 are all likely to be busy on
	// a developer's machine. If it is taken anyway, Listen falls forward.
	defaultPort = 7717

	// Generous for one HTTP round trip, because the failure this bounds is a
	// host that never answers, and a slow relay answering in eight seconds is
	// not the thing `check` should report as broken.
	defaultCheckTimeoutSecs = 15
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

// helpText is the whole manual. It is long on purpose.
//
// This CLI launches autonomous agents that spend money, inside a repo checkout,
// with no prompt to answer — so the cost of someone (or some agent) guessing at
// how it works is not a confusing error message, it is a fleet doing something
// unintended. Everything needed to use it correctly is here, in one place, at
// zero cost to read.
const helpText = `relay-cli ` + version + ` (` + channel + `) — run a fleet of relay CLI workers, and watch them work.

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
  relay-cli <command> [flags]

GETTING STARTED
  You need two things: this binary, and a relay agent credential. Relay is at
  https://relay.bytecurio.com/ — sign in with Google or Microsoft, and the free
  demo workspace is enough. Nothing else is installed from here: the coding CLI
  is separate (see RUNTIMES).

    1. relay-cli init
       Creates relay-cli-workers/ here, holding an annotated .worker-config
       and an agent-workspace/ for the worker to run in. That config is the
       reference; you should not need anything else.

    2. Get a credential from relay, and paste it in.
       In your relay workspace, add the agent (onboard_agent), then issue its
       credential (issue_agent_credential). Leave its capabilities off — a
       first worker needs none. The secret is embedded in the URL it returns
       and is shown EXACTLY ONCE.
       That is the only edit needed: "repo_dir" already points at the
       workspace. Change it when you want the worker in a checkout of yours.

    3. relay-cli check
       Validates the config and tests every credential. Launches nothing and
       spends nothing, so it is the cheap way to find a mistake.

    4. relay-cli run
       Starts the workers and opens the dashboard on 127.0.0.1.

  Then delegate a task to that agent in relay and watch the run happen.

COMMANDS
  init         create relay-cli-workers/ here: an annotated config and a
               workspace for the worker to run in
  check        validate the config and test every credential, launching nothing
  run          start every worker in the config and open the dashboard
  version      print the version
  help         show this message

FLAGS (for run)
  --config PATH   the worker list to run: a config file, or the directory
                  holding one. Its directory becomes the poller root, so
                  live-workers/ and logs/ are created next to it — not next to
                  the binary.
                  With no --config, .worker-config in the CURRENT directory is
                  used, and failing that relay-cli-workers/.worker-config
                  below it — which is what "relay-cli init" creates.
  --port N        dashboard port on 127.0.0.1. Default 7717. If it is taken,
                  the next free port is used and the real URL is printed.
  --no-open       do not open a browser (servers, containers, CI).
  --quiet         do not echo worker logs to stdout. The dashboard and
                  live-workers/<name>/worker.log are unaffected.
  --no-archive    do not archive worker logs to logs/ on shutdown.

FLAGS (for check)
  --config PATH   the worker list to check. Same default as run.
  --timeout N     seconds to wait for each credential probe. Default 15.

FLAGS (for init)
  --config PATH   where to create it: a directory, or the config file itself.
                  Default relay-cli-workers/ in the current directory. The
                  workspace is created beside the config either way.
                  init never overwrites an existing config: it holds credentials
                  relay showed only once.

EXAMPLES
  relay-cli init                 # start here — writes an annotated config
  relay-cli check                # is everything wired up? costs nothing
  relay-cli run
  relay-cli run --config ~/code/wizhub/.worker-config
  relay-cli run --port 8080 --no-open
  relay-cli run --quiet          # dashboard only, quiet terminal

THE CONFIG FILE
  .worker-config is JSON listing your workers, and // comments are allowed —
  "relay-cli init" writes one with every field annotated, which is the
  reference. Two fields per worker are required; everything else has a bounded
  default:

    {
      "relay_workers": [
        {
          "name": "wizhub-claude",                       // REQUIRED, unique
          "mcp_endpoint": "https://…/relay/mcp/c/wzh_…", // REQUIRED, a SECRET
          "runtime": "claude",     // default "claude"
          "repo_dir": "~/code/x",  // where the CLI runs. init points this at
                                   // the workspace it made; default is the
                                   // worker's own state dir
          "model": "opus",         // default: the CLI's own default
          "poll_frequency_seconds": 30,   // default 30, min 5 — polls are free
          "max_runs_per_hour": 6,         // default 12  — caps what you SPEND
          "max_budget_usd": 5,            // default 5   — cap inside one run
          "run_timeout_seconds": 900,     // default 900 — kill for one session
          "runtime_args": ""              // default none — raw extra CLI flags
        }
      ]
    }

  Run "relay-cli init" to generate this with every field explained.
  NEVER COMMIT IT: each mcp_endpoint is a live credential, and each is shown by
  relay exactly once. init writes a .gitignore beside it for that reason, and
  keeps the workspace a separate directory so what an agent works in is not
  what holds your credentials.

CHOOSING A MODEL
  "model" is passed to the CLI verbatim, so use that CLI's own spelling. For
  claude: opus, sonnet or haiku — each alias tracks the latest model in that
  family — or a full id like claude-opus-5 to pin an exact version.

  Omit "model" entirely to take the CLI's own default. That is a good choice:
  it needs no maintenance here when the lineup changes.

    opus     open-ended briefs, wide blast radius, work you would review closely
    sonnet   the usual default — capable, and cheaper per run
    haiku    mechanical, well-specified work where turnaround is the win

  Pair a bigger model with a tighter max_runs_per_hour and a lower
  max_budget_usd. A wrong model name fails inside the run, not at startup, so
  "claude --help" is the authority on what is currently accepted.

TWO CLOCKS
  A POLL is curl-equivalent: it asks relay "do I have a task?" and runs no
  model, so an idle worker costs nothing however often it ticks.
  A RUN is one CLI session, and is the part that costs money.
  max_runs_per_hour limits RUNS, not polls. Lowering it does not make a worker
  check less often — it makes it act less often.

WHILE IT RUNS
  Ctrl-C            stop every worker, archive logs to logs/, remove live-workers/
  Pause one worker  touch live-workers/<name>/PAUSED
  Resume it         rm live-workers/<name>/PAUSED
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

    claude    SUPPORTED — the only runtime supported today. Its adapter is
              compiled in, and it is the one that produces the live session
              feed. Install the CLI separately: https://claude.com/claude-code
    codex     COMING SOON. Not offered yet: it is unverified against current
              codex builds and has no per-run spend cap, and shipping a runtime
              that cannot be bounded is not something this will do quietly. A
              worker asking for it is refused at startup, by name.
    <other>   nothing else is offered today. An unsupported "runtime" value is
              refused when the config loads, rather than failing inside a run
              you have already paid for.

  At startup, every worker's runtime must prove its CLI is installed and usable,
  and relay-cli refuses to start if one is not — a missing CLI is reported once,
  by name, rather than failing on every cycle in a background log. For claude it
  also checks that the installed build accepts the flags this adapter needs
  (streaming output, the MCP config, the spend cap); set
  RELAY_CLI_SKIP_RUNTIME_CHECK=1 to bypass that flag check.

EVERYTHING COSTS WHAT IT SAYS
  Polls are free and runs are not, and every ceiling here counts runs. The
  defaults bound you before you configure anything: 12 runs an hour, $5 inside
  one run, a 15-minute kill. Set any of them to 0 to remove it — deliberately.

  The one bound that is not yours to remove is the 5-second floor under
  poll_frequency_seconds. That one protects relay rather than you: an empty
  poll costs you nothing, but relay still has to answer it. A config below the
  floor is rejected instead of clamped.

  A worker also pauses ITSELF rather than failing forever: after repeated probe
  failures (a revoked credential, a dead host), after two spend-cap kills in a
  row, or when the same task has needed its attention across consecutive runs
  with nothing changing. Each explains its own fix in the worker's log.

Source and full documentation:
  https://github.com/wizdown/relay-cli
`

func usage(w *os.File) { fmt.Fprint(w, helpText) }

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
		fmt.Println("relay-cli " + version + " (" + channel + ")")
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
		fmt.Fprintf(os.Stderr, "error: %q is a flag, not a command. Did you mean:\n\n  relay-cli run %s\n\nRun \"relay-cli help\" for the full manual.\n",
			os.Args[1], strings.Join(os.Args[1:], " "))
		os.Exit(2)
	}
	fmt.Fprintf(os.Stderr, "error: unknown command %q. Commands are: init, check, run, version, help.\n", os.Args[1])
	os.Exit(2)
}

// errPlaceholderEndpoint marks a worker that still has the endpoint `init`
// wrote. It is not a failure to reach relay — it is a config nobody finished —
// and the two want different advice.
var errPlaceholderEndpoint = errors.New("mcp_endpoint is still the placeholder from `relay-cli init`")

// checkOpts is the check command's flags, split out for the same reason
// runOpts is: a test walks them and proves the manual still documents each one.
type checkOpts struct {
	configPath string
	timeout    int
}

func checkFlags(o *checkOpts) *flag.FlagSet {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `
usage: relay-cli check [--config PATH] [--timeout N]

  --config takes a config file or the directory holding one. Without it,
  .worker-config here is used, then relay-cli-workers/.worker-config.
  Run "relay-cli help" for the full manual.
`)
	}
	fs.StringVar(&o.configPath, "config", defaultConfigName, "worker list to check: a config file, or the directory holding one")
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
		fmt.Fprintf(os.Stderr, "error: unexpected argument %q. Run \"relay-cli help\" for usage.\n", fs.Arg(0))
		os.Exit(2)
	}

	if err := check(o.configPath, time.Duration(o.timeout)*time.Second, os.Stdout); err != nil {
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

	fmt.Fprintf(out, "relay-cli %s (%s) — checking %d worker(s) from %s\n", version, channel, len(cfg.Workers), cfg.Path)
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
		// A worker still carrying the placeholder from `relay-cli init` has
		// not been finished, and saying so beats spending a DNS timeout to
		// report that relay.example.com does not resolve.
		if strings.Contains(w.Endpoint, "REPLACE_ME") {
			results[i] = result{worker: w, err: errPlaceholderEndpoint}
			continue
		}
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

	failed, unauthorized, placeholder := 0, false, false
	for _, r := range results {
		if r.err != nil {
			failed++
			// Scrub: a probe error can quote the URL it failed on, and that URL
			// is the credential.
			msg := oneLine(Scrub(r.err.Error()), 160)
			switch {
			case errors.Is(r.err, errPlaceholderEndpoint):
				placeholder = true
			case strings.Contains(msg, "401"):
				unauthorized = true
			}
			fmt.Fprintf(out, "  %-24s FAIL  %s\n", r.worker.Name, msg)
			continue
		}
		fmt.Fprintf(out, "  %-24s ok    queue: resume %d · attention %d · todo %d\n",
			r.worker.Name, r.queue.Resume, r.queue.Attention, r.queue.Todo)
	}
	fmt.Fprintln(out)

	if failed > 0 {
		// The advice is conditional because the two failures have different
		// fixes, and offering both makes each one less believable: a 401 is a
		// credential to reissue, while a refused connection or a DNS miss is a
		// host to correct.
		hint := "       The endpoint could not be reached at all — check the host in mcp_endpoint,\n" +
			"       and that this machine can reach it."
		if placeholder {
			hint = "       Get a credential from relay — onboard_agent, then issue_agent_credential —\n" +
				"       and paste the whole connector_url it returns over the placeholder in\n" +
				"       mcp_endpoint. The secret is part of that URL and is shown only once."
		} else if unauthorized {
			hint = "       HTTP 401 means that worker's mcp_endpoint is wrong or its credential was\n" +
				"       revoked — issue a new one in relay (issue_agent_credential) and replace\n" +
				"       the whole URL, secret included."
		}
		return fmt.Errorf("%d of %d worker(s) could not reach relay.\n%s", failed, len(cfg.Workers), hint)
	}

	// A zero queue is the healthy answer here, and saying so is the point: it is
	// the reading people most often mistake for a failure.
	fmt.Fprintf(out, "all %d worker(s) ready. A queue of 0 means the credential works and there is\n"+
		"simply no work waiting. Nothing was launched and nothing was spent.\n", len(cfg.Workers))
	return nil
}

// runOpts is the run command's flags, split out from runCommand so a test can
// walk them and prove the manual above still documents every one.
type runOpts struct {
	configPath string
	port       int
	noOpen     bool
	noArchive  bool
	quiet      bool
}

func runFlags(o *runOpts) *flag.FlagSet {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	// A mistyped flag gets a short pointer, not the whole manual: dumping ninety
	// lines after a one-word typo buries the error that explains it.
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `
usage: relay-cli run [--config PATH] [--port N] [--no-open] [--quiet] [--no-archive]

  --config takes a config file or the directory holding one. Without it,
  .worker-config here is used, then relay-cli-workers/.worker-config.
  Run "relay-cli help" for the full manual.
`)
	}

	fs.StringVar(&o.configPath, "config", defaultConfigName, "worker list to run: a config file, or the directory holding one")
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
		fmt.Fprintf(os.Stderr, "error: unexpected argument %q. Run \"relay-cli help\" for usage.\n", fs.Arg(0))
		os.Exit(2)
	}

	if err := run(o.configPath, o.port, o.noOpen, o.noArchive, o.quiet); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(configPath string, port int, noOpen, noArchive, quiet bool) error {
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return err
	}

	liveDir := filepath.Join(cfg.PollerRoot, "live-workers")
	logsDir := filepath.Join(cfg.PollerRoot, "logs")

	if err := checkNothingElseRunning(liveDir); err != nil {
		return err
	}

	// Every start begins fresh: archive whatever a previous run left, then clear
	// the state directory. This is also what clears a PAUSED file, which the
	// breakers' messages promise.
	if err := archiveAndClear(liveDir, logsDir, noArchive); err != nil {
		return err
	}

	rules, rulesFile := loadWorkerRules(cfg.PollerRoot)

	bus := NewBus(!quiet)
	sup := &Supervisor{cfg: cfg, bus: bus, startedAt: time.Now().UTC()}

	for _, w := range cfg.Workers {
		rt, err := ResolveRuntime(w.Runtime, cfg.PollerRoot)
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

	fmt.Printf("relay-cli %s (%s) — %d worker(s) from %s\n", version, channel, len(cfg.Workers), cfg.Path)
	// Which CLI each worker will actually drive, and where it was found. relay-cli
	// bundles no CLI, so "which claude is this using?" is a question worth
	// answering before the first run rather than after a surprising one.
	for _, line := range runtimeBanner(cfg) {
		fmt.Println(line)
	}
	for _, w := range cfg.Workers {
		fmt.Printf("  %-24s runtime %-8s poll %gs  runs/h %d  repo %s\n",
			w.Name, w.Runtime, w.PollSeconds, w.MaxRunsPerHour, orDefault(w.RepoDir, "<none>"))
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

	// Keep the evidence. Everything else in live-workers/ is regenerable state
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
		rt, err := ResolveRuntime(w.Runtime, cfg.PollerRoot)
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

// checkNothingElseRunning refuses to start on top of a live fleet. Two pollers
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
		return fmt.Errorf("relay-cli is already running (pid %d). Stop it first", pid)
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
// removes live-workers/ entirely. The structured record goes with it: an
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
func loadWorkerRules(pollerRoot string) (string, string) {
	onDisk := filepath.Join(pollerRoot, "worker-rules.md")
	if b, err := os.ReadFile(onDisk); err == nil && len(b) > 0 {
		return string(b), onDisk
	}
	// The rules used to live under internal/. Silently falling back to the
	// embedded copy would quietly discard someone's edits — the one failure mode
	// of this override that gives no sign it happened.
	if legacy := filepath.Join(pollerRoot, "internal", "worker-rules.md"); fileHasContent(legacy) {
		fmt.Fprintf(os.Stderr, "warning: %s is no longer read — worker rules now live beside your\n"+
			"         .worker-config. The embedded copy is being used instead; to keep your\n"+
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
