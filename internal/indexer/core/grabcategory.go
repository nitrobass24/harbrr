package core

import "context"

// grabCategoryKey is the unexported context key carrying the parent category of the
// release a grab is for. It travels on the context rather than in Indexer.Grab's
// signature because the category is pure observability (autobrr/harbrr#403): it exists
// only so the stats layer can tally the grab under the right family, and no driver
// reads it. The /dl proxy seals the category into the download token alongside the
// link, so it survives the round trip to the consumer and back.
type grabCategoryKey struct{}

// WithGrabCategory marks ctx with the grabbed release's standard PARENT category id
// (0 = unknown), for the per-category grab tally.
func WithGrabCategory(ctx context.Context, categoryID int) context.Context {
	return context.WithValue(ctx, grabCategoryKey{}, categoryID)
}

// GrabCategory returns the parent category id ctx carries, or 0 when the grab arrived
// without one (an older token, a magnet, or a caller outside the /dl proxy).
func GrabCategory(ctx context.Context) int {
	id, _ := ctx.Value(grabCategoryKey{}).(int)
	return id
}
