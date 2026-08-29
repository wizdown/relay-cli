package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRedactURLKeepsHostDropsSecret(t *testing.T) {
	got := RedactURL("https://relay.example.com/relay/mcp/c/wzh_abcdefghijkl")
	if strings.Contains(got, "abcdefghijkl") {
		t.Fatalf("secret survived redaction: %s", got)
	}
	if !strings.Contains(got, "relay.example.com") {
		t.Fatalf("host should survive so a worker stays identifiable: %s", got)
	}
}

// An endpoint that is not in the /c/<secret> shape is still a credential.
func TestRedactURLUnknownShapeIsFullyHidden(t *testing.T) {
	if got := RedactURL("https://relay.example.com/mcp?token=abcdefghijkl"); strings.Contains(got, "abcdefghijkl") {
		t.Fatalf("secret survived: %s", got)
	}
}

func TestScrubCatchesBareTokenAndFullURL(t *testing.T) {
	InstallSecrets([]*Worker{{Endpoint: "https://r.example/relay/mcp/c/wzh_longsecrettoken"}})
	defer InstallSecrets(nil)

	for _, in := range []string{
		"connecting to https://r.example/relay/mcp/c/wzh_longsecrettoken now",
		`{"connector":"wzh_longsecrettoken"}`,
		"failed for /c/wzh_someotherunknownsecret",
	} {
		out := Scrub(in)
		if strings.Contains(out, "wzh_longsecrettoken") || strings.Contains(out, "wzh_someotherunknownsecret") {
			t.Errorf("Scrub(%q) leaked: %q", in, out)
		}
	}
}

// A short token is not redacted on purpose: a three-character replacement target
// would rewrite unrelated text all over the UI.
func TestScrubIgnoresImplausiblyShortTokens(t *testing.T) {
	InstallSecrets([]*Worker{{Endpoint: "https://r.example/relay/mcp/c/ab"}})
	defer InstallSecrets(nil)
	if got := Scrub("a cab and a taxi"); got != "a cab and a taxi" {
		t.Fatalf("short token caused collateral rewriting: %q", got)
	}
}

// The snapshot the browser receives must never carry a raw endpoint. Worker.Endpoint
// is json:"-" for exactly this reason; this pins it.
func TestWorkerJSONOmitsRawEndpoint(t *testing.T) {
	w := &Worker{Name: "w", Endpoint: "https://r.example/relay/mcp/c/wzh_longsecrettoken"}
	w.EndpointRedacted = RedactURL(w.Endpoint)
	b, err := jsonMarshal(w)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "wzh_longsecrettoken") {
		t.Fatalf("serialized worker leaked its credential: %s", b)
	}
	if !strings.Contains(string(b), "wzh_REDACTED") {
		t.Fatalf("serialized worker should carry the redacted form: %s", b)
	}
}

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }
