// `relay init` — create ~/.relay/ and write a starting config into it.
//
// The generated file is deliberately SHORT. It used to be the whole field
// reference, on the theory that someone who downloaded one binary has no
// checkout and no docs to read — but a template that documents every field is a
// second copy of the manual that drifts from the first. What a new user needs
// here is the two blanks to fill in and where to read the rest; that is what
// this writes.
//
// "Where to read the rest" has to be a URL. The binary is downloadable on its
// own and the config lands in ~/.relay/, so the reader of this file may have no
// checkout for a relative docs/ path to point into.
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

// initConfigTemplate is one claude worker: the four required fields, the one
// required runtime setting, and the ceilings — annotated only where the file
// itself is the right place to say something.
//
// That last part is the whole editing rule for this string. A comment earns its
// line here if it is irreversible when missed (the credential, the directory the
// agent may rewrite) or unknowable at the point of editing (that JSON with
// comments parses at all). Everything else — what a poll costs, why "model" has
// no default, what trips the budget breaker — belongs in the reference, which is
// linked at the top. Rationale that lives in two places is rationale that will
// disagree with itself.
//
// Both placeholders are obviously placeholders and both are rejected BY NAME by
// `relay check`, so the two decisions this file cannot make — which agent, which
// repo — fail with the reason rather than with a parse error further in.
//
// Every link is a full URL: this file lands in ~/.relay/, where a relative
// docs/ path resolves to nothing, and its reader may never have cloned anything.
const initConfigTemplate = `{
  // ~/.relay/config — the workers ` + "`relay run`" + ` will launch.
  // NEVER COMMIT THIS FILE: each relay_mcp below becomes a live credential.
  // ` + "`relay check`" + ` validates it and tests every credential — launches
  //   nothing, spends nothing, reports every problem at once.
  // Fields, defaults, safeguards:
  //   ` + docsBase + `configuration.md
  // (// comments are stripped before parsing — annotate freely.)

  "poll_seconds": 30,             // how often a worker asks relay for work; runs no model

  "workers": [
    {
      "name": "worker-1",         // unique; becomes ~/.relay/state/<name>/

      // REPLACE — the connector_url relay issued, secret included.
      "relay_mcp": "https://relay.example.com/relay/mcp/c/wzh_REPLACE_ME",

      // REPLACE — where this agent works, and what it is free to rewrite: a
      // headless run cannot answer an approval prompt. How to prepare one:
      //   ` + docsBase + `working-directory.md
      "repo_dir": "` + repoDirPlaceholder + `",   // "~" is expanded; it must exist

      "runtime": "claude",        // or "codex"

      // Ceilings — delete any one and its default applies, bounded either way.
      "max_runs_per_hour": 6,     // CLI launches, not polls. Default 12.
      "max_seconds_per_run": 900, // wall-clock kill for one run

      "runtime_config": {         // claude's own vocabulary
        "model": "sonnet",        // required — pinned to claude-sonnet-5
        "max_usd_per_run": 5      // hard cap inside one run
      }
    }
    // More workers: copy the block. Each needs its own name and credential.
  ]
}
`

func initFlags() *flag.FlagSet {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `
usage: relay init

  Creates ~/%s/ with a starting config in it. Takes no flags — there is one
  location and nothing points elsewhere.
  Run "relay help" for the full manual.
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
		fmt.Fprintf(os.Stderr, "error: unexpected argument %q. \"relay init\" takes none — it always writes\n"+
			"       to ~/%s. Run \"relay help\" for usage.\n", fs.Arg(0), relayDirName)
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
			"       Move or remove that file, then run \"relay init\" again.", dir)
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

  2. "repo_dir" — the directory this agent should work in. Its CLAUDE.md,
     skills and tooling are what the agent gets, so this is the choice that
     decides what the worker can actually do. A headless run is fully autonomous
     and cannot answer an approval prompt, so point it somewhere you are willing
     to have rewritten. How to prepare one:
       `+docsBase+`working-directory.md

Then:

  relay check    validates the file and tests the credential against relay.
                 Launches nothing and spends nothing, so this is the cheap
                 way to find a mistake. It reports every problem at once.

  relay run      starts the workers and opens the dashboard on 127.0.0.1.

Both read %s. state/ and logs/ are created beside it when a fleet runs.

The defaults in the file are already bounded — 6 runs an hour, $5 inside one
run, a 15-minute kill — so a worker you barely configure is still a safe one.

NEVER COMMIT THIS FILE: the endpoint you are about to paste is a live
credential.
`, configPath, displayConfigPath())
	return nil
}
