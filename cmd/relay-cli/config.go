// Config loading for relay-cli: every check made on .worker-config before a
// single worker is launched.
//
// The order matters and is not incidental. A missing CLI, a mistyped repo path
// or a "30" that should have been 30 must fail HERE, once, at launch — not 120
// times an hour in a background log nobody is watching. Everything this file
// rejects is something that would otherwise surface much later and much less
// legibly.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Defaults. Each one is a BOUND rather than "unlimited": the short config has
// to be the safe one, because the safeguard you have to remember is the
// safeguard you forget.
const (
	defaultRuntime        = "claude"
	defaultPollSeconds    = 30.0
	defaultMaxRunsPerHour = 12
	defaultMaxBudgetUSD   = 5.0
	defaultRunTimeoutSecs = 900
)

// The floor under poll_frequency_seconds.
//
// A poll is free for the worker, which is the whole design — but it is not free
// for relay, and from where relay is standing a fleet ticking many times a
// second is indistinguishable from an attempt to flood it. One misplaced
// decimal point in a config should not be able to arrange that.
//
// It is enforced by rejecting the config rather than by quietly clamping the
// value: a worker that polls at a rate its own config does not state is a
// worker nobody can reason about.
const minPollSeconds = 5.0

// Worker is one relay agent identity × one repo checkout × one CLI runtime,
// with every optional field resolved to what the worker will actually use.
type Worker struct {
	Name        string `json:"name"`
	Endpoint    string `json:"-"` // never serialized: it embeds the agent secret
	Runtime     string `json:"runtime"`
	RepoDir     string `json:"repo_dir"`
	Model       string `json:"model"`
	RuntimeArgs string `json:"runtime_args"`

	PollSeconds    float64 `json:"poll_frequency_seconds"`
	MaxRunsPerHour int     `json:"max_runs_per_hour"`
	MaxBudgetUSD   float64 `json:"max_budget_usd"`
	RunTimeoutSecs int     `json:"run_timeout_seconds"`

	// EndpointRedacted is what the API and the UI are allowed to see.
	EndpointRedacted string `json:"mcp_endpoint"`
}

// Config is the validated worker list plus where it came from.
type Config struct {
	Path       string    `json:"path"`
	PollerRoot string    `json:"poller_root"`
	Workers    []*Worker `json:"workers"`
}

// rawWorker keeps the file's own view of an entry: which keys were actually
// present, and their unparsed values. Presence is the thing the struct above
// cannot express, and three checks below depend on it — an absent
// poll_frequency_seconds is a default, a present one that is a string is an
// error, and a present system_prompt_file is a config from a version whose
// behaviour no longer exists.
type rawWorker map[string]json.RawMessage

// removedKeys are rejected BY NAME. Every other unknown key is ignored (each
// optional field is read with a fallback), which is exactly why these five need
// saying out loud: a config still carrying system_prompt_file would otherwise
// launch an agent with no standing instructions at all and look fine doing it.
var removedKeys = map[string]string{
	"system_prompt":            "agent identity now lives in relay: set the agent's instructions_md (update_agent, or the relay agent console). It reaches a RUNNING agent, which a local file never could",
	"system_prompt_file":       "agent identity now lives in relay: move the file's text into the agent's instructions_md (update_agent, or the relay agent console)",
	"min_run_interval_seconds": "replaced by a fixed 60s relaunch cooldown. To make a worker act less often, lower max_runs_per_hour",
	"permission_mode":          "a headless run is always fully autonomous — there is no prompt it could answer. Pass --permission-mode via runtime_args if you truly need another mode",
	"codex_mcp_transport":      "export CODEX_MCP_TRANSPORT=mcp-remote before launching instead",
}

// stripLineComments removes // comments from JSON, quote-aware.
//
// .worker-config is JSON, and JSON has no comments — but this is a file humans
// hand-edit, where "wait, is this one required?" is the question they have while
// editing it. So the shipped example annotates each field where the field is,
// and comments are stripped before the parser ever sees them.
//
// The quote-awareness is the only part that needs care: an mcp_endpoint is a
// URL, and "http://host" contains the comment marker. This walks each line
// tracking whether it is inside a JSON string (honouring backslash escapes) and
// cuts only at a // found OUTSIDE one. A naive cut at the first // would
// silently truncate every endpoint in the file — turning a credential into
// "http:" and failing much later, somewhere far less obvious.
func stripLineComments(src []byte) []byte {
	var out strings.Builder
	for _, line := range strings.Split(string(src), "\n") {
		inStr := false
		for i := 0; i < len(line); i++ {
			c := line[i]
			if inStr {
				if c == '\\' && i+1 < len(line) {
					out.WriteByte(c)
					out.WriteByte(line[i+1])
					i++
					continue
				}
				if c == '"' {
					inStr = false
				}
			} else if c == '"' {
				inStr = true
			} else if c == '/' && i+1 < len(line) && line[i+1] == '/' {
				break
			}
			out.WriteByte(c)
		}
		out.WriteByte('\n')
	}
	return []byte(out.String())
}

