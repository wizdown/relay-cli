package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeCaffeinate writes an executable that records its argv and then blocks,
// which is the shape of the real thing: it holds the assertion until it is
// killed. Every branch below can then be walked on any OS, because the darwin
// check and the binary are both passed in.
func fakeCaffeinate(t *testing.T) (bin, argvFile string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake needs a POSIX shell")
	}
	dir := t.TempDir()
	bin = filepath.Join(dir, "caffeinate")
	argvFile = filepath.Join(dir, "argv")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argvFile + "\nwhile true; do sleep 1; done\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, argvFile
}

// waitFor polls until cond holds, so a test never depends on how fast a shell
// starts. A wall-clock sleep long enough to be safe on a loaded CI box is long
// enough to make the suite slow.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// The assertion is caffeinate -s, pinned to this process. -s is the whole
// promise of the flag: it applies on AC power only, so the flag cannot flatten
// a battery. -w is what releases the assertion if this process is killed
// outright and stop never runs.
func TestKeepAwakeRunsCaffeinateOnACPowerOnly(t *testing.T) {
	bin, argvFile := fakeCaffeinate(t)

	stop, note, err := startKeepAwakeOn("darwin", bin, 4242)
	if err != nil {
		t.Fatalf("startKeepAwakeOn: %v", err)
	}
	defer stop()

	waitFor(t, "the fake to record its argv", func() bool {
		b, err := os.ReadFile(argvFile)
		return err == nil && strings.Count(string(b), "\n") == 3
	})
	b, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Fields(string(b))
	want := []string{"-s", "-w", "4242"}
	if len(got) != len(want) {
		t.Fatalf("argv = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("argv = %q, want %q", got, want)
		}
	}
	if note == "" {
		t.Error("a held assertion should print a line saying so")
	}
	if !strings.Contains(note, "lid") {
		t.Errorf("the note should say the lid still sleeps the Mac, got %q", note)
	}
}

// The real one is pinned to the running process, which is what makes the -w
// safety net work. A test cannot assert that through the seam, so it asserts it
// on the wrapper's own arguments.
func TestKeepAwakePinsTheAssertionToThisProcess(t *testing.T) {
	bin, argvFile := fakeCaffeinate(t)

	stop, _, err := startKeepAwakeOn("darwin", bin, os.Getpid())
	if err != nil {
		t.Fatalf("startKeepAwakeOn: %v", err)
	}
	defer stop()

	waitFor(t, "the fake to record its argv", func() bool {
		b, err := os.ReadFile(argvFile)
		return err == nil && strings.Contains(string(b), strconv.Itoa(os.Getpid()))
	})
}

// A machine that is not a Mac, and a Mac with no caffeinate, both warn and hand
// back a usable stop. The fleet is the point; staying awake is a convenience.
func TestKeepAwakeWarnsRatherThanFailing(t *testing.T) {
	bin, _ := fakeCaffeinate(t)

	cases := map[string]struct {
		goos, bin string
		want      string
	}{
		"not a mac":      {"linux", bin, "macOS only"},
		"no caffeinate":  {"darwin", filepath.Join(t.TempDir(), "absent"), "does not have"},
		"not executable": {"darwin", writeNonExecutable(t), "does not have"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			stop, note, err := startKeepAwakeOn(tc.goos, tc.bin, os.Getpid())
			if err == nil {
				t.Fatal("expected a warning, got none")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("warning %q does not say %q", err, tc.want)
			}
			if !strings.Contains(err.Error(), "--keep-awake") {
				t.Errorf("warning %q does not name the flag that caused it", err)
			}
			if note != "" {
				t.Errorf("nothing was held, but the note claims otherwise: %q", note)
			}
			if stop == nil {
				t.Fatal("stop is nil; a caller has to be able to defer it unconditionally")
			}
			stop() // must not panic
			stop()
		})
	}
}

func writeNonExecutable(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "caffeinate")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// The assertion is released on shutdown, and stop is safe to call twice: it is
// deferred, and a second call must not kill a pid the OS has since reused.
func TestKeepAwakeStopReleasesTheAssertionOnce(t *testing.T) {
	bin, argvFile := fakeCaffeinate(t)

	stop, _, err := startKeepAwakeOn("darwin", bin, os.Getpid())
	if err != nil {
		t.Fatalf("startKeepAwakeOn: %v", err)
	}
	waitFor(t, "the fake to start", func() bool {
		_, err := os.Stat(argvFile)
		return err == nil
	})

	stop()
	stop()

	if n := countProcesses(t, bin); n != 0 {
		t.Errorf("%d caffeinate process(es) survived stop", n)
	}
}

