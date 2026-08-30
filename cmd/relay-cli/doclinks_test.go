package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Every doc pointer relay-cli prints has to be a full URL.
//
// The reader of one may hold nothing but the binary — there is no clone for a
// bare "docs/configuration.md" to resolve against, and the config `init` writes
// lands in ~/.relay/, which is not one either. A relative path there is not a
// slightly worse link, it is no link: the reader has nothing to type it into.
//
// This walks string literals rather than the file text, so the prose in Go
// comments (which nobody outside this repo reads) stays free to say
// "docs/configuration.md" as shorthand.
func TestUserFacingDocLinksAreFullURLs(t *testing.T) {
	forEachSourceString(t, func(t *testing.T, file string, s string) {
		for _, ref := range docRefs(s) {
			if !strings.Contains(s, docsBase+strings.TrimPrefix(ref, "docs/")) {
				t.Errorf("%s: a string printed to a user says %q.\n"+
					"Use docsBase — %q — so it is a link the reader can open.", file, ref, docsBase)
			}
		}
	})
}

// The default branch is master. `main` is the habit, and a link to a branch
// that does not exist is a 404 shipped inside somebody's config file — which is
// exactly what v0.1.0 did, and what this test exists to stop happening twice.
func TestNoLinksToABranchThatDoesNotExist(t *testing.T) {
	if !strings.Contains(docsBase, "/blob/master/") {
		t.Fatalf("docsBase = %q, but this repo's default branch is master", docsBase)
	}
	forEachSourceString(t, func(t *testing.T, file string, s string) {
		if strings.Contains(s, "relay-cli/blob/main/") {
			t.Errorf("%s: links wizdown/relay-cli at blob/main, which does not exist "+
				"— the default branch is master", file)
		}
	})
}

// docRefs finds "docs/<something>.md" mentions in one string.
func docRefs(s string) []string {
	var out []string
	for i := 0; ; {
		j := strings.Index(s[i:], "docs/")
		if j < 0 {
			return out
		}
		start := i + j
		end := strings.Index(s[start:], ".md")
		if end < 0 {
			return out
		}
		out = append(out, s[start:start+end+len(".md")])
		i = start + end
	}
}

// forEachSourceString calls fn for every string literal in the package's
// non-test sources — the ones that can reach a user's terminal or config file.
func forEachSourceString(t *testing.T, fn func(t *testing.T, file, s string)) {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatalf("%s: %v", filepath.Base(name), err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			fn(t, name, s)
			return true
		})
	}
}
