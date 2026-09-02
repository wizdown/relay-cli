// Holding off sleep while a fleet runs.
//
// An idle poll loop looks exactly like an idle machine to macOS, so a laptop
// left running a fleet suspends between polls and the workers stop claiming
// work until someone touches it. `--keep-awake` holds one macOS power
// assertion for the life of the run.
//
// The assertion is `caffeinate -s`, not `-i`: `-s` applies only while the Mac
// is on AC power, so the flag cannot flatten a battery in a bag. Nothing here
// can beat clamshell sleep, which is why the flag's promise stops at the lid.
//
// The assertion is a separate process rather than IOKit through cgo. This
// binary is pure Go with no dependencies and cross-compiles, and a fresh clone
// has to pass its tests on a machine with no macOS in sight; linking a
// framework for one call would cost all three.
package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"sync"
)

// caffeinatePath is absolute rather than looked up on PATH. It ships at this
// path on every macOS, and a fleet that spends money should not run whichever
// "caffeinate" a modified PATH points at.
const caffeinatePath = "/usr/bin/caffeinate"

// awakeNote is what a held assertion prints. The lid is named because it is the
// limit of what the flag can promise.
const awakeNote = "keeping this Mac awake while it is on AC power. Closing the lid still sleeps it"

// keepAwakeStarter is the seam startKeepAwake reaches the banner through, so a
// test can hold the held-assertion branch on a machine that has no caffeinate.
var keepAwakeStarter = startKeepAwake

// announceKeepAwake starts the assertion and reports it on the startup banner.
//
// A failure is a warning and nothing more. The flag asks the machine for a
// convenience; the fleet is what the user typed `relay run` for, so it starts
// either way. The returned release is always safe to call.
func announceKeepAwake(out, errOut io.Writer) (release func()) {
	stop, note, err := keepAwakeStarter()
	if err != nil {
		fmt.Fprintf(errOut, "  warning: %s\n", Scrub(err.Error()))
		return stop
	}
	fmt.Fprintf(out, "  %s\n", Scrub(note))
	return stop
}

// startKeepAwake holds off system sleep until the returned stop is called.
//
// A failure is never fatal: the fleet is the point, and staying awake is a
// convenience the machine may not offer. Every path returns a usable stop, so a
// caller can defer it without checking the error first.
func startKeepAwake() (stop func(), note string, err error) {
	return startKeepAwakeOn(runtime.GOOS, caffeinatePath, os.Getpid())
}

// startKeepAwakeOn is startKeepAwake with the machine's answers passed in, so a
// test can walk every branch on any OS.
func startKeepAwakeOn(goos, bin string, pid int) (stop func(), note string, err error) {
	noop := func() {}

	if goos != "darwin" {
		return noop, "", fmt.Errorf("--keep-awake holds off sleep on macOS only, and this is %s. The fleet runs without it", goos)
	}
	// LookPath on an absolute path proves it exists AND is executable, which is
	// the pair that decides whether Start can work.
	if _, lookErr := exec.LookPath(bin); lookErr != nil {
		return noop, "", fmt.Errorf("--keep-awake needs %s, which this machine does not have. The fleet runs without it", bin)
	}

	// -w outlives a clean shutdown: if this process is killed outright and the
	// stop below never runs, caffeinate sees the pid go and releases the
	// assertion itself rather than pinning it until the next reboot.
	cmd := exec.Command(bin, "-s", "-w", strconv.Itoa(pid))
	if startErr := cmd.Start(); startErr != nil {
		return noop, "", fmt.Errorf("--keep-awake could not start %s: %v. The fleet runs without it", bin, startErr)
	}

	var once sync.Once
	stop = func() {
		once.Do(func() {
			// Kill then Wait: the child is a sleeper with nothing to flush, and
			// Wait is what reaps it rather than leaving a zombie behind.
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		})
	}
	return stop, awakeNote, nil
}
