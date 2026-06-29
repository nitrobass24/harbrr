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

// stageOrder is the frozen topological layering of the Cardigann pipeline.
// Earlier stages must NEVER import later (or same-position) stages, so the
// production import graph stays the acyclic, orthogonal DAG it is today:
//
//	encode, loader  ->  magnet, mapper, selector, regexadapter, dateparse, parity
//	                ->  template, filter, normalizer  ->  login  ->  search
//
// Maintenance: adding a new stage = insert one string at the correct position
// here; nothing else changes. The parent package internal/indexer/cardigann
// (engine.go) is the composition root above the stages, not a stage itself, so
// it is deliberately absent from this slice.
var stageOrder = []string{
	"encode", "loader", // layer 0
	"magnet", "mapper", "selector", "regexadapter", "dateparse", "parity", // layer 1
	"template", "filter", "normalizer", // layer 2
	"login",  // layer 3
	"search", // layer 4
}

const stagePrefix = "github.com/autobrr/harbrr/internal/indexer/cardigann/"

// TestPipelineIsAcyclicDAG freezes the pipeline's stage-to-stage dependency DAG.
// For each stage it parses the import lists of its non-test .go files (go/parser
// with ImportsOnly, so comments and strings that merely look like imports are
// ignored), keeps only imports under the cardigann engine, and asserts the
// importer's rank is strictly greater than each imported stage's rank. A
// strictly-greater check forbids both back-edges (cycles) and same-layer edges,
// mirroring the stdlib-AST architecture guard in
// internal/database/rebind_guard_test.go.
func TestPipelineIsAcyclicDAG(t *testing.T) {
	t.Parallel()
	rank := make(map[string]int, len(stageOrder))
	for i, s := range stageOrder {
		rank[s] = i
	}
	for _, stage := range stageOrder {
		for _, dep := range stageImports(t, stage) {
			depRank, ok := rank[dep]
			if !ok {
				continue // non-stage import (stdlib, third-party, engine root)
			}
			if depRank >= rank[stage] {
				t.Errorf("back-edge/cycle: stage %q (rank %d) imports %q (rank %d); "+
					"the pipeline is a linear DAG — earlier (and same-layer) stages may not import later ones",
					stage, rank[stage], dep, depRank)
			}
		}
	}
}

// stageImports returns the cardigann stage names directly imported by stage's
// non-test source. Any import path under stagePrefix is collapsed to its first
// path segment, so a future sub-package (…/search/foo) is attributed to its
// owning stage (search) before the rank lookup.
func stageImports(t *testing.T, stage string) []string {
	t.Helper()
	entries, err := os.ReadDir(stage)
	if err != nil {
		t.Fatalf("read %s: %v", stage, err)
	}
	fset := token.NewFileSet()
	var deps []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(stage, name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, spec := range f.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("unquote import %s in %s: %v", spec.Path.Value, name, err)
			}
			if rest, ok := strings.CutPrefix(path, stagePrefix); ok {
				seg, _, _ := strings.Cut(rest, "/") // collapse any sub-package to its owning stage
				deps = append(deps, seg)
			}
		}
	}
	return deps
}
