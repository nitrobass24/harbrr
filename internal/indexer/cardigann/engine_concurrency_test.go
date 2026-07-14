package cardigann

import (
	"fmt"
	"sync"
	"testing"
)

// TestSearch_ConcurrentQueriesDoNotRace drives many concurrent Search calls
// through ONE shared Engine (and thus one shared selector.Engine and one shared
// login.Executor) with DIFFERENT queries, under -race. The selector's
// row-extraction seam is stateless (eval is passed per call, never mutated
// on shared engine state), so this must be race-free AND every goroutine must
// see only its own query's andmatch-filtered result — never another
// goroutine's in-flight .Result/eval state. Run with `go test -race` to be
// meaningful; without -race this only checks correctness.
func TestSearch_ConcurrentQueriesDoNotRace(t *testing.T) {
	t.Parallel()

	def := loadFixtureDef(t, "html_scrape.yml")
	doer := &engineReplay{body: string(readBody(t, "html_scrape.html"))}
	eng, err := NewEngine(def, WithClock(fixedClock()), WithDoer(doer))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	// The fixture body carries two rows: "Big Buck Bunny 1080p" and "Sintel
	// Series S01E02". The andmatch row filter means each query below keeps
	// exactly its own row — proving concurrent searches never cross-contaminate
	// each other's per-row template/eval state.
	cases := []struct {
		keywords  string
		wantTitle string
	}{
		{"bunny", "Big Buck Bunny 1080p"},
		{"sintel", "Sintel Series S01E02"},
	}

	const iterations = 25
	var wg sync.WaitGroup
	errCh := make(chan error, iterations*len(cases))
	for i := 0; i < iterations; i++ {
		for _, tc := range cases {
			wg.Add(1)
			go func(keywords, wantTitle string) {
				defer wg.Done()
				releases, err := eng.Search(t.Context(), Query{Keywords: keywords})
				if err != nil {
					errCh <- err
					return
				}
				if len(releases) != 1 {
					errCh <- fmt.Errorf("keywords %q: releases = %d, want 1", keywords, len(releases))
					return
				}
				if releases[0].Title != wantTitle {
					errCh <- fmt.Errorf("keywords %q: title = %q, want %q", keywords, releases[0].Title, wantTitle)
				}
			}(tc.keywords, tc.wantTitle)
		}
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}
