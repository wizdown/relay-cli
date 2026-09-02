// Config loading for relay-cli: every check made on ~/.relay/config before a
// single worker is launched.
//
// The order matters and is not incidental. A missing CLI, a mistyped repo path
// or a "30" that should have been 30 must fail HERE, once, at launch — not 120
// times an hour in a background log nobody is watching. Everything this file
// rejects is something that would otherwise surface much later and much less
// legibly.
//
// Problems are ACCUMULATED rather than reported one at a time. Four fields per
// worker are required, so the first run of `check` against a half-written config
// has a lot to say; saying one thing per run turns that into a dozen edit-rerun
// rounds for what is really one sitting of work.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Defaults. Each one is a BOUND rather than "unlimited": the short config has
// to be the safe one, because the safeguard you have to remember is the
// safeguard you forget.
//
// Fields the user MUST state have no default here on purpose — a name, a
// credential, a repo and a runtime are decisions relay-cli cannot make for
// anyone, and inventing an answer for them is how a fleet ends up doing
// something nobody chose.
const (
	defaultPollSeconds      = 30.0
	defaultMaxRunsPerHour   = 12
	defaultMaxSecondsPerRun = 900
)

// The floor under poll_seconds.
//
// A poll is free for the worker, which is the whole design — but it is not free
// for relay, and from where relay is standing a fleet ticking many times a
// second is indistinguishable from an attempt to flood it. One misplaced
// decimal point in a config should not be able to arrange that.
//
// It is enforced by rejecting the config rather than by quietly clamping the
// value: a fleet that polls at a rate its own config does not state is a fleet
// nobody can reason about.
const minPollSeconds = 5.0

// The floor under a max_seconds_per_run that is actually set.
//
// This one protects the operator rather than relay. A kill measured in a few
// seconds does not make a worker careful, it makes every session die before it
// can claim anything — while still costing whatever the model spent getting
// there. A worker that can never finish a cycle is a worse outcome than one
// with no kill at all, so the value is rejected rather than allowed through as
// a plausible-looking number. 0 still removes the kill deliberately.
const minSecondsPerRun = 30

// The two placeholders `relay init` writes, rejected BY NAME.
//
// Both mark a decision relay-cli cannot make for anyone, and both are rejected
// here — in the same pass as every other problem — rather than later. A step the
// user has not done yet should not read as a typo, and finding out about the
// second one only after fixing the first is the edit-and-rerun grind this whole
// pass exists to avoid.
const (
	repoDirPlaceholder  = "/path/to/your/repo"
	endpointPlaceholder = "REPLACE_ME"
)

// Worker is one relay agent identity × one repo checkout × one CLI runtime,
// with every optional field resolved to what the worker will actually use.
//
// Everything here is enforced by relay-cli itself and means the same thing for
// every runtime. What varies per runtime lives in RuntimeConfig, which is the
// whole point of the split: the fields above the line are this tool's promises,
// and the ones below are one CLI's vocabulary.
type Worker struct {
	Name     string `json:"name"`
	Endpoint string `json:"-"` // never serialized: it embeds the agent secret
	Runtime  string `json:"runtime"`
	RepoDir  string `json:"repo_dir"`

	MaxRunsPerHour   int `json:"max_runs_per_hour"`
	MaxSecondsPerRun int `json:"max_seconds_per_run"`

	// RuntimeConfig holds the validated "runtime_config" block: every key the
	// worker's runtime declares, with absent optional ones resolved to their
	// defaults. Values are canonical strings because that is what an argv and a
	// bash adapter's environment both need.
	RuntimeConfig map[string]string `json:"runtime_config"`

	// EndpointRedacted is what the API and the UI are allowed to see.
	EndpointRedacted string `json:"relay_mcp"`
}

// RCString reads one runtime_config value.
func (w *Worker) RCString(key string) string { return w.RuntimeConfig[key] }

// RCFloat reads one runtime_config value as a number. Absent or unparseable
// reads as 0, which every numeric field here spells "no limit".
func (w *Worker) RCFloat(key string) float64 {
	n, err := strconv.ParseFloat(w.RuntimeConfig[key], 64)
	if err != nil {
		return 0
	}
	return n
}

// RCBool reads one runtime_config value as a boolean. Values are canonicalised
// to "true"/"false" when the config is validated, so anything else is a key the
// worker's runtime does not declare and reads as false.
func (w *Worker) RCBool(key string) bool { return w.RuntimeConfig[key] == "true" }