// countProcesses answers "is anything still running this binary" without
// assuming a ps flag set. The fake's path is unique to the test's TempDir, so a
// match is this test's own child and nothing else on the machine.
func countProcesses(t *testing.T, bin string) int {
	t.Helper()
	out, err := exec.Command("ps", "-A", "-o", "args=").Output()
	if err != nil {
		t.Skipf("ps is unavailable here: %v", err)
	}
	n := 0
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, bin) {
			n++
		}
	}
	return n
}

// The flag defaults to off, and `relay run` with nothing after it stays exactly
// as it was: no assertion, no warning, no caffeinate.
func TestKeepAwakeIsOffByDefault(t *testing.T) {
	var o runOpts
	if err := runFlags(&o).Parse(nil); err != nil {
		t.Fatal(err)
	}
	if o.keepAwake {
		t.Error("--keep-awake should default to off")
	}
}

func TestKeepAwakeFlagParses(t *testing.T) {
	var o runOpts
	if err := runFlags(&o).Parse([]string{"--keep-awake"}); err != nil {
		t.Fatal(err)
	}
	if !o.keepAwake {
		t.Error("--keep-awake did not set the option")
	}
}

// The banner reports a held assertion on stdout and a refusal on stderr, and
// hands back a release either way. The second half is the flag's contract: a
// machine that cannot hold the assertion still runs the fleet.
func TestAnnounceKeepAwakeWarnsWithoutBlockingTheRun(t *testing.T) {
	t.Run("held", func(t *testing.T) {
		called := false
		swapStarter(t, func() (func(), string, error) {
			return func() { called = true }, awakeNote, nil
		})

		var out, errOut strings.Builder
		release := announceKeepAwake(&out, &errOut)

		if !strings.Contains(out.String(), awakeNote) {
			t.Errorf("stdout = %q, want the note", out.String())
		}
		if errOut.Len() > 0 {
			t.Errorf("a held assertion warned about nothing: %q", errOut.String())
		}
		release()
		if !called {
			t.Error("the release the banner returned does not reach the stop")
		}
	})

	t.Run("refused", func(t *testing.T) {
		stopped := false
		swapStarter(t, func() (func(), string, error) {
			return func() { stopped = true }, "", errors.New("--keep-awake needs /usr/bin/caffeinate, which this machine does not have")
		})

		var out, errOut strings.Builder
		release := announceKeepAwake(&out, &errOut)

		if !strings.Contains(errOut.String(), "warning:") {
			t.Errorf("stderr = %q, want a warning", errOut.String())
		}
		if !strings.Contains(errOut.String(), "--keep-awake") {
			t.Errorf("the warning does not name the flag: %q", errOut.String())
		}
		if out.Len() > 0 {
			t.Errorf("nothing was held, but stdout claims otherwise: %q", out.String())
		}
		if release == nil {
			t.Fatal("release is nil; the deferred call in run would panic")
		}
		release()
		release()
		if !stopped {
			t.Error("the release from a refused start does not reach the stop")
		}
	})
}

// A warning carries an OS error, and an OS error can carry anything. Every
// byte the binary prints goes through Scrub.
func TestAnnounceKeepAwakeScrubsWhatItPrints(t *testing.T) {
	const secret = "https://relay.example.com/relay/mcp/c/wzh_longsecrettoken"
	InstallSecrets([]*Worker{{Endpoint: secret}})
	defer InstallSecrets(nil)

	swapStarter(t, func() (func(), string, error) {
		return func() {}, "", fmt.Errorf("--keep-awake could not start: %s", secret)
	})
	var out, errOut strings.Builder
	announceKeepAwake(&out, &errOut)

	if strings.Contains(errOut.String(), "wzh_longsecrettoken") {
		t.Errorf("the warning leaked a credential: %q", errOut.String())
	}
}

func swapStarter(t *testing.T, f func() (func(), string, error)) {
	t.Helper()
	prev := keepAwakeStarter
	keepAwakeStarter = f
	t.Cleanup(func() { keepAwakeStarter = prev })
}
