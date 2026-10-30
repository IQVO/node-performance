package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	archgo "github.com/arch-go/arch-go/api"
	"github.com/arch-go/arch-go/api/configuration"
)

// TestA2AAdapterDependencyRule encodes ADR 0011: the A2A inbound adapter is
// additive, never load-bearing. It may depend only on the application and
// domain layers (never on outbound adapters, never on cmd), and nothing else
// in this codebase may depend on it; cmd/a2a wires it up and nothing else
// touches it.
func TestA2AAdapterDependencyRule(t *testing.T) {
	moduleInfo := configuration.Load(modulePath)

	t.Run("a2a adapter depends only on application and domain", func(t *testing.T) {
		result := archgo.CheckArchitecture(moduleInfo, configuration.Config{
			DependenciesRules: []*configuration.DependenciesRule{
				{
					Package: "**.internal.adapters.inbound.a2a.**",
					ShouldOnlyDependsOn: &configuration.Dependencies{
						Internal: []string{
							"**.internal.adapters.inbound.a2a.**",
							"**.internal.application.**",
							"**.internal.domain.**",
						},
					},
				},
			},
		})

		assertArchGoPasses(t, result)
	})

	t.Run("nothing else depends on the a2a adapter", func(t *testing.T) {
		result := archgo.CheckArchitecture(moduleInfo, configuration.Config{
			DependenciesRules: []*configuration.DependenciesRule{
				{
					Package: "**.internal.**",
					ShouldNotDependsOn: &configuration.Dependencies{
						Internal: []string{"**.internal.adapters.inbound.a2a.**"},
					},
				},
			},
		})

		assertArchGoPasses(t, result)
	})
}

// a2aAllowedUsecases is the ALLOWLIST of use-case identifiers the A2A agent may
// reference: the two read queries and the input type of one of them. This
// context's MCP charter is entirely read-only (ADR 0007), so the A2A agent
// derived from it is read-only too (ADR 0011): an allowlist, not a denylist of
// write names, so a write use case added tomorrow is refused by default.
var a2aAllowedUsecases = map[string]bool{"Queries": true, "ListByGradeInput": true}

// a2aAllowedPorts is the ALLOWLIST of port identifiers the adapter may
// reference: the not-found sentinel its problem mapping classifies. No unit of
// work, repository write, publisher or outbox port is reachable.
var a2aAllowedPorts = map[string]bool{"ErrNotFound": true}

// a2aBannedImports are import path fragments the A2A agent must never carry:
// an outbound A2A client (this context calls no sibling), Kafka, the outbox and
// the in-memory adapters (real Postgres only).
var a2aBannedImports = []string{
	"a2a-go/v2/a2aclient",
	"segmentio/kafka-go",
	"internal/adapters/kafka",
	"internal/adapters/inbound/kafka",
	"internal/adapters/outbound/kafka",
	"internal/adapters/outbound/outbox",
	"internal/adapters/outbound/memory",
	"internal/adapters/outbound/repocontract",
}

