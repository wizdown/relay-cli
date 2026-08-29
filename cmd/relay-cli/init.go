// `relay-cli init` — write a starting .worker-config into the current
// directory.
//
// The generated file is the documentation. Someone who downloaded one binary
// from a release page has no checkout, no readme and no example to copy, so the
// annotated config plus `relay-cli help` has to be everything they need. That
// is what the comment support in the parser is for: .worker-config is JSON, JSON
// has no comments, and stripLineComments exists precisely so this file can
// explain itself where it is being edited.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// workspacePlaceholder is replaced with the absolute path of the workspace
// directory when the template is written. The generated config states where the
// worker runs rather than relying on a default, because "where did it write
// that file?" should be answerable by reading the config.
const workspacePlaceholder = "__WORKSPACE_DIR__"

// initConfigTemplate is one claude worker with every field spelled out.
//
// Every field appears here on purpose, even the ones most people never set: for
// a standalone user this is the field reference, and a field they cannot see is
// a field they cannot use. A drift test asserts it stays complete.
//
// The endpoint is an obvious placeholder. It has to be replaced before this
// config does anything, and `relay-cli check` says so in plain words rather
// than failing somewhere further in.
const initConfigTemplate = `{
  // ───────────────────────────────────────────────────────────────────────────
  //  .worker-config — the workers ` + "`relay-cli run`" + ` will launch.
  //
  //  One worker = one relay agent identity × one repo checkout × one CLI.
  //  You do not route tasks to repos or to CLIs: you delegate a task to an
  //  AGENT in relay, and that agent's worker already decides where it runs and
  //  what runs it.
  //
  //  // comments are stripped before this file is parsed, so annotate freely.
  //
  //  NEVER COMMIT THIS FILE. The mcp_endpoint below is a live credential.
  // ───────────────────────────────────────────────────────────────────────────

  "relay_workers": [
    {
      // REQUIRED. Unique. Becomes live-workers/<name>/, so keep it
      // filesystem-safe: no "/", no spaces. It is how you pause this one
      // worker and find its logs, so name it after the identity —
      // <repo>-<runtime> reads well.
      "name": "my-repo-claude",

      // REQUIRED. Unique. The connector_url relay gave you, secret included.
      //
      // Get one from relay: onboard_agent to create the agent, then
      // issue_agent_credential to get this URL. It is shown EXACTLY ONCE.
      // Replace this whole line, including the secret at the end.
      "mcp_endpoint": "https://relay.example.com/relay/mcp/c/wzh_REPLACE_ME",

      // Optional, default "claude". Which CLI drives this worker. "claude" is
      // the only supported runtime today; anything else resolves to
      // runtimes/<runtime>.sh beside this file.
      "runtime": "claude",

      // The directory this worker's CLI runs in, so that repo's AGENTS.md /
      // CLAUDE.md, skills and tooling load exactly as they would for you.
      // "~" is expanded, and the directory must EXIST at startup.
      //
      // "relay-cli init" created the one below and pointed this at it, so a
      // new worker has somewhere to work before you have decided anything.
      // Change it to a checkout of yours whenever you want real work done — but
      // a headless run is fully autonomous in here and cannot answer an approval
      // prompt, so point it at a checkout you are willing to have rewritten,
      // not at your only copy of anything.
      //
      // Give a second worker its OWN directory. Two agents working in one
      // directory will overwrite each other.
      "repo_dir": "__WORKSPACE_DIR__",

      // Optional, default: the CLI's own default model. Passed through
      // verbatim. claude takes opus | sonnet | haiku, or a full id such as
      // claude-opus-5 to pin an exact version.
      //
      // Omitting this is a fine choice — it tracks the CLI's own
      // recommendation. Otherwise: the largest model for open-ended work or a
      // wide blast radius, the smallest for mechanical, well-specified work.
      "model": "sonnet",

      // Optional, default 30. Seconds between POLLS. A poll is one short HTTP
      // request asking relay "any work?" — no CLI, no model, ZERO TOKENS — so
      // an idle worker is free however often it ticks. There is little reason
      // to go below ~15s, and slowing this down is NOT how you save money.
      // MINIMUM 5: below that the config is rejected. A poll is free for you,
      // but it is still a request relay has to answer.
      // Must be a JSON number: 30, not "30".
      "poll_frequency_seconds": 30,

      // Optional, default 12. Maximum CLI LAUNCHES per rolling hour — not
      // polls. This is the only ceiling on how many sessions may start, so it
      // is the one that actually caps what you spend. Start low. Set 0 for no
      // ceiling, which is deliberate and unbounded.
      "max_runs_per_hour": 6,

      // Optional, default 5. Hard dollar cap INSIDE ONE run — not across runs.
      // claude only. Set 0 to remove it. Two runs killed by this cap in a row
      // pause the worker, because retrying unchanged just spends it again.
      "max_budget_usd": 5,

      // Optional, default 900. Wall-clock kill for ONE session, enforced by
      // relay-cli rather than by the CLI. A hung session holds both this
      // worker's lock and the task's relay lease until it is killed.
      "run_timeout_seconds": 900,

      // Optional, no default. Raw extra flags appended to the CLI invocation.
      // An escape hatch — prefer a field above. Word-split, so an argument
      // containing a space cannot be expressed.
      "runtime_args": ""
    }

    // Add more workers by copying the block above. Each needs its OWN name and
    // its OWN credential — never point two workers at one connector URL.
    //
    // What an agent is FOR — its instructions, what it may decide alone, and
    // whether it can split work into subtasks and delegate them — is set on the
    // agent in relay (update_agent), not here. That is what reaches a session
    // already running.
  ]
}
`

