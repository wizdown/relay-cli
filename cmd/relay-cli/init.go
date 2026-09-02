// `relay init` — create ~/.relay/ and write a starting config into it.
//
// The generated file is deliberately SHORT. It used to be the whole field
// reference, on the theory that someone who downloaded one binary has no
// checkout and no docs to read — but a template that documents every field is a
// second copy of the manual that drifts from the first. What a new user needs
// here is the blanks to fill in and where to read the rest; that is what this
// writes.
//
// "Where to read the rest" has to be a URL. The binary is downloadable on its
// own and the config lands in ~/.relay/, so the reader of this file may have no
// checkout for a relative docs/ path to point into.
//
// What it writes depends on the machine. relay-cli bundles no CLI, and a worker
// naming a runtime that is not installed fails the start — so a fixed template
// would hand half its readers a config that cannot run. Instead every supported
// runtime gets a worker: installed ones live, the rest commented out with the
// reason. Both shapes are in front of you, and only a runtime that exists here
// is asked to run.
//
// Comment support in the parser is what lets it annotate at all: the config is
// JSON, JSON has no comments, and stripLineComments exists so the file can
// explain itself where it is being edited — and so a worker can be shipped
// commented out and turned on by deleting four characters a line.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// initHeader opens the file, and carries the only two things it says once: the
// reference to read, and what the REPLACE markers below mean.
//
// That is the whole editing rule for these strings. A comment earns its line
// here if it is irreversible when missed (the directory the agent may rewrite)
// or unknowable at the point of editing. Everything else — what a poll costs,
// why "model" has no default, what trips the budget breaker — belongs in the
// reference, which is linked here. Rationale that lives in two places is
// rationale that will disagree with itself.
//
// Every link is a full URL: this file lands in ~/.relay/, where a relative
// docs/ path resolves to nothing, and its reader may never have cloned
// anything.
const initHeader = `{
  // ~/.relay/config — the workers ` + "`relay run`" + ` launches. Every field:
  //   ` + docsBase + `configuration.md
  // REPLACE, in each worker: "relay_mcp" is the connector_url relay issued —
  // a secret, and never shared between two workers. "repo_dir" is where the
  // agent works and what it is free to rewrite:
  //   ` + docsBase + `working-directory.md

  "poll_seconds": 30,             // how often a worker asks relay for work; runs no model

  "workers": [
`

const initFooter = `    // Delete a worker you do not want; copy one to add another. Each needs
    // its own name and its own credential.
  ]
}
`

// initWorkerBlocks is the starting worker for each supported runtime, indented
// as an entry in "workers" and carrying no separating comma — renderInitConfig
// places that, because where it goes depends on which of these is live.
//
// Both placeholders are obviously placeholders and both are rejected BY NAME
// when the config loads, so the decisions this file cannot make — which agent,
// which repo — fail with the reason rather than with a parse error further in.
var initWorkerBlocks = map[string]string{
	"claude": `    {
      "name": "worker-claude",    // unique; becomes ~/.relay/state/<name>/
      "relay_mcp": "https://relay.example.com/relay/mcp/c/wzh_REPLACE_ME", // REPLACE
      "repo_dir": "` + repoDirPlaceholder + `", // REPLACE
      "runtime": "claude",
      "max_runs_per_hour": 6,     // CLI launches, not polls. Default 12.
      "max_seconds_per_run": 900, // wall-clock kill for one run
      "runtime_config": {
        "model": "sonnet",        // required — pinned to claude-sonnet-5
        "max_usd_per_run": 5      // hard cap inside one run
      }
    }`,
	"codex": `    {
      "name": "worker-codex",     // unique; becomes ~/.relay/state/<name>/
      "relay_mcp": "https://relay.example.com/relay/mcp/c/wzh_REPLACE_ME_2", // REPLACE
      "repo_dir": "` + repoDirPlaceholder + `", // REPLACE
      "runtime": "codex",
      "max_runs_per_hour": 6,     // CLI launches, not polls. Default 12.
      "max_seconds_per_run": 900, // codex has no spend cap — this is the bound
      "runtime_config": {
        "model": "terra",         // required — pinned to gpt-5.6-terra
        "reasoning_effort": "medium"
      }
    }`,
}

// initInstallHints name each CLI the way its own vendor does, since "install
// it" is only actionable with the name and the link.
var initInstallHints = map[string]struct{ Name, URL string }{
	"claude": {"Claude Code", "https://claude.com/claude-code"},
	"codex":  {"the Codex CLI", "https://developers.openai.com/codex/cli"},
}

// installedRuntimes reports which supported runtimes have their CLI on this
// machine, in supportedRuntimes() order.
//
// A variable for the same reason checkRuntime is one: `go test ./...` has to
// pass on a clone with no coding CLI installed, and a starting config whose
// shape depends on what happens to be on PATH is otherwise untestable — the
// tests would assert one thing on a laptop and another on CI. Nothing but a
// test reassigns it.
//
// It asks "is it here", not Check()'s "will it run": a signed-out CLI is
// installed, and telling someone to install what they already have would be
// the wrong fix. Sign-in is checked where it can be acted on, by `relay check`
// and `relay run`, which name the login command.
var installedRuntimes = func() []string {
	var found []string
	for _, rt := range supportedRuntimes() {
		if loc, ok := rt.(cliLocator); ok && loc.Installed() {
			found = append(found, rt.Name())
		}
	}
	return found
}