// readFileOrFail returns the content of one source file.
func readFileOrFail(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

// parseNoComments parses one Go source without its comments, so only code
// counts: a comment may freely explain what is forbidden.
func parseNoComments(src string) (*ast.File, error) {
	return parser.ParseFile(token.NewFileSet(), "src.go", src, 0)
}

// qualifiedUses returns the identifiers of pkg that the file selects
// (pkg.Name), sorted.
func qualifiedUses(file *ast.File, pkg string) []string {
	seen := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok && id.Name == pkg {
			seen[sel.Sel.Name] = true
		}
		return true
	})
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// importPaths returns the import paths of a file.
func importPaths(file *ast.File) []string {
	out := make([]string, 0, len(file.Imports))
	for _, imp := range file.Imports {
		if p, err := strconv.Unquote(imp.Path.Value); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// outsideAllowlist returns every name of uses that the allowlist lacks.
func outsideAllowlist(uses []string, allowed map[string]bool) []string {
	var out []string
	for _, name := range uses {
		if !allowed[name] {
			out = append(out, name)
		}
	}
	return out
}

// bannedImportsIn returns the banned import paths a file carries.
func bannedImportsIn(file *ast.File) []string {
	var out []string
	for _, path := range importPaths(file) {
		for _, banned := range a2aBannedImports {
			if strings.Contains(path, banned) {
				out = append(out, path)
			}
		}
	}
	return out
}

// a2aReadOnlyViolations is the whole sensor over one non-test source.
func a2aReadOnlyViolations(src string) ([]string, error) {
	file, err := parseNoComments(src)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, name := range outsideAllowlist(qualifiedUses(file, "usecases"), a2aAllowedUsecases) {
		out = append(out, "usecases."+name+" is not on the read-only allowlist")
	}
	for _, name := range outsideAllowlist(qualifiedUses(file, "ports"), a2aAllowedPorts) {
		out = append(out, "ports."+name+" is not on the read-only allowlist")
	}
	for _, path := range bannedImportsIn(file) {
		out = append(out, "imports "+path+" (no outbound client, Kafka, outbox or in-memory runtime)")
	}
	return out, nil
}

// TestA2AAgentIsReadOnlyAndHasNoOutboundClient is the sensor behind the ADR
// 0011 boundaries: in the adapter AND the composition root (test files
// excluded: the wire tests legitimately drive the agent with the SDK client)
// only the read queries are referenced, no outbound A2A client, Kafka, outbox
// or in-memory runtime is imported.
func TestA2AAgentIsReadOnlyAndHasNoOutboundClient(t *testing.T) {
	files := append(goFilesUnder(t, "../adapters/inbound/a2a", false), goFilesUnder(t, "../../cmd/a2a", false)...)
	if len(files) < 6 {
		t.Fatalf("only %d A2A sources found; the sensor is looking in the wrong place", len(files))
	}
	for _, path := range files {
		src := readFileOrFail(t, path)
		violations, err := a2aReadOnlyViolations(src)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, v := range violations {
			t.Errorf("%s: %s", path, v)
		}
	}
}

// TestA2ADepsCarryOnlyTheTwoReadUseCases pins the shape of the adapter's Deps:
// exactly the two read use cases the MCP adapter has, so a write use case
// cannot be wired in without this test (and the ADR) changing.
func TestA2ADepsCarryOnlyTheTwoReadUseCases(t *testing.T) {
	file, err := parseNoComments(readFileOrFail(t, "../adapters/inbound/a2a/server.go"))
	if err != nil {
		t.Fatal(err)
	}
	got := depsFields(file)
	want := []string{"GetScorecard", "ListScorecards"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("a2a.Deps fields = %v, want exactly %v (read-only agent)", got, want)
	}
}

// depsFields returns the sorted field names of the struct type named Deps.
func depsFields(file *ast.File) []string {
	var out []string
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.TypeSpec)
		if !ok || spec.Name.Name != "Deps" {
			return true
		}
		if st, ok := spec.Type.(*ast.StructType); ok {
			for _, f := range st.Fields.List {
				for _, name := range f.Names {
					out = append(out, name.Name)
				}
			}
		}
		return false
	})
	sort.Strings(out)
	return out
}

// TestA2AReadOnlySensorBites proves the detector flags each forbidden shape in
// CODE (a comment mentioning it must not count), so the sensors above cannot
// pass vacuously.
func TestA2AReadOnlySensorBites(t *testing.T) {
	bad := map[string]string{
		"a write use case":        "package a2a\nvar _ = usecases.RecordObservation{}\n",
		"a recompute use case":    "package a2a\nvar _ = usecases.Recompute{}\n",
		"a unit of work port":     "package a2a\nvar _ ports.UnitOfWork\n",
		"an event publisher port": "package a2a\nvar _ ports.EventPublisher\n",
		"an outbound a2a client":  "package a2a\nimport \"github.com/a2aproject/a2a-go/v2/a2aclient\"\nvar _ = a2aclient.NewFromCard\n",
		"the kafka adapter":       "package main\nimport _ \"github.com/claudioed/node-performance/internal/adapters/outbound/kafka\"\n",
		"the outbox adapter":      "package main\nimport _ \"github.com/claudioed/node-performance/internal/adapters/outbound/outbox\"\n",
		"the in-memory adapter":   "package main\nimport _ \"github.com/claudioed/node-performance/internal/adapters/outbound/memory\"\n",
	}
	for name, src := range bad {
		got, err := a2aReadOnlyViolations(src)
		if err != nil || len(got) == 0 {
			t.Errorf("the sensor does not flag %s (violations %v, err %v)", name, got, err)
		}
	}

	good := "package a2a\n// usecases.RecordObservation and ports.UnitOfWork are forbidden, a2aclient too\n" +
		"var _ = usecases.Queries{}\nvar _ usecases.ListByGradeInput\nvar _ = ports.ErrNotFound\n"
	if got, err := a2aReadOnlyViolations(good); err != nil || len(got) != 0 {
		t.Errorf("false positive on the read use cases or on a comment: %v (err %v)", got, err)
	}

	withWrite := "package a2a\ntype Deps struct {\n\tGetScorecard int\n\tListScorecards int\n\tRecord int\n}\n"
	file, err := parseNoComments(withWrite)
	if err != nil {
		t.Fatal(err)
	}
	if got := depsFields(file); len(got) != 3 {
		t.Errorf("depsFields = %v, want to see the extra field", got)
	}
}