type initOpts struct {
	configPath string
}

func initFlags(o *initOpts) *flag.FlagSet {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `
usage: relay-cli init [--config PATH]

  Creates relay-cli-workers/ here: an annotated .worker-config and an
  agent-workspace/ for the worker to run in.
  --config takes a directory, or the config file itself.
  Run "relay-cli help" for the full manual.
`)
	}
	fs.StringVar(&o.configPath, "config", homeDirName, "where to create it: a directory, or the config file itself")
	return fs
}

func initCommand(args []string) {
	var o initOpts
	fs := initFlags(&o)

	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "error: unexpected argument %q. Run \"relay-cli help\" for usage.\n", fs.Arg(0))
		os.Exit(2)
	}

	if err := initConfig(o.configPath, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// initTarget decides what --config asked for: a path ending in .worker-config
// is that file, and anything else is the directory to put one in. Both forms
// name the same place for the workspace, which goes beside the config.
func initTarget(path string) (configPath, dir string) {
	dir = path
	if filepath.Base(path) == defaultConfigName {
		dir = filepath.Dir(path)
	}
	return filepath.Join(dir, defaultConfigName), dir
}

// initConfig creates the poller root, the workspace beside it, and the config
// pointing at that workspace — and refuses to overwrite.
//
// Refusing matters more here than it usually does: an existing .worker-config
// holds connector URLs whose secrets relay showed exactly once. Overwriting one
// does not lose a file you can rewrite, it loses credentials you have to reissue.
// So there is no --force; writing somewhere else is the escape hatch.
func initConfig(path string, out io.Writer) error {
	configPath, dir := initTarget(path)
	workspace := filepath.Join(dir, workspaceDirName)

	if _, err := os.Stat(configPath); err == nil {
		abs, _ := filepath.Abs(configPath)
		return fmt.Errorf("%s already exists, and overwriting it would destroy the\n"+
			"       connector credentials in it — relay shows each secret exactly once.\n"+
			"       Edit it, or write the template somewhere else:\n"+
			"         relay-cli init --config %s.new", abs, dir)
	}

	// 0700: this directory holds the credentials of every worker in the fleet.
	// MkdirAll on the workspace creates the poller root above it too.
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		return err
	}

	absWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		absWorkspace = workspace
	}

	// 0600: this file is about to hold a credential. Creating it world-readable
	// and hoping the user tightens it later is the wrong default.
	body := strings.Replace(initConfigTemplate, workspacePlaceholder, absWorkspace, 1)
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		return err
	}

	// This directory is often created inside a repo, and one `git add -A` is
	// all it takes to publish a live credential. "*" ignores the whole
	// directory, this file included. An existing one is left alone.
	gitignore := filepath.Join(dir, ".gitignore")
	if _, err := os.Stat(gitignore); os.IsNotExist(err) {
		if err := os.WriteFile(gitignore, []byte("*\n"), 0o600); err != nil {
			return err
		}
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}

	fmt.Fprintf(out, `created %s

  .worker-config     ONE claude worker, every field annotated. 0600: it is
                     about to hold a live credential.
  %s/   what that worker's CLI runs in — already its repo_dir.
  .gitignore         so none of this can be committed by accident.

// comments are allowed in the config and are stripped before it is parsed, so
the file explains itself where you are editing it.

Next:

  1. Get a relay credential for this worker.
     In relay (https://relay.bytecurio.com/): add the agent (onboard_agent),
     then issue its credential (issue_agent_credential). Leave its capabilities
     off; a first worker needs none.
     The secret is embedded in the URL it returns and is shown EXACTLY ONCE.

  2. Paste it over the "mcp_endpoint" placeholder.
     That is the only edit a working worker needs. "repo_dir" already points at
     the workspace above; change it when you want the worker in a checkout of
     yours instead — and only one you are willing to have rewritten.

  3. relay-cli check
     Validates the file and tests the credential against relay. It launches
     nothing and spends nothing, so this is the cheap way to find a mistake.

  4. relay-cli run
     Starts the worker and opens the dashboard on 127.0.0.1.

Both find this config on their own from the directory you ran init in.

The defaults in the file are already bounded — 6 runs an hour, $5 inside one
run, a 15-minute kill — so a worker you barely configure is still a safe one.

NEVER COMMIT THIS FILE: the endpoint you are about to paste is a live
credential.
`, abs, workspaceDirName)
	return nil
}
