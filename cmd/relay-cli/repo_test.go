package main

import (
	"bytes"
	"encoding/json"
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

// The agent configuration under .claude/ is the repo's own copy of what
// docs/working-directory.md tells a user to give their agent. None of it is
// compiled, so nothing else would notice a skill with no trigger, an agent
// file with no description, or a settings.json with a trailing comma — and the
// failure mode is silent: the session simply does not load it.
//
// The link checker in docs_pages_test.go already walks these files, so a rotted
// link fails too. This is the part it cannot see.

const claudeDir = repoRoot + "/.claude"

// frontMatterField reads one `key: value` line from a markdown file's leading
// --- block. Empty when the file has no front matter or no such key. Line-based
// on purpose: the repo has no third-party dependencies, and front matter this
// simple does not need a YAML parser.
func frontMatterField(body, key string) string {
	if !strings.HasPrefix(body, "---\n") {
		return ""
	}
	end := strings.Index(body[4:], "\n---")
	if end < 0 {
		return ""
	}
	for _, line := range strings.Split(body[4:4+end], "\n") {
		if rest, ok := strings.CutPrefix(line, key+":"); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

func TestAgentConfigIsWellFormed(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	if err != nil {
		t.Fatalf("cannot read .claude/settings.json: %v", err)
	}
	var settings struct {
		Hooks       map[string]any `json:"hooks"`
		Permissions struct {
			Allow []string `json:"allow"`
			Deny  []string `json:"deny"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(body, &settings); err != nil {
		t.Fatalf(".claude/settings.json does not parse: %v.\n"+
			"It is strict JSON — no comments, no trailing comma. The notes live in .claude/README.md.", err)
	}
	if _, ok := settings.Hooks["SessionStart"]; !ok {
		t.Error(".claude/settings.json has no SessionStart hook. It is what installs the git " +
			"hooks in a fresh clone, which is every agent session.")
	}
	if len(settings.Permissions.Deny) == 0 {
		t.Error(".claude/settings.json denies nothing. ~/.relay/config holds live credentials.")
	}

	skills, err := filepath.Glob(filepath.Join(claudeDir, "skills", "*", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) == 0 {
		t.Fatal("no skills under .claude/skills/ — the glob is wrong, not the repo")
	}
	for _, path := range skills {
		body := mustRead(t, path)
		dir := filepath.Base(filepath.Dir(path))
		name := frontMatterField(body, "name")
		switch {
		case name == "":
			t.Errorf(".claude/skills/%s/SKILL.md has no `name` in its front matter, so nothing loads it", dir)
		case name != dir:
			t.Errorf(".claude/skills/%s/SKILL.md is named %q; a skill's name is its directory", dir, name)
		}
		if frontMatterField(body, "description") == "" {
			t.Errorf(".claude/skills/%s/SKILL.md has no `description`. The description IS the trigger: "+
				"a skill without one is never invoked.", dir)
		}
	}

	agents, err := filepath.Glob(filepath.Join(claudeDir, "agents", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range agents {
		body := mustRead(t, path)
		base := strings.TrimSuffix(filepath.Base(path), ".md")
		if name := frontMatterField(body, "name"); name != base {
			t.Errorf(".claude/agents/%s.md is named %q; a subagent's name is its filename", base, name)
		}
		if frontMatterField(body, "description") == "" {
			t.Errorf(".claude/agents/%s.md has no `description`, so nothing knows when to invoke it", base)
		}
	}
}
