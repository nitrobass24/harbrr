package smoke

import "testing"

// TestMatchProwlarrIndexer pins the exact-only matching (no fuzzy/prefix) using the real
// harbrr↔Prowlarr name divergences observed in a live run: display-name matches, slug↔def
// matches, the Prowlarr "-api" variant, the curated upstream aliases, and the genuine
// no-matches that must stay unmatched.
func TestMatchProwlarrIndexer(t *testing.T) {
	t.Parallel()
	list := []prowlarrIndexer{
		{ID: 1, Name: "DigitalCore (API)", DefinitionName: "digitalcore-api"},
		{ID: 2, Name: "HDspace", DefinitionName: "HD-Space"},
		{ID: 3, Name: "IPTorrents2", DefinitionName: "IPTorrents"},
		{ID: 4, Name: "seedpool (API)", DefinitionName: "seedpool-api"},
		{ID: 5, Name: "BroadcasTheNet", DefinitionName: "BroadcasTheNet"},
		{ID: 6, Name: "FileList.io", DefinitionName: "FileList.io"},
	}
	tests := []struct {
		name    string
		hName   string
		hSlug   string
		wantID  int
		wantHit bool
	}{
		{"display-name match ignores punctuation", "DigitalCore (API)", "digitalcore", 1, true},
		{"display-name match with case/dash", "HDspace", "hdspace", 2, true},
		{"slug matches definitionName", "IPTorrents", "iptorrents", 3, true},
		{"slug matches the -api variant", "seedpool", "seedpool", 4, true},
		{"upstream alias bridges Prowlarr's typo'd name", "BroadcastTheNet", "btn", 5, true},
		{"upstream alias bridges the .io suffix", "FileList", "filelist", 6, true},
		{"unknown tracker stays unmatched", "SomeTracker", "sometracker", 0, false},
		{"empty keys never match", "", "", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			id, ok := matchProwlarrIndexer(tt.hName, tt.hSlug, list)
			if ok != tt.wantHit || id != tt.wantID {
				t.Errorf("matchProwlarrIndexer(%q,%q) = (%d,%v), want (%d,%v)",
					tt.hName, tt.hSlug, id, ok, tt.wantID, tt.wantHit)
			}
		})
	}
}
