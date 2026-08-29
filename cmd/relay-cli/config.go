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
	"encoding/json"
	"fmt"
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

// The two placeholders `relay-cli init` writes, rejected BY NAME.
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

// removedKeys are rejected BY NAME. Every other unknown key at worker level is
// ignored (each optional field is read with a fallback), which is exactly why
// these need saying out loud: a config still carrying system_prompt_file would
// otherwise launch an agent with no standing instructions at all and look fine
// doing it, and one carrying "model" at the top level would lose the model it
// asked for to a silent default.
var removedKeys = map[string]string{
	"system_prompt":            "agent identity now lives in relay: set the agent's instructions_md (update_agent, or the relay agent console). It reaches a RUNNING agent, which a local file never could",
	"system_prompt_file":       "agent identity now lives in relay: move the file's text into the agent's instructions_md (update_agent, or the relay agent console)",
	"min_run_interval_seconds": "replaced by a fixed 60s relaunch cooldown. To make a worker act less often, lower max_runs_per_hour",
	"permission_mode":          "a headless run is always fully autonomous — there is no prompt it could answer",
	"codex_mcp_transport":      "export CODEX_MCP_TRANSPORT=mcp-remote before launching instead",
	"runtime_args":             "removed. Raw argv could silently override the flags this harness depends on. Every setting a runtime accepts is now a declared key in \"runtime_config\"",
	"mcp_endpoint":             "renamed to \"relay_mcp\"",
	"model":                    "moved into \"runtime_config\": it is spelled in the runtime's own vocabulary, not relay-cli's",
	"max_budget_usd":           "moved into \"runtime_config\" and renamed to \"max_usd_per_run\": only some runtimes can enforce a spend cap",
	"run_timeout_seconds":      "renamed to \"max_seconds_per_run\"",
	"poll_frequency_seconds":   "renamed to \"poll_seconds\", and moved to the TOP LEVEL of the config: one poll rate for the fleet",
}

// stripLineComments removes // comments from JSON, quote-aware.
//
// The config is JSON, and JSON has no comments — but this is a file humans
// hand-edit, where "wait, is this one required?" is the question they have while
// editing it. So `relay-cli init` annotates each field where the field is, and
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
				"       Run \"relay-cli init\" to create one.", path)
		}
		return nil, err
	}

	stripped := stripLineComments(src)

	var doc struct {
		Workers json.RawMessage `json:"workers"`
		// Raw rather than *float64 so that "30" as a string reports itself as
		// the wrong TYPE for one field, instead of failing the whole document
		// as malformed JSON somewhere unnamed.
		PollSeconds json.RawMessage `json:"poll_seconds"`
	}
	if err := json.Unmarshal(stripped, &doc); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON.\n       (// comments are allowed and were stripped before parsing)\n       %v", path, err)
	}
	if len(doc.Workers) == 0 {
		return nil, fmt.Errorf("%s must contain a top-level \"workers\" array", path)
	}

	var raws []rawWorker
	if err := json.Unmarshal(doc.Workers, &raws); err != nil {
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
	if len(doc.PollSeconds) > 0 {
		if err := json.Unmarshal(doc.PollSeconds, &pollSeconds); err != nil {
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

		for _, f := range []struct{ key, val, why string }{
			{"name", w.Name, "unique, filesystem-safe — it becomes " + stateDirName + "/<name>/"},
			{"relay_mcp", w.Endpoint, "the connector_url from relay's issue_agent_credential, secret included"},
			{"repo_dir", w.RepoDir, "the checkout this worker's CLI runs in"},
			{"runtime", w.Runtime, "which CLI drives this worker"},
		} {
			if f.val == "" {
				problems = append(problems, fmt.Sprintf("  %s: missing %q — %s", label, f.key, f.why))
			}
		}

		if strings.Contains(w.Endpoint, endpointPlaceholder) {
			problems = append(problems, fmt.Sprintf("  %s: relay_mcp is still the placeholder from `relay-cli init` — paste the whole\n"+
				"      connector_url over it (relay: onboard_agent, then issue_agent_credential).\n"+
				"      The secret is part of that URL and is shown exactly once", label))
		}

		// A name becomes <state>/<name>/, so it has to be a single path segment.
		// The shell poller this replaced only asked for that in a comment; a
		// worker called "a/b" silently wrote its state somewhere else entirely.
		if w.Name != "" && (strings.ContainsAny(w.Name, "/\\") || w.Name == "." || w.Name == "..") {
			problems = append(problems, fmt.Sprintf("  %s: name is not filesystem-safe — it becomes %s/<name>/, so it may not contain \"/\" or \"\\\"", label, stateDirName))
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

		for _, f := range []struct {
			key string
			val int
		}{
			{"max_runs_per_hour", w.MaxRunsPerHour},
			{"max_seconds_per_run", w.MaxSecondsPerRun},
		} {
			if v, ok := raw[f.key]; ok {
				var n float64
				if json.Unmarshal(v, &n) != nil || n < 0 {
					problems = append(problems, fmt.Sprintf("  %s: %q must be a non-negative JSON number (30, not \"30\")", label, f.key))
				}
			}
		}

		// The repo has to exist now rather than at the first launch: a worker
		// that polls happily for an hour and then cannot start is a worse way to
		// learn about a typo than a line at startup.
		switch {
		case w.RepoDir == repoDirPlaceholder:
			problems = append(problems, fmt.Sprintf("  %s: repo_dir is still the placeholder from `relay-cli init` — point it at the\n"+
				"      checkout this agent should work in, and one you are willing to have rewritten", label))
		case w.RepoDir != "":
			expanded := expandTilde(w.RepoDir)
			info, err := os.Stat(expanded)
			if err != nil || !info.IsDir() {
				problems = append(problems, fmt.Sprintf("  %s: repo_dir %q is not a directory", label, w.RepoDir))
			}
			w.RepoDir = expanded
		}

		// runtime_config is validated against what the named runtime actually
		// accepts, so an unsupported key is refused here instead of being
		// silently ignored for the life of the fleet.
		if w.Runtime != "" {
			fields, err := runtimeFields(w.Runtime, cfg.RelayDir)
			if err != nil {
				problems = append(problems, fmt.Sprintf("  %s: %v", label, err))
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
			"       runtime. See docs/configuration.md for the full reference.",
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
		case fieldNumber:
			var n float64
			if json.Unmarshal(v, &n) != nil || n < 0 {
				problems = append(problems, fmt.Sprintf("  %s: runtime_config.%s must be a non-negative JSON number (5, not \"5\")", label, f.Key))
				continue
			}
			out[f.Key] = strconv.FormatFloat(n, 'f', -1, 64)
		default:
			var s string
			if json.Unmarshal(v, &s) != nil || s == "" {
				problems = append(problems, fmt.Sprintf("  %s: runtime_config.%s must be a non-empty string", label, f.Key))
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
