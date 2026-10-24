package mcp_test

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// update regenerates testdata/tool_registry.golden.json:
//
//	go test ./internal/adapters/inbound/mcp -run TestToolRegistryGolden -update
var update = flag.Bool("update", false, "rewrite the MCP tool registry golden file")

// wantTools is the curated, READ-ONLY tool set (docs/adr/0007): one tool per
// read operation of apis/openapi.yaml, nothing else.
var wantTools = []string{"get_scorecard", "list_scorecards_by_grade"}

// maxTools is the tool budget. Raising it is a deliberate, reviewed act (an
// ADR 0007 amendment): TestToolSurface still pins the exact curated set.
const maxTools = 2

// writeVerbs are the verbs of this service's write path (recording
// observations from the consumed events, recomputing grades, publishing the
// graded events) and the generic mutators. A tool whose name contains one of
// them as a word is a write tool, which the read-only MCP surface must never
// expose: writes happen in cmd/api's consumers and scheduler, through the
// transactional outbox.
var writeVerbs = []string{
	"record", "recompute", "apply", "publish", "declare", "issue", "consume",
	"expire", "supersede", "create", "update", "delete", "set", "put", "post",
	"assign", "claim", "complete", "grade",
}

// writeVerbViolations returns one message per tool name carrying a write
// verb as one of its snake_case words. ("grade" is a write verb here even
// though it is also a filter noun: this service's tools name it only inside
// list_scorecards_by_grade, and the check below excludes that curated set.)
func writeVerbViolations(names []string) []string {
	var out []string
	for _, name := range names {
		for _, word := range strings.Split(name, "_") {
			for _, verb := range writeVerbs {
				if word == verb {
					out = append(out, name+": contains the write verb "+verb+" (the MCP surface is read-only, docs/adr/0007)")
				}
			}
		}
	}
	return filterCurated(out)
}

// filterCurated drops the one known-safe hit: "grade" inside the curated
// list_scorecards_by_grade name, where it is the filter the REST API itself
// names (GET /scorecards?grade=), not the act of grading.
func filterCurated(violations []string) []string {
	var out []string
	for _, v := range violations {
		if strings.HasPrefix(v, "list_scorecards_by_grade: contains the write verb grade") {
			continue
		}
		out = append(out, v)
	}
	return out
}

// The MCP governance charter's mechanical gate: the advertised tool set is
// exactly the curated read-only one, within the budget, snake_case
// verb_noun with NO write verb, annotated read-only, described, and every
// argument is lowerCamelCase (like the REST contract) and documented.
func TestToolSurface(t *testing.T) {
	h := newHarness(t, nodeFixture())
	res, err := h.session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(res.Tools) > maxTools {
		t.Fatalf("advertised %d tools, over the budget of %d", len(res.Tools), maxTools)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		checkToolMetadata(t, tool)
		checkToolArguments(t, tool)
	}
	sort.Strings(names)
	if strings.Join(names, ",") != strings.Join(wantTools, ",") {
		t.Fatalf("advertised tools %v, want exactly %v", names, wantTools)
	}
	for _, v := range writeVerbViolations(names) {
		t.Error(v)
	}
}

// The write-verb sensor must actually be able to fail: every write verb in a
// tool name is flagged, the curated read names are not.
func TestWriteVerbSensorFailsOnWriteNames(t *testing.T) {
	for _, verb := range writeVerbs {
		for _, name := range []string{verb + "_scorecard", "bulk_" + verb + "_nodes"} {
			if got := writeVerbViolations([]string{name}); len(got) == 0 {
				t.Errorf("write tool name %q was not flagged", name)
			}
		}
	}
	if got := writeVerbViolations(wantTools); len(got) != 0 {
		t.Errorf("read tool names were flagged: %v", got)
	}
	// Substrings of a word are not verbs: "dataset" is not "set", "regraded"
	// is not "grade".
	if got := writeVerbViolations([]string{"get_dataset", "list_regraded_nodes"}); len(got) != 0 {
		t.Errorf("false positive on non-verb words: %v", got)
	}
}

var (
	toolNaming = regexp.MustCompile(`^[a-z]+(_[a-z]+)+$`)
	argNaming  = regexp.MustCompile(`^[a-z][a-zA-Z0-9]*$`)
)

func checkToolMetadata(t *testing.T, tool *sdk.Tool) {
	t.Helper()
	if !toolNaming.MatchString(tool.Name) {
		t.Errorf("tool %q is not snake_case verb_noun", tool.Name)
	}
	if tool.Description == "" {
		t.Errorf("tool %q has no description", tool.Name)
	}
	if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
		t.Errorf("tool %q is not annotated read-only: %+v", tool.Name, tool.Annotations)
		return
	}
	if tool.Annotations.DestructiveHint != nil && *tool.Annotations.DestructiveHint {
		t.Errorf("tool %q is annotated destructive", tool.Name)
	}
}

func checkToolArguments(t *testing.T, tool *sdk.Tool) {
	t.Helper()
	schema, isMap := tool.InputSchema.(map[string]any)
	if !isMap {
		t.Errorf("tool %q input schema = %T", tool.Name, tool.InputSchema)
		return
	}
	props, _ := schema["properties"].(map[string]any)
	if len(props) == 0 {
		t.Errorf("tool %q has no arguments in its schema", tool.Name)
	}
	for arg, p := range props {
		if !argNaming.MatchString(arg) {
			t.Errorf("tool %q argument %q is not lowerCamelCase", tool.Name, arg)
		}
		if d, _ := p.(map[string]any)["description"].(string); d == "" {
			t.Errorf("tool %q argument %q has no description", tool.Name, arg)
		}
	}
}

// TestToolRegistryGolden pins the whole advertised registry (names,
// descriptions, annotations, input AND output schemas) byte for byte, so any
// change to what an MCP client sees shows up as a reviewed golden diff.
func TestToolRegistryGolden(t *testing.T) {
	h := newHarness(t, nodeFixture())
	res, err := h.session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	sort.Slice(res.Tools, func(i, j int) bool { return res.Tools[i].Name < res.Tools[j].Name })
	got, err := json.MarshalIndent(res.Tools, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')

	path := filepath.Join("testdata", "tool_registry.golden.json")
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update to create it): %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("the MCP tool registry drifted from %s; review the change and re-run with -update.\ngot:\n%s", path, got)
	}
}

// Every optional input is published as optional: a model that leaves a
// filter out must not be rejected by schema validation.
func TestOptionalInputsAreNotRequired(t *testing.T) {
	h := newHarness(t, nodeFixture())
	res, err := h.session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	for _, tool := range res.Tools {
		if tool.Name != "list_scorecards_by_grade" {
			continue
		}
		schema := tool.InputSchema.(map[string]any)
		req, ok := schema["required"].([]any)
		if !ok {
			t.Fatalf("list_scorecards_by_grade publishes no required list: %v", schema)
		}
		want := []any{"grade"}
		if len(req) != len(want) || req[0] != want[0] {
			t.Fatalf("list_scorecards_by_grade requires %v, want only %v", req, want)
		}
	}
}
