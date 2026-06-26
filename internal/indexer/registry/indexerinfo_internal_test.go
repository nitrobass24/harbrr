package registry

import (
	"testing"

	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// TestIndexerInfoSourcesProtocolFromInstance guards that the served indexer
// identity takes Protocol from the persisted instance (the denormalized column),
// not by re-deriving it from the definition. The two are equal on the happy path
// (Add seeds the instance from the def), so the test forces them to disagree to
// prove the source: instance says usenet, def says torrent -> served = usenet.
func TestIndexerInfoSourcesProtocolFromInstance(t *testing.T) {
	t.Parallel()

	inst := domain.IndexerInstance{Slug: "s", Name: "S", Protocol: "usenet"}
	def := &loader.Definition{Name: "S"} // empty Protocol => EffectiveProtocol() == "torrent"

	if got := def.EffectiveProtocol(); got != "torrent" {
		t.Fatalf("precondition: def.EffectiveProtocol() = %q, want torrent", got)
	}
	if got := indexerInfo(inst, def).Protocol; got != "usenet" {
		t.Errorf("indexerInfo Protocol = %q, want usenet (must come from the instance, not the def)", got)
	}
}
