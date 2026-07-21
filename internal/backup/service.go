package backup

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/apps"
	"github.com/autobrr/harbrr/internal/database/dbinterface"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/secrets"
	"github.com/autobrr/harbrr/internal/version"
)

// ErrInvalid and ErrConflict are the service's input-mapping sentinels (the handler turns
// them into 400 / 409). A wrong passphrase, a malformed/foreign bundle, and an
// unsupported version are all ErrInvalid (a 400 the operator can act on); a restore into
// a non-empty instance without force is ErrConflict. Both wrap the matching domain
// sentinel so the api layer's writeServiceError only needs to check errors.Is against
// domain.ErrInvalid/domain.ErrConflict.
var (
	ErrInvalid = fmt.Errorf("backup: %w", domain.ErrInvalid)
	// ErrConflict keeps its historical "target instance is not empty" text (the
	// restore-target message is load-bearing UX) via %.0w: fmt treats %w like %v but
	// precision .0 emits zero characters, so nothing is appended to the message while
	// the error still wraps domain.ErrConflict for errors.Is.
	ErrConflict = fmt.Errorf("backup: target instance is not empty%.0w", domain.ErrConflict)
)

// Service exports harbrr's config + database to a passphrase-encrypted bundle and
// restores one. It reads/writes secrets through the at-rest keyring (decrypt on export,
// re-seal on import) but the bundle itself is sealed under a separate passphrase key, so
// it is portable across hosts and at-rest keys. appsSvc resolves/decrypts App identity —
// every app-sync/announce connection's own identity+credential lives on its App, not on
// the connection row (ADR 0004, #269).
type Service struct {
	db      dbinterface.Querier
	apps    *apps.Service
	keyring *secrets.Keyring
	clock   func() time.Time
	log     zerolog.Logger
}

// NewService wires the backup service.
func NewService(db dbinterface.Querier, keyring *secrets.Keyring, appsSvc *apps.Service, log zerolog.Logger) *Service {
	return &Service{db: db, apps: appsSvc, keyring: keyring, clock: time.Now, log: log}
}

// ExportParams is the input to Export. Passphrase is required — every export is sealed.
type ExportParams struct {
	Passphrase string
}

// Export collects every backed-up table (decrypting each secret with the current
// keyring), seals the JSON payload under a fresh passphrase-derived key, and returns the
// full envelope bytes. The plaintext secrets exist only transiently in memory between
// decrypt and re-seal.
func (s *Service) Export(ctx context.Context, p ExportParams) ([]byte, error) {
	if strings.TrimSpace(p.Passphrase) == "" {
		return nil, fmt.Errorf("%w: passphrase is required", ErrInvalid)
	}
	tables, err := s.collect(ctx, s.db)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(tables)
	if err != nil {
		return nil, fmt.Errorf("backup: marshal payload: %w", err)
	}
	salt, err := secrets.NewPassphraseSalt()
	if err != nil {
		return nil, fmt.Errorf("backup: %w", err)
	}
	kdf := secrets.DefaultPassphraseKDF()
	key, err := secrets.DeriveKeyFromPassphrase(p.Passphrase, salt, kdf)
	if err != nil {
		return nil, fmt.Errorf("backup: derive key: %w", err)
	}
	sealed, err := secrets.EncryptWithKey(key, []byte(payloadAAD), payload)
	if err != nil {
		return nil, fmt.Errorf("backup: seal payload: %w", err)
	}
	env := Envelope{
		SchemaVersion: SchemaVersion,
		HarbrrVersion: version.Version,
		CreatedAt:     s.clock().UTC().Format(time.RFC3339),
		KDF:           kdf,
		Salt:          base64.StdEncoding.EncodeToString(salt),
		Payload:       sealed,
	}
	out, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("backup: marshal envelope: %w", err)
	}
	return out, nil
}

// ImportParams is the input to Import. Force is required to overwrite a non-empty
// instance (a restore is destructive: it wipes the backed-up tables first).
type ImportParams struct {
	Payload    []byte
	Passphrase string
	Force      bool
}

// Import opens the bundle with the passphrase and restores it (transactional
// wipe-and-load). A wrong passphrase is a clean ErrInvalid, never a partial restore.
func (s *Service) Import(ctx context.Context, p ImportParams) error {
	if strings.TrimSpace(p.Passphrase) == "" {
		return fmt.Errorf("%w: passphrase is required", ErrInvalid)
	}
	tables, err := s.decode(p.Payload, p.Passphrase)
	if err != nil {
		return err
	}
	return s.restore(ctx, tables, p.Force)
}

// decode parses the envelope, verifies the format/KDF, derives the key, and opens the
// sealed payload. Every failure here is a client-actionable ErrInvalid (wrong passphrase,
// wrong version, not a bundle) — none leak key material.
func (s *Service) decode(payload []byte, passphrase string) (*Tables, error) {
	var env Envelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return nil, fmt.Errorf("%w: not a harbrr backup bundle", ErrInvalid)
	}
	if env.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("%w: unsupported bundle version %d (this harbrr reads version %d)",
			ErrInvalid, env.SchemaVersion, SchemaVersion)
	}
	salt, err := base64.StdEncoding.DecodeString(env.Salt)
	if err != nil {
		return nil, fmt.Errorf("%w: malformed salt", ErrInvalid)
	}
	// Derive with the KDF params RECORDED in the bundle (not the current default), so a
	// bundle stays decryptable after harbrr's default cost is raised.
	key, err := secrets.DeriveKeyFromPassphrase(passphrase, salt, env.KDF)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	raw, err := secrets.DecryptWithKey(key, []byte(payloadAAD), env.Payload)
	if err != nil {
		return nil, fmt.Errorf("%w: wrong passphrase or corrupt bundle", ErrInvalid)
	}
	var tables Tables
	if err := json.Unmarshal(raw, &tables); err != nil {
		return nil, fmt.Errorf("backup: decode payload: %w", err)
	}
	return &tables, nil
}
