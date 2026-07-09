package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/autobrr/harbrr/internal/database/dbinterface"
	"github.com/autobrr/harbrr/internal/domain"
)

// Solvers is the SQLite repository for global anti-bot-solver resources
// (FlareSolverr today). Stateless like the other resource repos; stores the
// opaque (already-encrypted) endpoint URL, encryption being the service's concern.
type Solvers struct{}

// solverColumns is the full select list, in scan order.
const solverColumns = `id, name, type, url_encrypted, key_id, max_timeout, created_at, updated_at`

// InsertSolver writes a solver row and returns its new id (empty url_encrypted;
// the service seals the URL back via SetSolverSecret in the same tx).
func (Solvers) InsertSolver(ctx context.Context, q dbinterface.Execer, s domain.Solver) (int64, error) {
	res, err := q.ExecContext(ctx,
		q.Rebind(`INSERT INTO solvers (name, type, url_encrypted, key_id, max_timeout, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`),
		s.Name, s.Type, s.URLEncrypted, s.KeyID, s.MaxTimeout,
		s.CreatedAt.UTC().Format(timeLayout), s.UpdatedAt.UTC().Format(timeLayout))
	if err != nil {
		return 0, fmt.Errorf("database: insert solver: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("database: solver last insert id: %w", err)
	}
	return id, nil
}

// GetSolver returns the solver with the given id, or ErrNotFound.
func (Solvers) GetSolver(ctx context.Context, q dbinterface.Execer, id int64) (domain.Solver, error) {
	row := q.QueryRowContext(ctx, q.Rebind(`SELECT `+solverColumns+` FROM solvers WHERE id = ?`), id)
	s, err := scanSolver(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Solver{}, fmt.Errorf("solver %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return domain.Solver{}, fmt.Errorf("database: scan solver %d: %w", id, err)
	}
	return s, nil
}

// ListSolvers returns all solvers ordered by id.
func (Solvers) ListSolvers(ctx context.Context, q dbinterface.Execer) ([]domain.Solver, error) {
	rows, err := q.QueryContext(ctx, q.Rebind(`SELECT `+solverColumns+` FROM solvers ORDER BY id`))
	if err != nil {
		return nil, fmt.Errorf("database: list solvers: %w", err)
	}
	defer rows.Close()

	var out []domain.Solver
	for rows.Next() {
		s, err := scanSolver(rows)
		if err != nil {
			return nil, fmt.Errorf("database: scan solver row: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("database: iterate solvers: %w", err)
	}
	return out, nil
}

// UpdateSolver writes a solver's mutable fields (name, type, re-encrypted URL,
// key_id, max_timeout) by id, returning ErrNotFound when no row matches.
func (Solvers) UpdateSolver(ctx context.Context, q dbinterface.Execer, s domain.Solver) error {
	res, err := q.ExecContext(ctx,
		q.Rebind(`UPDATE solvers SET name = ?, type = ?, url_encrypted = ?, key_id = ?, max_timeout = ?, updated_at = ?
			WHERE id = ?`),
		s.Name, s.Type, s.URLEncrypted, s.KeyID, s.MaxTimeout, s.UpdatedAt.UTC().Format(timeLayout), s.ID)
	if err != nil {
		return fmt.Errorf("database: update solver: %w", err)
	}
	return affectedOrNotFoundID(res, s.ID)
}

// SetSolverSecret writes the encrypted URL column + key_id by id (phase two of the
// insert-then-seal write).
func (Solvers) SetSolverSecret(ctx context.Context, q dbinterface.Execer, id int64, urlEncrypted, keyID string) error {
	res, err := q.ExecContext(ctx,
		q.Rebind(`UPDATE solvers SET url_encrypted = ?, key_id = ? WHERE id = ?`),
		urlEncrypted, keyID, id)
	if err != nil {
		return fmt.Errorf("database: set solver secret: %w", err)
	}
	return affectedOrNotFoundID(res, id)
}

// DeleteSolver removes a solver by id, returning ErrNotFound when absent.
// Referencing instances' solver_id is nulled by the ON DELETE SET NULL FK.
func (Solvers) DeleteSolver(ctx context.Context, q dbinterface.Execer, id int64) error {
	res, err := q.ExecContext(ctx, q.Rebind(`DELETE FROM solvers WHERE id = ?`), id)
	if err != nil {
		return fmt.Errorf("database: delete solver: %w", err)
	}
	return affectedOrNotFoundID(res, id)
}

// scanSolver reads one solvers row from a *sql.Row or *sql.Rows.
func scanSolver(sc interface{ Scan(...any) error }) (domain.Solver, error) {
	var (
		s                    domain.Solver
		createdAt, updatedAt string
	)
	if err := sc.Scan(&s.ID, &s.Name, &s.Type, &s.URLEncrypted, &s.KeyID, &s.MaxTimeout, &createdAt, &updatedAt); err != nil {
		return domain.Solver{}, err //nolint:wrapcheck // sql.ErrNoRows matched by caller; others wrapped there.
	}
	s.CreatedAt, s.UpdatedAt = parseTime(createdAt), parseTime(updatedAt)
	return s, nil
}
