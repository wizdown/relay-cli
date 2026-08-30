package main

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Drift tests for the documentation as a set of PAGES, where docs_test.go
// covers the config reference as a set of FIELDS.
//
// The rule these enforce used to be prose in AGENTS.md — "re-read the pages
// your change touches" — which is a rule only as good as the last person's
// attention. These three are the parts of it a machine can hold: a link that
// resolves, a command that exists, a version a binary actually prints. What a
// sentence *means* is still nobody's job but the author's.

const repoRoot = "../.."

// markdownFiles is every .md file in the repo, so a new page is covered the
// moment it is added rather than when someone remembers to list it.
func markdownFiles(t *testing.T) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(repoRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "dist", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".md") {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repo for .md files: %v", err)
	}
	if len(out) < 5 {
		t.Fatalf("found only %d markdown files — the walk is wrong, not the docs", len(out))
	}
	return out
}

var (
	// [text](target) — the target may be a path, an #anchor, or both.
	linkRe = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)\)`)
	// ## A heading, from which GitHub derives an anchor.
	headingRe = regexp.MustCompile(`(?m)^#{1,6}\s+(.*)$`)
	// GitHub keeps letters, digits, spaces, hyphens and underscores in an
	// anchor and drops everything else — so `runtime_config` survives while the
	// backticks around it do not.
	slugStripRe = regexp.MustCompile(`[^a-z0-9 _-]`)
)

// slug turns a heading into the anchor GitHub gives it.
func slug(heading string) string {
	s := strings.ToLower(strings.TrimSpace(heading))
	s = slugStripRe.ReplaceAllString(s, "")
	return strings.ReplaceAll(s, " ", "-")
}

func anchors(t *testing.T, path string) map[string]bool {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read %s: %v", path, err)
	}
	out := map[string]bool{}
	for _, m := range headingRe.FindAllStringSubmatch(string(body), -1) {
		out[slug(m[1])] = true
	}
	return out
}

// A cross-reference that 404s is the cheapest documentation bug to make and the
// most expensive to notice: the page still reads fine to whoever wrote it. The
// doc map in AGENTS.md is built on links — "link instead of restating" only
// works while the links resolve.
func TestEveryDocLinkResolves(t *testing.T) {
	files := markdownFiles(t)
	anchorCache := map[string]map[string]bool{}

	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("cannot read %s: %v", file, err)
		}
		for _, m := range linkRe.FindAllStringSubmatch(string(body), -1) {
			target := m[1]
			switch {
			case strings.HasPrefix(target, "http://"),
				strings.HasPrefix(target, "https://"),
				strings.HasPrefix(target, "mailto:"):
				continue
			}

			path, anchor, _ := strings.Cut(target, "#")

			resolved := file
			if path != "" {
				resolved = filepath.Join(filepath.Dir(file), path)
				if _, err := os.Stat(resolved); err != nil {
					t.Errorf("%s links to %q, which does not exist.\n"+
						"Either the file moved and this link did not, or the link is a typo.",
						rel(file), target)
					continue
				}
			}
			if anchor == "" || !strings.HasSuffix(resolved, ".md") {
				continue
			}
			if _, ok := anchorCache[resolved]; !ok {
				anchorCache[resolved] = anchors(t, resolved)
			}
			if !anchorCache[resolved][anchor] {
				t.Errorf("%s links to %q, but %s has no heading with that anchor.\n"+
					"Headings become anchors lowercased, punctuation dropped, spaces hyphenated — "+
					"so renaming a heading breaks every link to it.",
					rel(file), target, rel(resolved))
			}
		}
	}
}

func rel(path string) string {
	r, err := filepath.Rel(repoRoot, path)
	if err != nil {
		return path
	}
	return r
}

// relay 0.1.0 (beta) — checking 2 worker(s) …
var sampleVersionRe = regexp.MustCompile(`\brelay (\d+\.\d+\.\d+)`)

// The docs quote real output, which means they quote a version number. Two
// rules keep that honest without a chore on every merge:
//
//	they all agree — one number across every page, so a reader never has to
//	  wonder which sample is current
//	none is ahead of the code — a docs page may show the last release while
//	  master is already on the next -SNAPSHOT, but it may never show a version
//	  that has not been built
//
// `make release` rewrites these lines to the version it is publishing, so the
// steady state is "the docs show the latest release".
func TestDocsQuoteAVersionThatExists(t *testing.T) {
	base := baseVersion()
	seen := map[string][]string{}

	for _, file := range markdownFiles(t) {
		// Contributor docs describe the release machinery itself, so a version
		// in one of them is an example of a command, not a quote of output.
		if strings.Contains(file, "contributing") {
			continue
		}
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("cannot read %s: %v", file, err)
		}
		for _, m := range sampleVersionRe.FindAllStringSubmatch(string(body), -1) {
			seen[m[1]] = append(seen[m[1]], rel(file))
		}
	}

	if len(seen) > 1 {
		for version, files := range seen {
			t.Errorf("the docs quote relay %s in %s", version, strings.Join(files, ", "))
		}
		t.Error("sample output must quote ONE version across every page — " +
			"a reader cannot tell which of two is the current one")
	}

	for version, files := range seen {
		if semverGreater(version, base) {
			t.Errorf("%s quotes relay %s, but the version constant is %s. "+
				"The docs may lag a release, never lead one — no binary prints that.",
				strings.Join(files, ", "), version, base)
		}
	}
}

// semverGreater reports whether a > b for bare x.y.z versions.
func semverGreater(a, b string) bool {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < 3 && i < len(as) && i < len(bs); i++ {
		if as[i] == bs[i] {
			continue
		}
		return len(as[i]) > len(bs[i]) || (len(as[i]) == len(bs[i]) && as[i] > bs[i])
	}
	return false
}

const cliDocPath = repoRoot + "/docs/cli.md"

// helpText is held to the code by main_test.go. docs/cli.md is the same promise
// made to someone who never runs `relay help` — AGENTS.md sends a flag change to
// both, and one of the two was previously enforced.
func TestCLIDocDocumentsEveryCommandAndFlag(t *testing.T) {
	doc := mustRead(t, cliDocPath)

	for _, cmd := range commands {
		if !strings.Contains(doc, "`"+cmd+"`") {
			t.Errorf("command %q is not in docs/cli.md — add it to the command table", cmd)
		}
	}

	var ro runOpts
	var co checkOpts
	for _, fs := range []*flag.FlagSet{runFlags(&ro), checkFlags(&co)} {
		fs.VisitAll(func(f *flag.Flag) {
			if !strings.Contains(doc, "--"+f.Name) {
				t.Errorf("flag --%s is not in docs/cli.md — add it to the flag table", f.Name)
			}
		})
	}
}
