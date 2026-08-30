package main

import (
	"flag"
	"fmt"
	"regexp"
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
	want := []string{
		fmt.Sprintf("default %g", defaultPollSeconds),
		fmt.Sprintf("default %d", defaultMaxRunsPerHour),
		fmt.Sprintf("default %d", defaultMaxSecondsPerRun),
	}
	// Runtime defaults are declared by the adapter, so the manual is checked
	// against what the runtime actually says rather than a second copy here.
	for _, rt := range supportedRuntimes() {
		for _, f := range rt.ConfigFields() {
			if f.Default != "" {
				want = append(want, "default "+f.Default)
			}
		}
	}
	for _, w := range want {
		if !strings.Contains(helpText, w) {
			t.Errorf("helpText does not state %q — the manual has drifted from the code", w)
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
	if o.timeout != defaultCheckTimeoutSecs {
		t.Errorf("bare `check` should take every default, got %+v", o)
	}
	if !strings.Contains(helpText, fmt.Sprint(defaultCheckTimeoutSecs)) {
		t.Error("helpText should state the probe timeout default")
	}
}

// init deliberately has no flags: there is one location and nothing points
// elsewhere. If one is ever added, the rule above applies to it too.
func TestInitTakesNoFlags(t *testing.T) {
	n := 0
	initFlags().VisitAll(func(f *flag.Flag) {
		n++
		if !strings.Contains(helpText, "--"+f.Name) {
			t.Errorf("init flag --%s is not mentioned in helpText", f.Name)
		}
	})
	if n > 0 {
		t.Errorf("init grew %d flag(s); the manual says it takes none", n)
	}
}

// The binary has to stand on its own: someone who downloaded it from a release
// page has no readme. The manual must carry the whole path from nothing to a
// running worker.
func TestHelpCarriesTheWholeGettingStartedPath(t *testing.T) {
	for _, want := range []string{
		"relay init",             // make a config
		"issue_agent_credential", // get a credential
		"relay check",            // verify it
		"relay run",              // start
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
	if o.port != defaultPort {
		t.Errorf("--port default = %d, want %d", o.port, defaultPort)
	}
	if !strings.Contains(helpText, fmt.Sprint(defaultPort)) {
		t.Error("helpText should name the port default")
	}
}

// There is one config location and no flag to move it, so the manual has to
// say where it is — it is the only way a user finds the file to edit.
func TestHelpNamesTheOneConfigLocation(t *testing.T) {
	for _, want := range []string{displayConfigPath(), "~/" + relayDirName + "/"} {
		if !strings.Contains(helpText, want) {
			t.Errorf("the manual never mentions %q, so nothing tells a user where "+
				"their config actually lives", want)
		}
	}
	if strings.Contains(helpText, "--config ") {
		t.Error("the manual still advertises --config; there is one location and " +
			"no flag that points elsewhere")
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

// The version constant is either a release (x.y.z) or the next one being worked
// towards (x.y.z-SNAPSHOT). Nothing else parses: the Makefile names artifacts
// from it, the release workflow compares a tag to it, and `make release` reads
// the marker to decide whether master is mid-release.
func TestVersionConstantIsWellFormed(t *testing.T) {
	shape := regexp.MustCompile(`^\d+\.\d+\.\d+(-SNAPSHOT)?$`)
	if !shape.MatchString(version) {
		t.Errorf("version is %q — it must be x.y.z, optionally with -SNAPSHOT while "+
			"master is between releases", version)
	}
	if got, want := baseVersion(), strings.TrimSuffix(version, "-SNAPSHOT"); got != want {
		t.Errorf("baseVersion() = %q, want %q", got, want)
	}
}

// The build stamp exists to distinguish the many unreleased trees that share one
// version constant — so it says nothing at all when it would only repeat the
// tag, and a released binary prints exactly what the docs show.
func TestVersionLineShowsTheBuildOnlyWhenItAdds(t *testing.T) {
	original := build
	t.Cleanup(func() { build = original })

	build = ""
	if got := versionLine(); strings.Contains(got, "[") {
		t.Errorf("with no build stamp, versionLine() = %q — it should be the version alone", got)
	}

	build = "v" + version
	if got := versionLine(); strings.Contains(got, "[") {
		t.Errorf("at the release tag, versionLine() = %q — the stamp only repeats the version", got)
	}

	build = "v0.0.9-4-gdeadbee"
	if got := versionLine(); !strings.Contains(got, "[v0.0.9-4-gdeadbee]") {
		t.Errorf("versionLine() = %q — a build between releases must name its commit", got)
	}
}

// Parsing must not require the flags: `relay run` alone is the common case.
func TestRunParsesWithNoFlags(t *testing.T) {
	var o runOpts
	fs := runFlags(&o)
	if err := fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
	if o.noOpen || o.quiet || o.noArchive {
		t.Errorf("bare `run` should take every default, got %+v", o)
	}
}
