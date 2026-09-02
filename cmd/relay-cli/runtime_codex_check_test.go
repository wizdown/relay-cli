package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// A help text offering everything this adapter uses.
const fullCodexHelp = `
Usage: codex exec [OPTIONS] [PROMPT]

  -m, --model <MODEL>
      --sandbox <MODE>          read-only, workspace-write, danger-full-access
      --json                    print events as JSON Lines
      --ephemeral               do not persist a session recording
      --skip-git-repo-check
      --ignore-user-config
  -c, --config <key=value>
`

func TestCodexNoMissingFlagsOnACompleteCLI(t *testing.T) {
	if got := missingCodexFlags([]byte(fullCodexHelp)); len(got) != 0 {
		t.Fatalf("a complete CLI must pass, got:\n%s", strings.Join(got, "\n"))
	}
}

// A missing flag has to be named with what relay-cli needed it FOR: the fix is
// upgrading the CLI, and "unsupported" alone does not tell anyone that.
func TestCodexMissingFlagsAreNamedWithTheirReason(t *testing.T) {
	help := strings.ReplaceAll(fullCodexHelp, "--json                    print events as JSON Lines", "")
	help = strings.ReplaceAll(help, "--ignore-user-config", "")
	got := strings.Join(missingCodexFlags([]byte(help)), "\n")
	for _, want := range []string{"--json", "live session feed", "--ignore-user-config", "personal MCP servers"} {
		if !strings.Contains(got, want) {
			t.Errorf("report is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "--sandbox") {
		t.Errorf("a flag the CLI has must not be reported missing:\n%s", got)
	}
}

// The startup check exists so a missing CLI is one line at launch rather than a
// failure per cycle in a background log.
func TestCodexCheckFailsWithoutTheCLI(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	err := (&codexRuntime{}).Check()
	if err == nil {
		t.Fatal("a missing codex must fail the check")
	}
	for _, want := range []string{"not found on PATH", "does not bundle"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q: %v", want, err)
		}
	}
}

// The bypass is for a CLI whose help text this adapter cannot read — not for one
// that is not installed.
func TestCodexSkipDoesNotSuppressAMissingCLI(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("RELAY_CLI_SKIP_RUNTIME_CHECK", "1")
	if err := (&codexRuntime{}).Check(); err == nil {
		t.Error("the bypass must not suppress a missing CLI")
	}
}

// `codex login status` reads a local file and spends nothing, which is why this
// adapter can answer a question the claude one cannot. A signed-out CLI has to
// fail the check with the command that fixes it.
func TestCodexLoginErrorNamesTheFix(t *testing.T) {
	dir := t.TempDir()
	// A stub CLI that reports "not logged in" the way the real one does.
	script := "#!/bin/sh\nif [ \"$1\" = login ]; then echo 'Not logged in'; exit 1; fi\nexit 0\n"
	if err := os.WriteFile(dir+"/codex", []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	err := codexLoginError()
	if err == nil {
		t.Fatal("a signed-out CLI must fail the check — every cycle would fail the same way")
	}
	for _, want := range []string{"codex login", "ChatGPT"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q: %v", want, err)
		}
	}
	// The credential is the operator's, and relay-cli must not read as though it
	// takes one over.
	if strings.Contains(err.Error(), "API key") && !strings.Contains(err.Error(), "no API key") {
		t.Errorf("a subscription sign-in should not be answered with an API key: %v", err)
	}
}

// runtime_config is validated against the runtime's own table, so a value the
// CLI would reject inside a paid run is refused when the config loads.
func TestCodexConfigIsValidatedAtLoad(t *testing.T) {
	noRuntimeCheck(t)
	base := `{
	  "name": "w",
	  "relay_mcp": "https://x/c/wzh_aaaaaaaa",
	  "repo_dir": %q,
	  "runtime": "codex",
	  "runtime_config": %s
	}`

	cases := []struct{ name, block, want string }{
		{"missing model", `{}`, `missing "model"`},
		{"bad effort", `{"model":"m","reasoning_effort":"maximum"}`, "it takes one of: minimal, low, medium, high, xhigh"},
		{"bad sandbox", `{"model":"m","sandbox":"workspace_write"}`, "it takes one of: read-only, workspace-write, danger-full-access"},
		{"quoted bool", `{"model":"m","network_access":"true"}`, "must be true or false, unquoted"},
		{"claude's cap", `{"model":"m","max_usd_per_run":5}`, `is not a setting runtime "codex" accepts`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := `{"workers":[` + fmtWorker(t, base, c.block) + `]}`
			err := loadErr(write(t, body))
			if err == nil {
				t.Fatalf("%s should be refused when the config loads", c.name)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error should say %q:\n%v", c.want, err)
			}
		})
	}
}

// The defaults are the whole point of the short config: a codex worker that
// states only its model is still a bounded one.
func TestCodexDefaultsResolve(t *testing.T) {
	noRuntimeCheck(t)
	body := `{"workers":[` + fmtWorker(t, `{
	  "name": "w",
	  "relay_mcp": "https://x/c/wzh_aaaaaaaa",
	  "repo_dir": %q,
	  "runtime": "codex",
	  "runtime_config": %s
	}`, `{"model":"gpt-5.1-codex"}`) + `]}`

	cfg, err := LoadConfig(write(t, body))
	if err != nil {
		t.Fatal(err)
	}
	rc := cfg.Workers[0].RuntimeConfig
	for key, want := range map[string]string{
		"reasoning_effort": "medium",
		"sandbox":          "workspace-write",
		"network_access":   "true",
		"web_search":       "true",
	} {
		if rc[key] != want {
			t.Errorf("%s = %q, want %q", key, rc[key], want)
		}
	}
}

// fmtWorker fills a worker template with a real repo_dir and one runtime_config
// block, so each case above is one deviation rather than five repeated fields.
func fmtWorker(t *testing.T, tmpl, block string) string {
	t.Helper()
	return fmt.Sprintf(tmpl, repoHere(t), block)
}
