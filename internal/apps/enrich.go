package apps

import (
	"context"

	"github.com/autobrr/harbrr/internal/domain"
)

// Bind resolves a surface row's App and decrypts its credential in one step — the
// driver-build pairing every surface repeats. Errors come back with apps' own
// prefixes; callers wrap once.
func (s *Service) Bind(ctx context.Context, appID int64) (domain.App, string, error) {
	app, err := s.Get(ctx, appID)
	if err != nil {
		return domain.App{}, "", err
	}
	key, err := s.DecryptKey(app)
	if err != nil {
		return domain.App{}, "", err
	}
	return app, key, nil
}

// EnrichList overwrites each row's App-projected display fields from one Index
// lookup. A nil app id, or an id missing from the index, keeps the row's stored
// fields — the list path tolerates a dangling reference; EnrichOne errors instead.
func EnrichList[Row any](ctx context.Context, s *Service, rows []Row, appID func(*Row) *int64, apply func(*Row, domain.App)) error {
	index, err := s.Index(ctx)
	if err != nil {
		return err
	}
	for i := range rows {
		if id := appID(&rows[i]); id != nil {
			if app, ok := index[*id]; ok {
				apply(&rows[i], app)
			}
		}
	}
	return nil
}

// EnrichOne overwrites one row's App-projected display fields. A nil app id is a
// no-op; a missing App is an error — the get path is strict where EnrichList skips.
func EnrichOne[Row any](ctx context.Context, s *Service, row *Row, appID func(*Row) *int64, apply func(*Row, domain.App)) error {
	id := appID(row)
	if id == nil {
		return nil
	}
	app, err := s.Get(ctx, *id)
	if err != nil {
		return err
	}
	apply(row, app)
	return nil
}
