// Secret scrubbing.
//
// Every relay_mcp in the config is a live credential — the connector URL
// from issue_agent_credential, secret included, shown exactly once. relay-cli
// serves a web page and writes log files, so it has two new ways to leak one
// that the bash poller did not have, and both are covered here rather than at
// each call site.
//
// The rule this file exists to enforce: a raw endpoint is passed to exactly two
// places — the HTTP client that talks to relay, and the MCP config file the CLI
// reads (mode 0600, in the worker's gitignored state dir). Everything else —
// every log line, every event, every error, every byte of every HTTP response —
// goes through Scrub first.
package main

import (
	"regexp"
	"strings"
	"sync"
)

// connectorPath matches the credential segment of a connector URL, which is the
// form a connector URL takes: .../relay/mcp/c/<secret>.
var connectorPath = regexp.MustCompile(`(/c/)[^/\s"']+`)

var (
	secretsMu sync.RWMutex
	// scrubber replaces the exact endpoints and bare tokens this process knows
	// about. It catches a secret however it was reassembled — split across a URL
	// we did not construct, quoted inside a CLI's JSON output, or logged bare —
	// which the path regex alone would miss.
	scrubber *strings.Replacer
)

// RedactURL renders one connector URL safe to display, keeping everything that
// identifies the RELAY (host, path shape) and nothing that authenticates as the
// agent.
func RedactURL(u string) string {
	if u == "" {
		return ""
	}
	if connectorPath.MatchString(u) {
		return connectorPath.ReplaceAllString(u, "${1}wzh_REDACTED")
	}
	// An endpoint that is not in the /c/<secret> shape is still a credential;
	// treat the whole thing as one rather than guessing which part is safe.
	return "<redacted relay_mcp>"
}

// InstallSecrets teaches Scrub every credential in this config. Called once,
// from LoadConfig, before any worker starts.
func InstallSecrets(workers []*Worker) {
	var pairs []string
	for _, w := range workers {
		if w.Endpoint == "" {
			continue
		}
		pairs = append(pairs, w.Endpoint, RedactURL(w.Endpoint))
		// The bare token too: a CLI that echoes only the last path segment, or a
		// server that quotes the credential back in an error, never prints the
		// whole URL.
		if m := connectorPath.FindStringSubmatch(w.Endpoint); m != nil {
			token := strings.TrimPrefix(m[0], "/c/")
			// Short tokens are not redacted: a 3-character replacement target
			// would rewrite unrelated text all over the UI, which is its own kind
			// of broken. A real connector secret is far longer than this.
			if len(token) >= 8 {
				pairs = append(pairs, token, "wzh_REDACTED")
			}
		}
	}
	secretsMu.Lock()
	defer secretsMu.Unlock()
	if len(pairs) == 0 {
		scrubber = nil
		return
	}
	scrubber = strings.NewReplacer(pairs...)
}

// Scrub removes every known credential from a string, and any unknown one that
// still looks like a connector URL. Cheap enough to apply to everything, which
// is the point: correctness here must not depend on remembering to call it in
// the one place that turned out to matter.
func Scrub(s string) string {
	if s == "" {
		return s
	}
	secretsMu.RLock()
	r := scrubber
	secretsMu.RUnlock()
	if r != nil {
		s = r.Replace(s)
	}
	return connectorPath.ReplaceAllString(s, "${1}wzh_REDACTED")
}
