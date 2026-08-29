package main

import (
	"flag"
	"fmt"
	"strings"
	"testing"
)

// The manual is the interface. A flag that exists but is not documented is a
// feature nobody can find, so this fails the build rather than letting the two
// drift apart.
func TestHelpDocumentsEveryRunFlag(t *testing.T) {
	var o runOpts
	runFlags(&o).VisitAll(func(f *flag.Flag) {
		if !strings.Contains(helpText, "--"+f.Name) {
			t.Errorf("run flag --%s is not mentioned in helpText", f.Name)
		}
	})
}

func TestHelpDocumentsEveryCommand(t *testing.T) {
	for _, cmd := range []string{"init", "run", "check", "version", "help"} {
		if !strings.Contains(helpText, "  "+cmd) {
			t.Errorf("command %q is not listed in helpText", cmd)
		}
	}
}

// The help quotes the defaults a worker gets when a field is omitted. Those
// numbers are what someone decides their spend ceiling against, so a silent
// drift between the manual and the code is worse than no manual.
func TestHelpQuotesTheRealDefaults(t *testing.T) {
	for _, want := range []string{
		fmt.Sprintf("default %g", defaultPollSeconds),
		fmt.Sprintf("default %d", defaultMaxRunsPerHour),
		fmt.Sprintf("default %g", defaultMaxBudgetUSD),
		fmt.Sprintf("default %d", defaultRunTimeoutSecs),
		`default "` + defaultRuntime + `"`,
	} {
		if !strings.Contains(helpText, want) {
			t.Errorf("helpText does not state %q — the manual has drifted from the code", want)
		}
	}
}

// Same contract as the run flags: a check flag that exists but is undocumented
// is a feature nobody can find.
func TestHelpDocumentsEveryCheckFlag(t *testing.T) {
	var o checkOpts
	checkFlags(&o).VisitAll(func(f *flag.Flag) {
		if !strings.Contains(helpText, "--"+f.Name) {
			t.Errorf("check flag --%s is not mentioned in helpText", f.Name)
		}
	})
}

func TestCheckFlagDefaults(t *testing.T) {
	var o checkOpts
	fs := checkFlags(&o)
	if err := fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
	if o.configPath != defaultConfigName || o.timeout != defaultCheckTimeoutSecs {
		t.Errorf("bare `check` should take every default, got %+v", o)
	}
	if !strings.Contains(helpText, fmt.Sprint(defaultCheckTimeoutSecs)) {
		t.Error("helpText should state the probe timeout default")
	}
}

func TestHelpDocumentsEveryInitFlag(t *testing.T) {
	var o initOpts
	initFlags(&o).VisitAll(func(f *flag.Flag) {
		if !strings.Contains(helpText, "--"+f.Name) {
			t.Errorf("init flag --%s is not mentioned in helpText", f.Name)
		}
	})
}

// The binary has to stand on its own: someone who downloaded it from a release
// page has no readme. The manual must carry the whole path from nothing to a
// running worker.
func TestHelpCarriesTheWholeGettingStartedPath(t *testing.T) {
	for _, want := range []string{
		"relay-cli init",         // make a config
		"issue_agent_credential", // get a credential
		"relay-cli check",        // verify it
		"relay-cli run",          // start
		"CHOOSING A MODEL",       // which model, without leaving the terminal
		"NEVER COMMIT",           // the irreversible mistake
	} {
		if !strings.Contains(helpText, want) {
			t.Errorf("the manual does not mention %q — a user with only the binary "+
				"would have to go looking elsewhere", want)
		}
	}
}

func TestRunFlagDefaultsMatchTheDocumentedOnes(t *testing.T) {
	var o runOpts
	runFlags(&o)
	if o.configPath != defaultConfigName {
		t.Errorf("--config default = %q, want %q", o.configPath, defaultConfigName)
	}
	if o.port != defaultPort {
		t.Errorf("--port default = %d, want %d", o.port, defaultPort)
	}
	if !strings.Contains(helpText, defaultConfigName) || !strings.Contains(helpText, fmt.Sprint(defaultPort)) {
		t.Error("helpText should name the config default and the port default")
	}
}

// 1.x would be a claim that the interface is settled. It is not, and a version
// bump is an easy thing to do without meaning it, so this says so out loud.
func TestVersionStaysOnZeroX(t *testing.T) {
	if !strings.HasPrefix(version, "0.") {
		t.Errorf("version is %q — leaving 0.x means declaring the interface stable. "+
			"If that is deliberate, delete this test in the same commit.", version)
	}
	if !strings.Contains(helpText, channel) {
		t.Errorf("helpText should say it is %s", channel)
	}
}

// Parsing must not require the flags: `relay-cli run` alone is the common case.
func TestRunParsesWithNoFlags(t *testing.T) {
	var o runOpts
	fs := runFlags(&o)
	if err := fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
	if o.configPath != defaultConfigName || o.noOpen || o.quiet || o.noArchive {
		t.Errorf("bare `run` should take every default, got %+v", o)
	}
}
