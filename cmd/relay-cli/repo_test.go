package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Lint for the repository itself: the rules in AGENTS.md about what the tree
// holds and how the binary exits. The pre-commit hook catches the first at
// commit time; this is the backstop for a clone where nobody ran `make hooks`.

// maxTrackedFileBytes is the ceiling on a tracked file. The largest source
// file is a sixth of it; a Go binary is ten times it.
const maxTrackedFileBytes = 1 << 20

// Executable magic numbers: Mach-O (both byte orders, 32 and 64 bit, and the
// universal header) and ELF.
var executableMagic = [][]byte{
	{0xfe, 0xed, 0xfa, 0xce}, {0xce, 0xfa, 0xed, 0xfe},
	{0xfe, 0xed, 0xfa, 0xcf}, {0xcf, 0xfa, 0xed, 0xfe},
	{0xca, 0xfe, 0xba, 0xbe},
	{0x7f, 'E', 'L', 'F'},
}

// A build output committed by accident is pulled by every clone from then on,
// and a 10 MB binary is not something a review of a diff notices. Every
// tracked file is small and none is an executable image.
func TestNoBuildOutputIsTracked(t *testing.T) {
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("not a git checkout, or git is unavailable: %v", err)
	}
	for _, rel := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		if rel == "" {
			continue
		}
		path := filepath.Join(repoRoot, rel)
		info, err := os.Stat(path)
		if err != nil {
			continue // deleted in the working tree; the index still lists it
		}
		if info.Size() > maxTrackedFileBytes {
			t.Errorf("%s is %d bytes, over the %d-byte ceiling for a tracked file. "+
				"If it is a build output, git rm --cached it and add it to .gitignore.", rel, info.Size(), maxTrackedFileBytes)
			continue
		}
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		head := make([]byte, 4)
		n, _ := f.Read(head)
		f.Close()
		for _, magic := range executableMagic {
			if n == 4 && bytes.Equal(head, magic) {
				t.Errorf("%s is an executable image. Build outputs are not tracked: "+
					"git rm --cached it and add it to .gitignore.", rel)
			}
		}
	}
}

// The exit status is an interface scripts branch on, so it is three named
// constants and one call site. Everything else returns a code to dispatch,
// which is what lets cli_test.go drive the whole surface without a process.
func TestExitStatusesAreConstantsAndMainIsTheOnlyExit(t *testing.T) {
	src := nonTestSource(t)
	for _, literal := range []string{"os.Exit(0)", "os.Exit(1)", "os.Exit(2)"} {
		if strings.Contains(src, literal) {
			t.Errorf("a non-test file calls %s; use exitOK, exitFail or exitUsage", literal)
		}
	}
	if n := strings.Count(src, "os.Exit("); n != 1 {
		t.Errorf("os.Exit is called %d times outside tests; main() is the one place. "+
			"Return the status to dispatch instead.", n)
	}
}
