package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/database/dbinterface"
)

// hideAdultCategories is the operator's "hide adult categories" choice. Absent or
// unreadable means false — the setting is off by default and an instance that never
// touches it behaves exactly as it did before the setting existed
// (autobrr/harbrr#383).
var hideAdultCategories = database.Setting[bool]{
	Key:     "hide_adult_categories",
	Default: false,
	Parse:   strconv.ParseBool,
	Format:  strconv.FormatBool,
}

// AdultCategoriesStore is the global hide-adult-categories dial: it holds the
// live value in memory (every category-listing and search response consults it,
// so it must not cost a DB read per request) and persists changes to
// app_settings so the choice survives a restart.
//
// The setting hides a taxonomy from the management API's category surfaces and
// drops XXX-category results from an uncategorised UI search. It deliberately
// does NOT touch the Torznab/Newznab feed: feed consumers are automation that
// already carries its own category selection.
type AdultCategoriesStore struct {
	db     dbinterface.Execer
	now    func() time.Time
	hidden atomic.Bool
}

// NewAdultCategoriesStore builds a store over db, defaulting to "not hidden"
// until LoadPersisted reads the stored value. now supplies the persisted-at
// timestamp (injectable for tests); nil uses time.Now.
func NewAdultCategoriesStore(db dbinterface.Execer, now func() time.Time) *AdultCategoriesStore {
	if now == nil {
		now = time.Now
	}
	return &AdultCategoriesStore{db: db, now: now}
}

// Hidden reports whether adult categories are currently hidden. A nil store
// (the setting is unwired, e.g. in a narrow test router) reports false, so the
// unconfigured path is the pre-setting behaviour.
func (s *AdultCategoriesStore) Hidden() bool {
	return s != nil && s.hidden.Load()
}

// Set persists the choice and then applies it live. Persisting first means a
// write failure leaves the running value untouched rather than desynchronizing
// runtime and stored state.
func (s *AdultCategoriesStore) Set(ctx context.Context, hidden bool) error {
	if err := hideAdultCategories.Write(ctx, s.db, hidden, s.now()); err != nil {
		return fmt.Errorf("api: persist hide-adult-categories: %w", err)
	}
	s.hidden.Store(hidden)
	return nil
}

// LoadPersisted applies the stored value at startup. A missing or unusable row
// leaves the default (false) in place, so a stale row can never wedge boot.
func (s *AdultCategoriesStore) LoadPersisted(ctx context.Context, log zerolog.Logger) {
	s.hidden.Store(hideAdultCategories.Read(ctx, s.db, log))
}

// adultCategoriesBody is the shared request/response shape for the endpoints.
type adultCategoriesBody struct {
	Hidden bool `json:"hidden"`
}

// getAdultCategories returns whether adult categories are currently hidden.
func (rt *router) getAdultCategories(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, adultCategoriesBody{Hidden: rt.adultCats.Hidden()})
}

// putAdultCategories sets the global hide-adult-categories choice. It takes
// effect on the next request and survives a restart.
func (rt *router) putAdultCategories(w http.ResponseWriter, r *http.Request) {
	if rt.adultCats == nil {
		writeError(w, http.StatusServiceUnavailable, "adult-category filtering is unavailable")
		return
	}
	var req adultCategoriesBody
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.adultCats.Set(r.Context(), req.Hidden); err != nil {
		rt.writeServiceError(w, "hide-adult-categories", err)
		return
	}
	writeJSON(w, http.StatusOK, adultCategoriesBody{Hidden: rt.adultCats.Hidden()})
}