// Config is the validated worker list plus where it came from.
type Config struct {
	Path     string `json:"path"`
	RelayDir string `json:"relay_dir"`

	// PollSeconds is fleet-wide. The floor under it protects relay rather than
	// the operator, and how hard a fleet leans on one relay server is a property
	// of the fleet — not something each worker should be able to answer
	// differently.
	PollSeconds float64 `json:"poll_seconds"`

	Workers []*Worker `json:"workers"`
}

// rawWorker keeps the file's own view of an entry: which keys were actually
// present, and their unparsed values. Presence is the thing the struct above
// cannot express, and several checks below depend on it — an absent
// max_runs_per_hour is a default, a present one that is a string is an error,
// and a present system_prompt_file is a config from a version whose behaviour
// no longer exists.
type rawWorker map[string]json.RawMessage

// removedKeys are rejected with the migration, not merely as unknown.
//
// Every unknown key is refused (see workerKeys below), so these would fail
// anyway — what this table adds is WHERE THE SETTING WENT. A config carrying
// system_prompt_file is not a typo to correct, it is a version's worth of
// behaviour that moved to relay, and "did you mean repo_dir?" would be a worse
// answer than none. These errors are the whole migration path, which is why the
// user docs describe only what this version accepts.
var removedKeys = map[string]string{
	"system_prompt":            "agent identity now lives in relay: set the agent's instructions_md (update_agent, or the relay agent console). It reaches a RUNNING agent, which a local file never could",
	"system_prompt_file":       "agent identity now lives in relay: move the file's text into the agent's instructions_md (update_agent, or the relay agent console)",
	"min_run_interval_seconds": "replaced by a fixed 60s relaunch cooldown. To make a worker act less often, lower max_runs_per_hour",
	"permission_mode":          "a headless run is always fully autonomous — there is no prompt it could answer",
	"codex_mcp_transport":      "there is no transport to choose: a codex worker is given relay as a streamable-HTTP MCP server directly, with no stdio bridge in between",
	"runtime_args":             "removed. Raw argv could silently override the flags this harness depends on. Every setting a runtime accepts is now a declared key in \"runtime_config\"",
	"mcp_endpoint":             "renamed to \"relay_mcp\"",
	"model":                    "moved into \"runtime_config\": it is spelled in the runtime's own vocabulary, not relay-cli's",
	"max_budget_usd":           "moved into \"runtime_config\" and renamed to \"max_usd_per_run\": only some runtimes can enforce a spend cap",
	"run_timeout_seconds":      "renamed to \"max_seconds_per_run\"",
	"poll_frequency_seconds":   "renamed to \"poll_seconds\", and moved to the TOP LEVEL of the config: one poll rate for the fleet",
}

// Every key this version accepts, by the level it belongs at.
//
// Unknown keys are REFUSED rather than ignored, at every level of the file. A
// key relay-cli does not read is a setting the operator believes is in force —
// `max_runs_per_hr` is not a config with a harmless extra line, it is a fleet
// running at a ceiling nobody chose, and finding that out costs a week of runs.
// The same reasoning already applied inside `runtime_config`; it applies just as
// well to the fields relay-cli enforces itself.
//
// workerKeys is the json tags on Worker, and a test fails when the two drift —
// so a new field is accepted by adding it to the struct, not by remembering to
// add it here as well.
var (
	topLevelKeys = []string{"poll_seconds", "workers"}
	workerKeys   = []string{
		"name",
		"relay_mcp",
		"repo_dir",
		"runtime",
		"max_runs_per_hour",
		"max_seconds_per_run",
		"runtime_config",
	}
)