// renderInitConfig builds the starting config for a machine where exactly
// `installed` runtimes were found.
//
// Live workers come first and commented-out ones last, always — which is what
// lets the comma that separates two entries ride INSIDE the comment. A block
// commented out ahead of a live one would need a trailing comma instead, and
// getting that wrong produces invalid JSON at the moment someone uncomments,
// which is the one moment this file has to be right.
func renderInitConfig(installed []string) string {
	live, missing := splitByInstalled(installed)

	var b strings.Builder
	b.WriteString(initHeader)
	for i, name := range live {
		b.WriteString(initWorkerBlocks[name])
		if i < len(live)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	for _, name := range missing {
		b.WriteString(commentedWorker(name, live))
	}
	b.WriteString(initFooter)
	return b.String()
}

// splitByInstalled partitions the supported runtimes into the ones found here
// and the ones not, keeping supportedRuntimes() order so the file reads the
// same way for everyone.
func splitByInstalled(installed []string) (live, missing []string) {
	for _, rt := range supportedRuntimes() {
		name := rt.Name()
		found := false
		for _, in := range installed {
			if in == name {
				found = true
				break
			}
		}
		if found {
			live = append(live, name)
		} else {
			missing = append(missing, name)
		}
	}
	return live, missing
}

// commentedWorker writes a worker for a runtime this machine does not have:
// the same block, commented, above a line saying why it is off and what turns
// it on. Shipping it commented rather than omitting it is the difference
// between "relay-cli runs codex too" being discoverable and being a docs page
// nobody opens.
func commentedWorker(name string, live []string) string {
	hint := initInstallHints[name]
	note := fmt.Sprintf("    // Only %s is installed here, so this %s worker is commented out\n"+
		"    // rather than left to fail the start — `relay run` refuses a runtime it\n"+
		"    // cannot find. Install %s, sign in, then uncomment it:\n"+
		"    //   %s\n",
		strings.Join(live, " and "), name, hint.Name, hint.URL)
	return note + commentOut(initWorkerBlocks[name])
}

// commentOut turns a worker block into comments, carrying the comma that
// separates it from the block above inside the comment — so uncommenting the
// block is the whole edit, with no punctuation left to fix.
func commentOut(block string) string {
	var out strings.Builder
	for i, line := range strings.Split(block, "\n") {
		body := strings.TrimPrefix(line, "    ")
		if i == 0 {
			body = "," + body
		}
		out.WriteString("    // " + body + "\n")
	}
	return out.String()
}

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

	// Last of the three refusals, and like the two above it creates nothing —
	// so installing a CLI and running init again meets an empty ~/ rather than
	// the overwrite refusal. It comes after them because a config that already
	// exists is the more useful thing to be told about: that file is not going
	// to be written whatever is on PATH.
	installed := installedRuntimes()
	if len(installed) == 0 {
		return fmt.Errorf("no coding CLI found on PATH, and a worker is one.\n" +
			"       relay-cli drives a CLI you install separately — it bundles none, so a\n" +
			"       worker with no runtime has nothing to run. Install either:\n" +
			"         Claude Code   https://claude.com/claude-code\n" +
			"         Codex CLI     https://developers.openai.com/codex/cli\n" +
			"       Sign it in, then run \"relay init\" again. Nothing was written.")
	}

	// 0700: this directory holds the credentials of every worker in the fleet.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	// 0600: this file is about to hold a credential. Creating it world-readable
	// and hoping the user tightens it later is the wrong default.
	if err := os.WriteFile(configPath, []byte(renderInitConfig(installed)), 0o600); err != nil {
		return err
	}

	live, missing := splitByInstalled(installed)
	fmt.Fprintf(out, `created %s

%s

Two placeholders per worker to replace, and it runs:

  1. "relay_mcp" — get a credential from relay (https://relay.bytecurio.com/):
     add the agent (onboard_agent), then issue its credential
     (issue_agent_credential). Leave its capabilities off; a first worker needs
     none. The secret is embedded in the URL it returns and is shown EXACTLY
     ONCE — paste the whole URL over the placeholder. One per worker: two
     workers may never share a credential.

  2. "repo_dir" — the directory this agent should work in. Its CLAUDE.md or
     AGENTS.md, skills and tooling are what the agent gets, so this is the
     choice that decides what the worker can actually do. A headless run is
     fully autonomous and cannot answer an approval prompt, so point it
     somewhere you are willing to have rewritten. How to prepare one:
       `+docsBase+`working-directory.md

Then:

  relay check    validates the file and tests every credential against relay.
                 Launches nothing and spends nothing, so this is the cheap
                 way to find a mistake. It reports every problem at once.

  relay run      starts the workers and opens the dashboard on 127.0.0.1.

Both read %s. state/ and logs/ are created beside it when a fleet runs.

The defaults in the file are already bounded — 6 runs an hour, $5 inside one
claude run, a 15-minute kill — so a worker you barely configure is still a safe
one.

NEVER COMMIT THIS FILE: the endpoint you are about to paste is a live
credential.
`, configPath, initSummary(live, missing), displayConfigPath())
	return nil
}

// initSummary says what was actually written, since that now depends on the
// machine: someone who reads "one worker per runtime" and finds two is owed the
// reason, and someone whose second worker arrived commented out has to be told
// it is there.
func initSummary(live, missing []string) string {
	s := fmt.Sprintf("It has one worker per coding CLI found here: %s.", strings.Join(live, " and "))
	if len(missing) > 0 {
		s += fmt.Sprintf("\n%s was not found on PATH, so a worker for it is written below,\ncommented out — install the CLI and uncomment it to run both.",
			strings.Join(missing, " and "))
	}
	return s
}
