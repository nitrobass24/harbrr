package cardigann_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// stageLayers is the frozen topological layering of the Cardigann pipeline, one
// inner slice per layer. A stage may import only stages in STRICTLY EARLIER
// layers; stages within the same layer must stay orthogonal (no imports between
// them). Together that keeps the production import graph the acyclic, layered
// DAG it is today:
//
//	encode, loader  ->  mapper, selector, regexadapter, dateparse, parity
//	                ->  template, normalizer  ->  login  ->  search
//
// Maintenance: adding a new stage = insert one string into the correct layer
// (or append a new layer); nothing else changes. The parent package
// internal/indexer/cardigann (engine.go) is the composition root above the
// stages, not a stage itself, so it is deliberately absent here.
// Engine-private support stages (encode, selector, regexadapter, template)
// live under internal/; stageDir resolves either location, so a stage's rank
// here is independent of where its directory sits.
var stageLayers = [][]string{
	{"encode", "loader"},
	{"mapper", "selector", "regexadapter", "dateparse", "parity"},
	{"template", "normalizer"},
	{"login"},
	{"search"},
}

const stagePrefix = "github.com/autobrr/harbrr/internal/indexer/cardigann/"

// TestPipelineIsAcyclicDAG freezes the pipeline's stage-to-stage dependency DAG.
// For each stage it parses the import lists of its non-test .go files (go/parser
// with ImportsOnly, so comments and strings that merely look like imports are
// ignored), keeps only imports under the cardigann engine, and asserts the
// imported stage's layer is strictly earlier than the importer's. Comparing
// layers (not flat positions) forbids back-edges (cycles) AND same-layer edges
// in both directions, mirroring the stdlib-AST architecture guard in
// internal/database/rebind_guard_test.go.
func TestPipelineIsAcyclicDAG(t *testing.T) {
	t.Parallel()
	layer := map[string]int{}
	for i, group := range stageLayers {
		for _, s := range group {
			layer[s] = i
		}
	}
	for stageLayer, group := range stageLayers {
		for _, stage := range group {
			for _, dep := range stageImports(t, stage) {
				depLayer, ok := layer[dep]
				if !ok {
					continue // non-stage import (stdlib, third-party, engine root)
				}
				if depLayer >= stageLayer {
					t.Errorf("back-edge/cycle: stage %q (layer %d) imports %q (layer %d); "+
						"a stage may import only strictly-earlier layers, and same-layer stages must stay orthogonal",
						stage, stageLayer, dep, depLayer)
				}
			}
		}
	}
}

// stageImports returns the cardigann stage names directly imported by stage's
// non-test source. Any import path under stagePrefix is collapsed to its first
// path segment, so a future sub-package (…/search/foo) is attributed to its
// owning stage (search) before the rank lookup. An internal/ first segment is
// skipped over to the stage name beneath it, so relocated support stages keep
// their own rank instead of all collapsing to the unranked segment "internal".
func stageImports(t *testing.T, stage string) []string {
	t.Helper()
	dir := stageDir(stage)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	fset := token.NewFileSet()
	var deps []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, spec := range f.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("unquote import %s in %s: %v", spec.Path.Value, name, err)
			}
			if rest, ok := strings.CutPrefix(path, stagePrefix); ok {
				seg, more, _ := strings.Cut(rest, "/") // collapse any sub-package to its owning stage
				if seg == "internal" {                 // engine-private support stage: internal/<stage>/...
					seg, _, _ = strings.Cut(more, "/")
				}
				deps = append(deps, seg)
			}
		}
	}
	return deps
}

// stageDir resolves a stage name to its directory relative to this package:
// engine-private support stages live under internal/, pipeline stages sit
// directly beside this file.
func stageDir(stage string) string {
	nested := filepath.Join("internal", stage)
	if fi, err := os.Stat(nested); err == nil && fi.IsDir() {
		return nested
	}
	return stage
}
