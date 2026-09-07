package api

import (
	"net/http/httptest"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/grab"
)

// TestGrabPayloadNamesTheJob pins where a download client's job name comes from: the
// caller's own title when it sent one, else the release title sealed into the link, and
// only then the indexer ID. A caller that posts a served link back verbatim (no title of
// its own) is the case the sealed name exists for.
func TestGrabPayloadNamesTheJob(t *testing.T) {
	t.Parallel()
	const title = "Show.S01E01.2160p.WEB-DL.HEVC-GRP"
	rt := &router{dlToken: testKeyring(t)}
	idx := fakeSearchIndexer{id: "demo", needsResolver: true}
	rw := grab.NewManagementDLRewriter(rt.dlToken, idx, "http://h.test/api/indexers/demo/download")
	sealed, _, ok := rw(keyLink, title, []int{2000})
	if !ok {
		t.Fatal("expected the link to be sealed")
	}

	tests := []struct {
		name string
		req  grabRequest
		want string
	}{
		{name: "caller title wins", req: grabRequest{Indexer: "demo", Link: sealed, Name: "CallerChosen"}, want: "CallerChosen"},
		{name: "sealed title fills in", req: grabRequest{Indexer: "demo", Link: sealed}, want: title},
		{name: "unsealed link falls back to the indexer", req: grabRequest{Indexer: "demo", Link: "https://elsewhere.test/x.torrent"}, want: "demo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w := httptest.NewRecorder()
			p, ok := rt.grabPayload(w, searchReq(t), idx, tt.req)
			if !ok {
				t.Fatalf("grabPayload failed: %d %s", w.Code, w.Body)
			}
			if p.Name != tt.want {
				t.Errorf("job name = %q, want %q", p.Name, tt.want)
			}
		})
	}
}
