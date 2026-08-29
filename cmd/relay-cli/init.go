// `relay-cli init` — create ~/.relay/ and write a starting config into it.
//
// The generated file is deliberately SHORT. It used to be the whole field
// reference, on the theory that someone who downloaded one binary has no
// checkout and no docs to read — but a template that documents every field is a
// second copy of the manual that drifts from the first. What a new user needs
// here is the two blanks to fill in and where to read the rest; that is what
// this writes.
//
// Comment support in the parser is what lets it annotate at all: the config is
// JSON, JSON has no comments, and stripLineComments exists so the file can
// explain itself where it is being edited.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// initConfigTemplate is one claude worker with the four required fields, the
// one required runtime setting, and nothing else.
//
// Both placeholders are obviously placeholders and both are rejected BY NAME by
// `relay-cli check`, so the two decisions this file cannot make — which agent,
// which repo — fail with the reason rather than with a parse error further in.
const initConfigTemplate = `{
  // ───────────────────────────────────────────────────────────────────────────
  //  ~/.relay/config — the workers ` + "`relay-cli run`" + ` will launch.
  //
  //  One worker = one relay agent identity × one repo checkout × one CLI.
  //  What an agent is FOR — its instructions, what it may decide alone — lives
  //  in relay as that agent's instructions_md, not here: that reaches a session
  //  already running, which a local file never could.
  //
  //  // comments are stripped before this file is parsed, so annotate freely.
  //  Full field reference: https://github.com/wizdown/relay-cli/blob/main/docs/configuration.md
  //
  //  NEVER COMMIT THIS FILE. The relay_mcp below is a live credential.
  // ───────────────────────────────────────────────────────────────────────────

  // How often every worker asks relay "any work?". A poll runs no model and
  // costs nothing. Optional, default 30, minimum 5.
  "poll_seconds": 30,

  "workers": [
    {
      // Unique, and filesystem-safe: it becomes ~/.relay/state/<name>/, which
      // is how you pause this one worker and find its logs. <repo>-<runtime>
      // reads well.
      "name": "my-repo-claude",

      // REPLACE THIS. The connector_url relay gave you, secret included.
      // Get one from relay: onboard_agent to create the agent, then
      // issue_agent_credential. It is shown EXACTLY ONCE.
      "relay_mcp": "https://relay.example.com/relay/mcp/c/wzh_REPLACE_ME",

      // REPLACE THIS. The checkout this agent works in — its AGENTS.md /
      // CLAUDE.md, skills and tooling are what the agent gets, so this decides
      // what the worker can actually do. "~" is expanded and it must exist.
      //
      // A headless run is fully autonomous and cannot answer an approval
      // prompt, so point this at a checkout you are willing to have rewritten,
      // not at your only copy of anything.
      "repo_dir": "` + repoDirPlaceholder + `",

      // Which CLI drives this worker. "claude" is the only one supported today.
      "runtime": "claude",

      // Optional. Maximum CLI LAUNCHES per rolling hour — not polls. This is
      // the ceiling that actually caps what you spend. Default 12, 0 for none.
      "max_runs_per_hour": 6,

      // Optional. Wall-clock kill for one session, enforced by relay-cli.
      // Default 900.
      "max_seconds_per_run": 900,

      // Settings the RUNTIME understands, in its own vocabulary. The keys here
      // depend on "runtime" above; anything it does not accept is refused when
      // the config loads.
      "runtime_config": {
        // REQUIRED for claude. opus | sonnet | haiku, or a full id such as
        // claude-opus-5 to pin an exact version. Required rather than
        // defaulted because the CLI's own default moves between versions, and
        // an unattended worker should say what it runs.
        "model": "sonnet",

        // Optional. Hard dollar cap INSIDE one run. Default 5, 0 removes it.
        // Two runs killed by this in a row pause the worker, because retrying
        // unchanged just spends it again.
        "max_usd_per_run": 5
      }
    }

    // Add more workers by copying the block above. Each needs its OWN name and
    // its OWN credential — never point two workers at one connector URL.
  ]
}
`

func initFlags() *flag.FlagSet {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `
usage: relay-cli init

  Creates ~/%s/ with a starting config in it. Takes no flags — there is one
  location and nothing points elsewhere.
  Run "relay-cli help" for the full manual.
`, relayDirName)
	}
	return fs
}

func initCommand(args []string) {
	fs := initFlags()

	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "error: unexpected argument %q. \"relay-cli init\" takes none — it always writes\n"+
			"       to ~/%s. Run \"relay-cli help\" for usage.\n", fs.Arg(0), relayDirName)
		os.Exit(2)
	}

	dir, err := RelayHome()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if err := initConfig(dir, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// initConfig creates the relay directory and writes the starting config — and
// refuses to overwrite.
//
// Refusing matters more here than it usually does: an existing config holds
// connector URLs whose secrets relay showed exactly once. Overwriting one does
// not lose a file you can rewrite, it loses credentials you have to reissue. So
// there is no --force, and no flag that could point this somewhere else by
// accident either.
func initConfig(dir string, out io.Writer) error {
	// ~/.relay existing as a FILE is rare and confusing, and MkdirAll would
	// report it as a bare "not a directory" from somewhere further in.
	if info, err := os.Stat(dir); err == nil && !info.IsDir() {
		return fmt.Errorf("%s exists and is a file, not a directory.\n"+
			"       relay-cli keeps its config, state and logs in a directory there.\n"+
			"       Move or remove that file, then run \"relay-cli init\" again.", dir)
	}

	configPath := filepath.Join(dir, configFileName)
	if _, err := os.Stat(configPath); err == nil {
		return fmt.Errorf("%s already exists, and overwriting it would destroy the\n"+
			"       connector credentials in it — relay shows each secret exactly once.\n"+
			"       Edit it instead, or move it aside if you want a fresh one.", configPath)
	}

	// 0700: this directory holds the credentials of every worker in the fleet.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	// 0600: this file is about to hold a credential. Creating it world-readable
	// and hoping the user tightens it later is the wrong default.
	if err := os.WriteFile(configPath, []byte(initConfigTemplate), 0o600); err != nil {
		return err
	}

	fmt.Fprintf(out, `created %s

Two placeholders to replace, and it runs:

  1. "relay_mcp" — get a credential from relay (https://relay.bytecurio.com/):
     add the agent (onboard_agent), then issue its credential
     (issue_agent_credential). Leave its capabilities off; a first worker needs
     none. The secret is embedded in the URL it returns and is shown EXACTLY
     ONCE — paste the whole URL over the placeholder.

  2. "repo_dir" — the checkout this agent should work in. Its AGENTS.md /
     CLAUDE.md, skills and tooling are what the agent gets, so this is the
     choice that decides what the worker can actually do. A headless run is
     fully autonomous and cannot answer an approval prompt, so point it at a
     checkout you are willing to have rewritten.

Then:

  relay-cli check    validates the file and tests the credential against relay.
                     Launches nothing and spends nothing, so this is the cheap
                     way to find a mistake. It reports every problem at once.

  relay-cli run      starts the workers and opens the dashboard on 127.0.0.1.

Both read %s. state/ and logs/ are created beside it when a fleet runs.

The defaults in the file are already bounded — 6 runs an hour, $5 inside one
run, a 15-minute kill — so a worker you barely configure is still a safe one.

NEVER COMMIT THIS FILE: the endpoint you are about to paste is a live
credential.
`, configPath, displayConfigPath())
	return nil
}
