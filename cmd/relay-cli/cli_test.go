package main

import (
	"bytes"
	"strings"
	"testing"
)

// The command surface, driven end to end through dispatch: what goes to which
// stream and with which exit status. These are the conventions AGENTS.md
// promises, held here so a new command or flag cannot quietly break them.

func runCLI(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = dispatch(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

// Asking for help is never an error: every form of it prints on stdout and
// exits 0, so `relay run --help | less` shows something.
func TestHelpIsOnStdoutAndExitsZero(t *testing.T) {
	for _, cmd := range commands {
		for _, args := range [][]string{{cmd, "--help"}, {cmd, "-h"}, {"help", cmd}} {
			code, out, errOut := runCLI(t, args...)
			if code != exitOK {
				t.Errorf("relay %s exited %d, want 0", strings.Join(args, " "), code)
			}
			if !strings.HasPrefix(out, "usage: "+synopsis[cmd]) {
				t.Errorf("relay %s did not print the synopsis on stdout:\n%s", strings.Join(args, " "), out)
			}
			if errOut != "" {
				t.Errorf("relay %s wrote to stderr:\n%s", strings.Join(args, " "), errOut)
			}
		}
	}
}

// A bare `relay`, `-h` and `--help` are one question, "which commands are
// there", and get the one-screen summary. `relay help` is the manual.
func TestSummaryAndManualRouting(t *testing.T) {
	for _, args := range [][]string{{}, {"-h"}, {"--help"}} {
		code, out, _ := runCLI(t, args...)
		if code != exitOK || out != shortHelp {
			t.Errorf("relay %s should print shortHelp and exit 0, got %d", strings.Join(args, " "), code)
		}
	}
	code, out, _ := runCLI(t, "help")
	if code != exitOK || out != helpText {
		t.Errorf("relay help should print the manual and exit 0, got %d", code)
	}
}

// A wrong command line is one line on stderr, prefixed `error:`, exit 2, and
// nothing on stdout. Nothing is silently ignored: an argument to a command
// that takes none is as wrong as an unknown flag.
func TestUsageErrorsAreOnStderrAndExitTwo(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"frob"}, `unknown command "frob"`},
		{[]string{"--port", "1"}, "Did you mean"},
		{[]string{"run", "--bogus"}, `"relay run" does not take --bogus`},
		{[]string{"run", "--port", "abc"}, `--port takes a number, not "abc"`},
		{[]string{"run", "--port"}, "--port needs a value"},
		{[]string{"check", "extra"}, `"relay check" takes no arguments`},
		{[]string{"init", "extra"}, `"relay init" takes no arguments`},
		{[]string{"version", "--json"}, `"relay version" takes no arguments`},
		{[]string{"help", "frob"}, `unknown command "frob"`},
		{[]string{"help", "run", "check"}, "takes one command name"},
	} {
		code, out, errOut := runCLI(t, tc.args...)
		if code != exitUsage {
			t.Errorf("relay %s exited %d, want 2", strings.Join(tc.args, " "), code)
		}
		if !strings.HasPrefix(errOut, "error: ") || !strings.Contains(errOut, tc.want) {
			t.Errorf("relay %s: stderr should start with error: and say %q:\n%s", strings.Join(tc.args, " "), tc.want, errOut)
		}
		if out != "" {
			t.Errorf("relay %s wrote to stdout on a usage error:\n%s", strings.Join(tc.args, " "), out)
		}
	}
}

// `relay version` is one line, and the version flags are its aliases.
func TestVersionAliases(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"-v"}, {"--version"}} {
		code, out, errOut := runCLI(t, args...)
		if code != exitOK || strings.TrimSpace(out) != versionLine() || errOut != "" {
			t.Errorf("relay %s: code %d, stdout %q, stderr %q", strings.Join(args, " "), code, out, errOut)
		}
	}
}