// unknownKeys returns the keys of raw that are not in accepted, sorted so a
// report reads the same way twice.
func unknownKeys(raw map[string]json.RawMessage, accepted []string) []string {
	known := map[string]bool{}
	for _, k := range accepted {
		known[k] = true
	}
	var out []string
	for k := range raw {
		if !known[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// nearest returns the accepted key an unknown one was most likely meant to be,
// or "" when nothing is close enough to guess.
//
// Refusing a key is only half an error message. Nearly every unknown key is a
// typo of a real one, and "did you mean max_runs_per_hour?" turns a rejection
// into the edit — which is the difference between a stricter parser helping and
// merely being stricter.
func nearest(key string, accepted []string) string {
	// Two edits catches the realistic misspellings (a dropped letter, a
	// transposition, a singular where the key is plural) without guessing wildly
	// at a long key that happens to share a prefix.
	best, bestDist := "", 3
	for _, k := range accepted {
		if d := editDistance(key, k); d < bestDist {
			best, bestDist = k, d
		}
	}
	return best
}

// editDistance is Levenshtein, two rows at a time. Config keys are short, so
// the simple form is the right one.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// topLevelKeyHint explains where an unknown top-level key actually belongs.
func topLevelKeyHint(key string) string {
	if gone, ok := removedKeys[key]; ok {
		return "\n      " + gone
	}
	for _, k := range workerKeys {
		if k == key {
			return "\n      it is a per-worker field — it belongs inside an entry in \"workers\""
		}
	}
	if near := nearest(key, topLevelKeys); near != "" {
		return fmt.Sprintf(" — did you mean %q?", near)
	}
	return ""
}

// workerKeyHint explains where an unknown worker-level key actually belongs.
//
// The two misplacements worth naming are the ones the file's own shape invites:
// a runtime's setting written beside relay-cli's fields instead of inside
// "runtime_config", and the fleet-wide poll rate written per worker. Both look
// entirely reasonable while being read by nothing.
func workerKeyHint(key string, fields []runtimeField) string {
	for _, f := range fields {
		if f.Key == key {
			return "\n      it is a setting this worker's runtime understands — move it inside\n      \"runtime_config\""
		}
	}
	if key == "poll_seconds" {
		return "\n      the poll rate is fleet-wide — move it to the TOP LEVEL of the config"
	}
	if near := nearest(key, workerKeys); near != "" {
		return fmt.Sprintf(" — did you mean %q?", near)
	}
	return ""
}

// quoted is the accepted-key list as it should read inside a sentence.
func quoted(keys []string) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, strconv.Quote(k))
	}
	return out
}

// trimNum prints a config number back the way the file wrote it — 6.5 as "6.5"
// and 6 as "6", not "6.000000". An error that misquotes the value it is
// complaining about is an error someone searches their file for in vain.
func trimNum(n float64) string { return strconv.FormatFloat(n, 'f', -1, 64) }

// endpointProblem rejects a relay_mcp that cannot be a connector URL at all.
//
// The shape is checked here rather than left to the first probe: under `check`
// a malformed URL costs a confusing transport error, and under `run` it fails
// on every poll for as long as the fleet is up. The value itself is never
// quoted back — it is the credential.
func endpointProblem(endpoint string) bool {
	u, err := url.Parse(endpoint)
	return err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https")
}

// stripLineComments removes // comments from JSON, quote-aware.
//
// The config is JSON, and JSON has no comments — but this is a file humans
// hand-edit, where "wait, is this one required?" is the question they have while
// editing it. So `relay init` annotates each field where the field is, and
// comments are stripped before the parser ever sees them.
//
// The quote-awareness is the only part that needs care: relay_mcp is a URL, and
// "http://host" contains the comment marker. This walks each line tracking
// whether it is inside a JSON string (honouring backslash escapes) and cuts only
// at a // found OUTSIDE one. A naive cut at the first // would silently truncate
// every endpoint in the file — turning a credential into "http:" and failing
// much later, somewhere far less obvious.
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

// expandTilde — the config is JSON, so a leading ~ arrives literally.
func expandTilde(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return home + p[1:]
		}
	}
	return p
}

// RelayHome is ~/.relay — the one place relay-cli keeps anything.
//
// One location, with no flag to move it. A worker is a relay agent IDENTITY
// holding a credential relay issued to a person, and identities are user-scoped
// the way ~/.aws and ~/.kube are; a fleet also routinely spans several
// checkouts, so there is no one repo it could sensibly belong to. Keeping it in
// exactly one place is also what makes "which config is this running?" a
// question with no answer needed.
func RelayHome() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot locate your home directory, and relay-cli keeps everything in ~/%s: %v", relayDirName, err)
	}
	return filepath.Join(home, relayDirName), nil
}

// DefaultConfigPath is ~/.relay/config.
func DefaultConfigPath() (string, error) {
	dir, err := RelayHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, configFileName), nil
}

