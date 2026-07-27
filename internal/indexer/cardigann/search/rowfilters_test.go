package search

import (
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// TestAndMatchFoldPunctuation pins BOTH halves of the opt-in
// (autobrr/harbrr#394): wantOff is the default, Jackett-identical verdict and
// wantOn is the punctuation-tolerant one. Every case asserts both, so a change
// that leaked into the default path fails here as well as in the parity corpus.
func TestAndMatchFoldPunctuation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		title    string
		keywords string
		wantOff  bool
		wantOn   bool
	}{
		// The reported defect: Radarr strips the apostrophe, the tracker returns
		// the apostrophe title, and our own row filter deletes the answer.
		{
			name:  "stripped query vs apostrophe title",
			title: "Venise n'est pas en Italie 2019 1080p", keywords: "Venise nest pas en Italie",
			wantOff: false, wantOn: true,
		},
		{
			name:  "stripped query vs typographic apostrophe title",
			title: "Venise n’est pas en Italie 2019 1080p", keywords: "Venise nest pas en Italie",
			wantOff: false, wantOn: true,
		},
		{
			// U+02BC is Unicode Lm — a WORD character — so it survives tokenization
			// and has to be folded out of the TOKEN, not just the title.
			name:  "modifier-letter apostrophe in the query token",
			title: "Venise nest pas en Italie 2019", keywords: "Venise nʼest pas en Italie",
			wantOff: false, wantOn: true,
		},
		{
			// The other *arr-deleted characters: full stop and grave accent.
			name:  "stripped query vs dotted acronym title",
			title: "Marvel's Agents of S.H.I.E.L.D. S01E01", keywords: "Marvels Agents of SHIELD",
			wantOff: false, wantOn: true,
		},
		{
			name:  "stripped query vs grave-accent title",
			title: "Rock`n Roll 2017", keywords: "Rockn Roll",
			wantOff: false, wantOn: true,
		},
		{
			// Folding never rescues a genuinely absent token — the filter still
			// filters.
			name:  "unrelated row still dropped",
			title: "Debian 12 Netinst", keywords: "Ubuntu 24.04",
			wantOff: false, wantOn: false,
		},
		{
			// The reverse direction already worked without the opt-in: the
			// apostrophe is a tokenizer separator, so "n'est" yields "est" (the
			// leading "n" is dropped as .NET Length 1) which the stripped title
			// contains. Pinned so the claim is not folklore.
			name:  "apostrophe query vs stripped title needs no folding",
			title: "Venise nest pas en Italie 2019", keywords: "Venise n'est pas en Italie",
			wantOff: true, wantOn: true,
		},
		{
			// A hyphen is a separator on BOTH sides (*arr turns it into a space),
			// so it never needs folding — which is why it is not in the table.
			name:  "hyphen unaffected",
			title: "Spider-Man Far From Home 2019", keywords: "Spider Man Far From Home",
			wantOff: true, wantOn: true,
		},
		{
			name:  "stopwords still dropped with folding on",
			title: "Marvels Agents of SHIELD", keywords: "the and an Marvels",
			wantOff: true, wantOn: true,
		},
		{
			name:  "stopword-only mismatch still drops",
			title: "Marvels Agents of SHIELD", keywords: "the Debian",
			wantOff: false, wantOn: false,
		},
		{
			// CJK safety: the fold table is enumerated, so an ideographic full stop
			// (U+3002) is NOT folded. If it were, this joined query token would
			// wrongly match — the guard against reaching for a Unicode class.
			name:  "ideographic full stop not folded",
			title: "三体。刘慈欣 2023 WEB-DL", keywords: "三体刘慈欣",
			wantOff: false, wantOn: false,
		},
		{
			name:  "cjk tokens survive folding",
			title: "三体 刘慈欣 2023 WEB-DL", keywords: "三体 刘慈欣",
			wantOff: true, wantOn: true,
		},
		{
			name:  "cjk missing token still drops with folding on",
			title: "三体 2023 WEB-DL", keywords: "三体 刘慈欣",
			wantOff: false, wantOn: false,
		},
		{
			// The zero-token keep-everything path must not widen: it is reached the
			// same way with folding on or off (the length/stopword drops run on the
			// RAW token).
			name:  "empty keywords keep the row",
			title: "Anything At All", keywords: "",
			wantOff: true, wantOn: true,
		},
		{
			name:  "all-short keywords keep the row",
			title: "Anything At All", keywords: "a b c",
			wantOff: true, wantOn: true,
		},
		{
			// A token that folds away entirely carries no constraint; it must be
			// skipped rather than left as Contains("") — same keep verdict either
			// way here, but via the skip branch, not an empty-substring match.
			name:  "token folding to nothing carries no constraint",
			title: "Anything At All", keywords: "ʼʼ",
			wantOff: false, wantOn: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := andMatch(tc.title, tc.keywords, false); got != tc.wantOff {
				t.Errorf("andMatch(%q,%q,fold=false) = %v, want %v", tc.title, tc.keywords, got, tc.wantOff)
			}
			if got := andMatch(tc.title, tc.keywords, true); got != tc.wantOn {
				t.Errorf("andMatch(%q,%q,fold=true) = %v, want %v", tc.title, tc.keywords, got, tc.wantOn)
			}
		})
	}
}

// TestApplyRowFiltersFoldSeam pins the seam: the opt-in reaches the row filter
// through Deps, and the surrounding gating (ID searches skip andmatch) is
// unchanged by it.
func TestApplyRowFiltersFoldSeam(t *testing.T) {
	t.Parallel()

	filters := []loader.RowFilterBlock{{Name: "andmatch"}}
	const title = "Venise n'est pas en Italie 2019"
	query := Query{Keywords: "Venise nest pas en Italie"}

	if skip := applyRowFilters(filters, title, query, Deps{}); !skip {
		t.Error("default Deps must keep the Jackett behaviour (row skipped)")
	}
	if skip := applyRowFilters(filters, title, query, Deps{FoldAndMatchPunctuation: true}); skip {
		t.Error("FoldAndMatchPunctuation must keep the row")
	}

	// An ID search skips andmatch entirely, folding or not.
	idQuery := Query{Keywords: "nothing matching", IMDBID: "tt1234567"}
	for _, fold := range []bool{false, true} {
		if skip := applyRowFilters(filters, title, idQuery, Deps{FoldAndMatchPunctuation: fold}); skip {
			t.Errorf("id search must skip andmatch (fold=%v)", fold)
		}
	}
}