// expandTilde — .worker-config is JSON, so a leading ~ arrives literally.
func expandTilde(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return home + p[1:]
		}
	}
	return p
}

// resolveConfigPath turns what the user asked for into the file to read, and
// reports everywhere it looked so a failure can say so.
//
// A directory means "the config in there", so --config relay-cli-workers and
// --config relay-cli-workers/.worker-config are the same request. Given
// nothing, it looks in the current directory first — a poller root someone set
// up themselves — and then in the one `relay-cli init` creates, which is what
// lets init, check and run all be run from the same place with no flags.
func resolveConfigPath(path string) (string, []string) {
	if info, err := os.Stat(path); err == nil {
		if info.IsDir() {
			return filepath.Join(path, defaultConfigName), nil
		}
		return path, nil
	}

	if path == defaultConfigName {
		fallback := filepath.Join(homeDirName, defaultConfigName)
		if _, err := os.Stat(fallback); err == nil {
			return fallback, nil
		}
		return path, []string{path, fallback}
	}
	return path, nil
}

// LoadConfig reads, strips, parses and validates .worker-config. The returned
// error is meant to be printed verbatim to a human and acted on; several are
// deliberately multi-line.
func LoadConfig(path string) (*Config, error) {
	path, searched := resolveConfigPath(path)

	src, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			if len(searched) > 1 {
				return nil, fmt.Errorf("no worker list found. Looked for:\n         %s\n"+
					"       Run \"relay-cli init\" to create one, or point at an existing\n"+
					"       one with --config PATH (a file, or the directory holding it).",
					strings.Join(searched, "\n         "))
			}
			return nil, fmt.Errorf("%s not found. Run \"relay-cli init\" to create one, or\n"+
				"       point --config at an existing config or the directory holding it", path)
		}
		return nil, err
	}

	stripped := stripLineComments(src)

	var doc struct {
		RelayWorkers json.RawMessage `json:"relay_workers"`
	}
	if err := json.Unmarshal(stripped, &doc); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON.\n       (// comments are allowed and were stripped before parsing)\n       %v", path, err)
	}
	if len(doc.RelayWorkers) == 0 {
		return nil, fmt.Errorf("%s must contain a top-level \"relay_workers\" array", path)
	}

	var raws []rawWorker
	if err := json.Unmarshal(doc.RelayWorkers, &raws); err != nil {
		return nil, fmt.Errorf("%s must contain a top-level \"relay_workers\" array (%v)", path, err)
	}
	if len(raws) == 0 {
		return nil, fmt.Errorf("\"relay_workers\" in %s is empty. Define at least one worker", path)
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	cfg := &Config{Path: abs, PollerRoot: filepath.Dir(abs)}

	// Removed keys first: a config written for an older version is not a config
	// with one bad field, and reporting a missing name for it would be noise.
	var removed []string
	for _, raw := range raws {
		name := rawString(raw, "name")
		if name == "" {
			name = "unnamed"
		}
		var keys []string
		for k := range raw {
			if _, gone := removedKeys[k]; gone {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			removed = append(removed, fmt.Sprintf("  worker %q: %q was removed — %s", name, k, removedKeys[k]))
		}
	}
	if len(removed) > 0 {
		return nil, fmt.Errorf("%s uses keys this version no longer supports:\n%s", path, strings.Join(removed, "\n"))
	}

	// Two required fields, and that is the whole floor: a name and the
	// credential. poll_frequency_seconds is optional but must be a positive JSON
	// number when present — "30" as a string is the classic version of this
	// mistake, and would otherwise reach the loop as a broken interval — and it
	// must be at or above minPollSeconds, which is relay's protection rather
	// than yours and so is reported separately, by name and value.
	var invalid, tooFast []string
	for i, raw := range raws {
		bad := rawString(raw, "name") == "" || rawString(raw, "mcp_endpoint") == ""
		if v, ok := raw["poll_frequency_seconds"]; ok {
			var n float64
			switch {
			case json.Unmarshal(v, &n) != nil || n <= 0:
				bad = true
			case n < minPollSeconds:
				tooFast = append(tooFast, fmt.Sprintf("  worker %q polls every %gs", rawString(raw, "name"), n))
			}
		}
		if bad {
			entry, _ := json.Marshal(raw)
			invalid = append(invalid, fmt.Sprintf("  entry %d: %s", i, entry))
		}
	}
	if len(invalid) > 0 {
		return nil, fmt.Errorf("every relay_workers entry needs a non-empty \"name\" and a non-empty \"mcp_endpoint\",\n"+
			"       and \"poll_frequency_seconds\", if present, must be a positive JSON number (30, not \"30\"):\n%s",
			strings.Join(invalid, "\n"))
	}

	if len(tooFast) > 0 {
		return nil, fmt.Errorf("\"poll_frequency_seconds\" is below the %gs minimum:\n%s\n"+
			"       An empty poll costs the worker nothing, but it is a request relay has to\n"+
			"       answer — and a fleet ticking faster than this looks like a flood from\n"+
			"       there. Polling faster is not how work arrives sooner either: raise\n"+
			"       \"max_runs_per_hour\" if a worker should be getting through more.",
			minPollSeconds, strings.Join(tooFast, "\n"))
	}

	seenNames := map[string]bool{}
	seenEndpoints := map[string]bool{}
	var dupNames, dupEndpoints []string

	for _, raw := range raws {
		w := &Worker{
			Name:           rawString(raw, "name"),
			Endpoint:       rawString(raw, "mcp_endpoint"),
			Runtime:        orDefault(rawString(raw, "runtime"), defaultRuntime),
			RepoDir:        rawString(raw, "repo_dir"),
			Model:          rawString(raw, "model"),
			RuntimeArgs:    rawString(raw, "runtime_args"),
			PollSeconds:    rawFloat(raw, "poll_frequency_seconds", defaultPollSeconds),
			MaxRunsPerHour: int(rawFloat(raw, "max_runs_per_hour", defaultMaxRunsPerHour)),
			MaxBudgetUSD:   rawFloat(raw, "max_budget_usd", defaultMaxBudgetUSD),
			RunTimeoutSecs: int(rawFloat(raw, "run_timeout_seconds", defaultRunTimeoutSecs)),
		}

		if seenNames[w.Name] {
			dupNames = append(dupNames, w.Name)
		}
		if seenEndpoints[w.Endpoint] {
			dupEndpoints = append(dupEndpoints, w.Name)
		}
		seenNames[w.Name] = true
		seenEndpoints[w.Endpoint] = true

		// A name becomes live-workers/<name>/, so it has to be a single path
		// segment. The shell poller this replaced only asked it in a comment; a worker called
		// "a/b" silently wrote its state somewhere else entirely.
		if strings.ContainsAny(w.Name, "/\\") || w.Name == "." || w.Name == ".." {
			return nil, fmt.Errorf("worker %q has a name that is not filesystem-safe: it becomes live-workers/<name>/, so it may not contain \"/\" or \"\\\"", w.Name)
		}

		cfg.Workers = append(cfg.Workers, w)
	}

	if len(dupNames) > 0 {
		return nil, fmt.Errorf("duplicate worker name(s) in %s:\n  %s", path, strings.Join(dupNames, "\n  "))
	}
	if len(dupEndpoints) > 0 {
		return nil, fmt.Errorf("duplicate mcp_endpoint(s) in %s (every worker needs its own); repeated on worker(s):\n  %s", path, strings.Join(dupEndpoints, "\n  "))
	}

	// Per-worker runtime + repo checks, before anything is launched.
	for _, w := range cfg.Workers {
		if err := checkRuntime(w.Name, w.Runtime, cfg.PollerRoot); err != nil {
			return nil, err
		}
		if w.RepoDir != "" {
			expanded := expandTilde(w.RepoDir)
			info, err := os.Stat(expanded)
			if err != nil || !info.IsDir() {
				return nil, fmt.Errorf("worker %q has repo_dir %q, which is not a directory", w.Name, w.RepoDir)
			}
			w.RepoDir = expanded
		}
	}

	// Redaction is computed once, here, so no later code path has to remember to
	// do it. Nothing downstream of this point is given the secret except the
	// code actually talking to relay.
	for _, w := range cfg.Workers {
		w.EndpointRedacted = RedactURL(w.Endpoint)
	}
	InstallSecrets(cfg.Workers)

	return cfg, nil
}

// checkRuntime resolves a worker's runtime and asks it to prove its CLI is
// installed and usable, before anything is launched.
//
// It is a variable so the config parser's own tests can run on a machine with
// no coding CLI installed. `go test ./...` passing on a fresh clone is a
// property worth keeping: requiring a contributor to install Claude Code before
// they can test a JSON parser would be a poor first five minutes, and CI would
// have to install one too. Nothing but a test ever reassigns it, and the
// production path below is the whole check.
var checkRuntime = func(name, runtime, pollerRoot string) error {
	rt, err := ResolveRuntime(runtime, pollerRoot)
	if err != nil {
		return fmt.Errorf("worker %q asks for runtime %q, but %v", name, runtime, err)
	}
	if err := rt.Check(); err != nil {
		return fmt.Errorf("worker %q cannot run: runtime %q is unusable.\n       %v", name, runtime, err)
	}
	return nil
}

func rawString(raw rawWorker, key string) string {
	v, ok := raw[key]
	if !ok {
		return ""
	}
	var s string
	if json.Unmarshal(v, &s) != nil {
		return ""
	}
	return s
}

func rawFloat(raw rawWorker, key string, def float64) float64 {
	v, ok := raw[key]
	if !ok {
		return def
	}
	var n float64
	if json.Unmarshal(v, &n) != nil {
		return def
	}
	return n
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