// LoadConfig reads, strips, parses and validates the config. The returned error
// is meant to be printed verbatim to a human and acted on; most are deliberately
// multi-line, and several list every problem in the file at once.
func LoadConfig(path string) (*Config, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no config at %s.\n"+
				"       Run \"relay init\" to create one.", path)
		}
		return nil, err
	}

	stripped := stripLineComments(src)

	// An empty file is a step half done, not a syntax error. `relay init` will
	// not overwrite it either, so the fix has to say more than "run init".
	if len(bytes.TrimSpace(stripped)) == 0 {
		return nil, fmt.Errorf("%s has no configuration in it — it is empty once // comments are\n"+
			"       stripped. Move it aside and run \"relay init\" to write a fresh starting\n"+
			"       config; init refuses to overwrite a file that is already there.", path)
	}

	// A map rather than a struct: which keys the file actually uses is the thing
	// a struct cannot report, and an unknown one at this level is refused. Values
	// stay raw so that "30" as a string reports itself as the wrong TYPE for one
	// field, instead of failing the whole document as malformed JSON somewhere
	// unnamed.
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(stripped, &doc); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON.\n       (// comments are allowed and were stripped before parsing)\n       %v", path, err)
	}

	// Top-level unknowns are reported before anything else, and alone: when
	// "workers" itself is the misspelled key there is no worker list to have an
	// opinion about, and the suggestion IS the whole fix.
	var topProblems []string
	for _, k := range unknownKeys(doc, topLevelKeys) {
		topProblems = append(topProblems, fmt.Sprintf("  top level: %q is not a key this version accepts%s", k, topLevelKeyHint(k)))
	}
	if len(topProblems) > 0 {
		return nil, fmt.Errorf("%s uses %d top-level key(s) this version does not accept:\n%s\n\n"+
			"       The top level takes %s, and nothing else — every\n"+
			"       other setting belongs to one worker. The full reference is\n"+
			"       "+docsBase+"configuration.md",
			path, len(topProblems), strings.Join(topProblems, "\n"), strings.Join(quoted(topLevelKeys), " and "))
	}

	if len(doc["workers"]) == 0 {
		return nil, fmt.Errorf("%s must contain a top-level \"workers\" array", path)
	}

	var raws []rawWorker
	if err := json.Unmarshal(doc["workers"], &raws); err != nil {
		return nil, fmt.Errorf("%s must contain a top-level \"workers\" array (%v)", path, err)
	}
	if len(raws) == 0 {
		return nil, fmt.Errorf("\"workers\" in %s is empty. Define at least one worker", path)
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	pollSeconds := float64(defaultPollSeconds)
	if len(doc["poll_seconds"]) > 0 {
		if err := json.Unmarshal(doc["poll_seconds"], &pollSeconds); err != nil {
			return nil, fmt.Errorf("%s: \"poll_seconds\" must be a JSON number (30, not \"30\")", path)
		}
	}
	if pollSeconds < minPollSeconds {
		return nil, fmt.Errorf("\"poll_seconds\" is %g, below the %gs minimum.\n"+
			"       An empty poll costs you nothing, but it is a request relay has to\n"+
			"       answer — and a fleet ticking faster than this looks like a flood from\n"+
			"       there. Polling faster is not how work arrives sooner either: raise\n"+
			"       \"max_runs_per_hour\" if a worker should be getting through more.",
			pollSeconds, minPollSeconds)
	}

	cfg := &Config{Path: abs, RelayDir: filepath.Dir(abs), PollSeconds: pollSeconds}

	// Removed keys first: a config written for an older version is not a config
	// with one bad field, and reporting a missing repo_dir for it would be noise
	// on top of the rename that actually explains it.
	var removed []string
	for i, raw := range raws {
		label := workerLabel(raw, i)
		var keys []string
		for k := range raw {
			if _, gone := removedKeys[k]; gone {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			removed = append(removed, fmt.Sprintf("  %s: %q — %s", label, k, removedKeys[k]))
		}
	}
	if len(removed) > 0 {
		return nil, fmt.Errorf("%s uses keys this version no longer supports:\n%s", path, strings.Join(removed, "\n"))
	}

	// Everything the FILE can be wrong about, in one pass and one report.
	var problems []string
	seenNames := map[string]bool{}
	seenEndpoints := map[string]bool{}

	for i, raw := range raws {
		label := workerLabel(raw, i)

		w := &Worker{
			Name:             rawString(raw, "name"),
			Endpoint:         rawString(raw, "relay_mcp"),
			Runtime:          rawString(raw, "runtime"),
			RepoDir:          rawString(raw, "repo_dir"),
			MaxRunsPerHour:   int(rawFloat(raw, "max_runs_per_hour", defaultMaxRunsPerHour)),
			MaxSecondsPerRun: int(rawFloat(raw, "max_seconds_per_run", defaultMaxSecondsPerRun)),
		}

		// Absent, wrong-typed and empty are three different mistakes with three
		// different fixes, and reporting a `"name": 5` as a MISSING name sends
		// someone to add a field that is already there.
		for _, f := range []struct{ key, val, why string }{
			{"name", w.Name, "unique, filesystem-safe — it becomes " + stateDirName + "/<name>/"},
			{"relay_mcp", w.Endpoint, "the connector_url from relay's issue_agent_credential, secret included"},
			{"repo_dir", w.RepoDir, "the checkout this worker's CLI runs in"},
			{"runtime", w.Runtime, "which CLI drives this worker"},
		} {
			if f.val != "" {
				continue
			}
			v, present := raw[f.key]
			switch {
			case !present:
				problems = append(problems, fmt.Sprintf("  %s: missing %q — %s", label, f.key, f.why))
			case json.Unmarshal(v, &f.val) != nil:
				problems = append(problems, fmt.Sprintf("  %s: %q must be a string — %s", label, f.key, f.why))
			default:
				problems = append(problems, fmt.Sprintf("  %s: %q is empty — %s", label, f.key, f.why))
			}
		}

		// What this worker's runtime accepts, resolved once: the unknown-key
		// check uses it to say where a misplaced setting belongs, and
		// runtime_config is validated against it further down.
		var fields []runtimeField
		var runtimeErr error
		if w.Runtime != "" {
			fields, runtimeErr = runtimeFields(w.Runtime, cfg.RelayDir)
		}

		// Every key relay-cli itself reads, checked by name. Inside
		// runtime_config the same rule is applied by the runtime's own table.
		for _, k := range unknownKeys(raw, workerKeys) {
			problems = append(problems, fmt.Sprintf("  %s: %q is not a key this version accepts%s", label, k, workerKeyHint(k, fields)))
		}

		switch {
		case strings.Contains(w.Endpoint, endpointPlaceholder):
			problems = append(problems, fmt.Sprintf("  %s: relay_mcp is still the placeholder from `relay init` — paste the whole\n"+
				"      connector_url over it (relay: onboard_agent, then issue_agent_credential).\n"+
				"      The secret is part of that URL and is shown exactly once", label))
		case w.Endpoint != "" && endpointProblem(w.Endpoint):
			// The value is never quoted back: it is the credential.
			problems = append(problems, fmt.Sprintf("  %s: relay_mcp is not an http(s) URL — paste the whole connector_url relay\n"+
				"      issued, scheme and secret included", label))
		}

		// A name becomes <state>/<name>/, so it has to be a single path segment.
		// The shell poller this replaced only asked for that in a comment; a
		// worker called "a/b" silently wrote its state somewhere else entirely.
		switch {
		case w.Name != "" && (strings.ContainsAny(w.Name, "/\\") || w.Name == "." || w.Name == ".."):
			problems = append(problems, fmt.Sprintf("  %s: name is not filesystem-safe — it becomes %s/<name>/, so it may not contain \"/\" or \"\\\"", label, stateDirName))
		// A name is typed into `tail`, `rm` and a PAUSED path by hand. One that
		// begins or ends in a space is a directory nobody can name from a shell
		// without discovering why, long after the config was written.
		case w.Name != strings.TrimSpace(w.Name):
			problems = append(problems, fmt.Sprintf("  %s: name has leading or trailing whitespace — it becomes a directory you\n"+
				"      have to type, so trim it to %q", label, strings.TrimSpace(w.Name)))
		}
		if w.Name != "" && seenNames[w.Name] {
			problems = append(problems, fmt.Sprintf("  %s: duplicate name — every worker needs its own", label))
		}
		seenNames[w.Name] = true

		// One credential per agent is relay's boundary, and two workers sharing
		// one is the mistake that quietly breaks it: both claim as the same
		// agent, against one another.
		if w.Endpoint != "" && seenEndpoints[w.Endpoint] {
			problems = append(problems, fmt.Sprintf("  %s: duplicate relay_mcp — every worker needs its own credential", label))
		}
		seenEndpoints[w.Endpoint] = true

		// Both ceilings count whole things — runs, seconds — and both spell "no
		// limit" as 0. Every other value is checked here rather than truncated
		// quietly into an int: a worker capped at 6.9 runs is capped at 6, and
		// nothing would ever have said so.
		for _, f := range []struct {
			key   string
			unit  string
			floor int // the smallest value that still means something, when set
			why   string
		}{
			{"max_runs_per_hour", "runs", 0, "no ceiling on how many sessions start"},
			{"max_seconds_per_run", "seconds", minSecondsPerRun,
				"no kill at all — only max_runs_per_hour would bound a hung session"},
		} {
			v, present := raw[f.key]
			if !present {
				continue
			}
			var n float64
			switch {
			case json.Unmarshal(v, &n) != nil:
				problems = append(problems, fmt.Sprintf("  %s: %q must be a JSON number (900, not \"900\")", label, f.key))
			case n < 0:
				problems = append(problems, fmt.Sprintf("  %s: %q is %s — a ceiling cannot be negative. Use 0 for %s,\n"+
					"      or a positive number of %s", label, f.key, trimNum(n), f.why, f.unit))
			case n != math.Trunc(n):
				problems = append(problems, fmt.Sprintf("  %s: %q is %s — it counts whole %s", label, f.key, trimNum(n), f.unit))
			case f.floor > 0 && n > 0 && n < float64(f.floor):
				problems = append(problems, fmt.Sprintf("  %s: %q is %s, below the %d-second minimum — a kill that short ends\n"+
					"      every session before it can claim a task, and the run is still paid for.\n"+
					"      Use 0 for %s", label, f.key, trimNum(n), f.floor, f.why))
			}
		}

		// The repo has to exist now rather than at the first launch: a worker
		// that polls happily for an hour and then cannot start is a worse way to
		// learn about a typo than a line at startup.
		switch {
		case w.RepoDir == repoDirPlaceholder:
			problems = append(problems, fmt.Sprintf("  %s: repo_dir is still the placeholder from `relay init` — point it at the\n"+
				"      checkout this agent should work in, and one you are willing to have rewritten", label))
		case w.RepoDir != "":
			expanded := expandTilde(w.RepoDir)
			info, err := os.Stat(expanded)
			switch {
			// A relative path resolves against whatever directory `relay run`
			// happened to be started from, so the same config would drive a
			// different checkout depending on where it was launched — and the
			// wrong one would still look like it worked.
			case !filepath.IsAbs(expanded):
				problems = append(problems, fmt.Sprintf("  %s: repo_dir %q is a relative path — it would resolve against wherever\n"+
					"      \"relay run\" was started from. Give the full path, or one starting with \"~/\"", label, w.RepoDir))
			case err != nil || !info.IsDir():
				problems = append(problems, fmt.Sprintf("  %s: repo_dir %q is not a directory", label, w.RepoDir))
			}
			w.RepoDir = expanded
		}

		// runtime_config is validated against what the named runtime actually
		// accepts, so an unsupported key is refused here instead of being
		// silently ignored for the life of the fleet.
		if w.Runtime != "" {
			if runtimeErr != nil {
				problems = append(problems, fmt.Sprintf("  %s: %v", label, runtimeErr))
			} else {
				rc, rcProblems := validateRuntimeConfig(label, w.Runtime, raw, fields)
				w.RuntimeConfig = rc
				problems = append(problems, rcProblems...)
			}
		}

		cfg.Workers = append(cfg.Workers, w)
	}

	if len(problems) > 0 {
		return nil, fmt.Errorf("%s needs %d fix(es):\n%s\n\n"+
			"       Every worker needs a name, a relay_mcp credential, a repo_dir and a\n"+
			"       runtime; every other key has to be one this version accepts, because a\n"+
			"       key relay-cli does not read is a setting you would believe was in force.\n"+
			"       The full reference is "+docsBase+"configuration.md",
			path, len(problems), strings.Join(problems, "\n"))
	}

	// Machine problems, reported separately from file problems: a config that is
	// correct and a CLI that is missing are different jobs, and mixing them makes
	// each one harder to act on.
	var unusable []string
	for _, w := range cfg.Workers {
		if err := checkRuntime(w.Name, w.Runtime, cfg.RelayDir); err != nil {
			unusable = append(unusable, "  "+err.Error())
		}
	}
	if len(unusable) > 0 {
		return nil, fmt.Errorf("the config is valid, but a runtime it names is not usable here:\n%s", strings.Join(unusable, "\n"))
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

// workerLabel names an entry in an error the best way the entry allows: by name
// when it has one, and by position when the missing name IS the problem.
func workerLabel(raw rawWorker, i int) string {
	if name := rawString(raw, "name"); name != "" {
		return fmt.Sprintf("worker %q", name)
	}
	return fmt.Sprintf("worker #%d", i+1)
}

// validateRuntimeConfig checks one worker's runtime_config against the fields
// its runtime declares, resolving absent optional keys to their defaults.
//
// Unknown keys are an ERROR here, unlike unknown keys elsewhere in the file.
// This block is the one place a setting is spelled in a CLI's own vocabulary, so
// a key the runtime does not know is either a typo or a setting meant for a
// different runtime — and both are worth catching before a fleet runs for a week
// ignoring it.
func validateRuntimeConfig(label, runtimeName string, raw rawWorker, fields []runtimeField) (map[string]string, []string) {
	var problems []string
	out := map[string]string{}

	block := map[string]json.RawMessage{}
	if v, ok := raw["runtime_config"]; ok {
		if err := json.Unmarshal(v, &block); err != nil {
			return out, []string{fmt.Sprintf("  %s: \"runtime_config\" must be a JSON object", label)}
		}
	}

	known := map[string]bool{}
	for _, f := range fields {
		known[f.Key] = true

		v, present := block[f.Key]
		if !present {
			if f.Required {
				problems = append(problems, fmt.Sprintf("  %s: runtime_config is missing %q — %s", label, f.Key, f.Doc))
			} else if f.Default != "" {
				out[f.Key] = f.Default
			}
			continue
		}

		switch f.Kind {
		case fieldBool:
			var b bool
			if json.Unmarshal(v, &b) != nil {
				problems = append(problems, fmt.Sprintf("  %s: runtime_config.%s must be true or false, unquoted — %s", label, f.Key, f.Doc))
				continue
			}
			out[f.Key] = strconv.FormatBool(b)
		case fieldNumber:
			var n float64
			switch {
			case json.Unmarshal(v, &n) != nil:
				problems = append(problems, fmt.Sprintf("  %s: runtime_config.%s must be a JSON number (5, not \"5\") — %s", label, f.Key, f.Doc))
				continue
			case n < 0:
				problems = append(problems, fmt.Sprintf("  %s: runtime_config.%s is %s — a cap cannot be negative. Use 0 to remove it", label, f.Key, trimNum(n)))
				continue
			}
			out[f.Key] = trimNum(n)
		default:
			var s string
			switch {
			case json.Unmarshal(v, &s) != nil:
				problems = append(problems, fmt.Sprintf("  %s: runtime_config.%s must be a string — %s", label, f.Key, f.Doc))
				continue
			case strings.TrimSpace(s) == "":
				problems = append(problems, fmt.Sprintf("  %s: runtime_config.%s is empty — %s", label, f.Key, f.Doc))
				continue
			// This value is handed to a CLI as one argv word. Whitespace around
			// it is invisible in the file and rejected by the CLI much later,
			// inside a run that has already been paid for.
			case s != strings.TrimSpace(s):
				problems = append(problems, fmt.Sprintf("  %s: runtime_config.%s has leading or trailing whitespace — it is passed to the\n"+
					"      runtime verbatim, so write it as %q", label, f.Key, strings.TrimSpace(s)))
				continue
			}
			// A value outside a declared set is a typo the CLI would only reject
			// from inside a run. `workspace_write` for `workspace-write` is the
			// shape of it: plausible, wrong, and expensive to find out later.
			//
			// An alias resolves HERE rather than being handed on, so everything
			// downstream — the argv, worker.log, the dashboard — names the model
			// that actually ran rather than a family whose latest member moves.
			if canonical, ok := f.Aliases[s]; ok {
				s = canonical
			}
			if len(f.Enum) > 0 && !contains(f.Enum, s) {
				if by := movingEnumStoodDownBy(f); by != "" {
					warnUnlistedValue(label, runtimeName, f, s, by)
					out[f.Key] = s
					continue
				}
				problems = append(problems, unlistedValueProblem(label, runtimeName, f, s))
				continue
			}
			out[f.Key] = s
		}
	}

	var unknown []string
	for k := range block {
		if !known[k] {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	for _, k := range unknown {
		problems = append(problems, fmt.Sprintf("  %s: runtime_config.%s is not a setting runtime %q accepts (it takes: %s)",
			label, k, runtimeName, strings.Join(fieldKeys(fields), ", ")))
	}

	return out, problems
}

// modelCheckEnv stands down a check on a list this repo does not own.
//
// The model names a runtime declares are a snapshot: a provider can ship one
// between two relay-cli releases, and an operator who reads that announcement
// should not have to wait for a release here to run it. Without an escape
// hatch, a check meant to save one paid run would cost every run until someone
// cut a version — which is the check causing the outage it exists to prevent,
// the same trap the CLI capability probe is written around.
//
// It is deliberately not the default. A name outside the list is a typo far
// more often than it is tomorrow's model, and the operator setting this has
// said out loud which one they have.
const modelCheckEnv = "RELAY_CLI_SKIP_MODEL_CHECK"

// movingEnumStoodDownBy names the variable letting an unlisted value through,
// or "" when the check stands.
//
// RELAY_CLI_SKIP_RUNTIME_CHECK counts too: it already means "this install is
// newer or stranger than this build knows", and someone who set it to get past
// a reshaped --help should not then be stopped by a stale model list for the
// same reason.
func movingEnumStoodDownBy(f runtimeField) string {
	if !f.EnumMoves {
		return ""
	}
	for _, name := range []string{modelCheckEnv, "RELAY_CLI_SKIP_RUNTIME_CHECK"} {
		if os.Getenv(name) != "" {
			return name
		}
	}
	return ""
}

// unlistedValueProblem reports a value outside a declared set, and for a set
// that MOVES it also carries the way past it. Naming the escape hatch in the
// error is the whole reason a snapshot can be enforced at all: the alternative
// is an operator with a valid new model and a fleet that will not start,
// reading the source to find out why.
func unlistedValueProblem(label, runtimeName string, f runtimeField, s string) string {
	msg := fmt.Sprintf("  %s: runtime_config.%s is %q — it takes one of: %s",
		label, f.Key, s, strings.Join(f.Enum, ", "))
	if aliases := aliasNames(f); len(aliases) > 0 {
		msg += fmt.Sprintf("\n      or one of these, which relay-cli pins to the id beside it: %s",
			strings.Join(aliases, ", "))
	}
	if f.EnumMoves {
		msg += fmt.Sprintf("\n      That is the list runtime %q offered when this relay-cli was built. If\n"+
			"      %q is newer than this build, set %s=1 to run it anyway.",
			runtimeName, s, modelCheckEnv)
	}
	return msg
}

// warnUnlistedValue says out loud what was let through and why, for the same
// reason the sign-in stand-down does: relay-cli is trusting something it cannot
// verify. Silence here would turn a typo into a fleet that fails every cycle
// with the answer sitting in an environment variable nobody remembers setting.
func warnUnlistedValue(label, runtimeName string, f runtimeField, s, by string) {
	fmt.Fprintf(warnOut, "warning: %s sets runtime_config.%s to %q, which is not one runtime %q offered when\n"+
		"         this relay-cli was built (%s) — %s is set, so it is passed through\n"+
		"         unchecked. If the name is a typo, every run will fail on it.\n",
		label, f.Key, s, runtimeName, strings.Join(f.Enum, ", "), by)
}

// aliasNames lists a field's aliases as "alias (id)", ordered by the value they
// resolve to so the line reads in the same order as the one above it. Sorted
// rather than ranged over directly: a map order would reshuffle the error text
// between two runs against the same config.
func aliasNames(f runtimeField) []string {
	out := make([]string, 0, len(f.Aliases))
	for a := range f.Aliases {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		if ii, jj := indexOf(f.Enum, f.Aliases[out[i]]), indexOf(f.Enum, f.Aliases[out[j]]); ii != jj {
			return ii < jj
		}
		return out[i] < out[j]
	})
	for i, a := range out {
		out[i] = fmt.Sprintf("%s (%s)", a, f.Aliases[a])
	}
	return out
}

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return len(list)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func fieldKeys(fields []runtimeField) []string {
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, f.Key)
	}
	return out
}

// runtimeFields asks a runtime what it accepts, without asking whether its CLI
// is installed. Resolving an adapter is pure; proving the CLI works is not, and
// the config parser's own tests have to run on a machine with no coding CLI.
func runtimeFields(name, relayDir string) ([]runtimeField, error) {
	rt, err := ResolveRuntime(name, relayDir)
	if err != nil {
		return nil, err
	}
	return rt.ConfigFields(), nil
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
var checkRuntime = func(name, runtime, relayDir string) error {
	rt, err := ResolveRuntime(runtime, relayDir)
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
