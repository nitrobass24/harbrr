package appsync

import (
	"fmt"
	"strings"

	"github.com/autobrr/harbrr/internal/domain"
)

// withDefaults fills the optional fields of a create request.
func (p CreateConnectionParams) withDefaults() CreateConnectionParams {
	if p.SyncLevel == "" {
		p.SyncLevel = domain.SyncLevelFull
	}
	if p.IndexScope == "" {
		p.IndexScope = domain.IndexScopeAll
	}
	if p.Priority == 0 {
		p.Priority = defaultPriority
	}
	if p.FreeleechMode == "" {
		p.FreeleechMode = defaultFreeleechMode(p.Kind)
	}
	return p
}

// defaultFreeleechMode picks a connection's freeleech routing by app kind: qui (which
// drives cross-seed off a single shared Torznab pool) gets the full catalog by default;
// every *arr honors the indexer's freeleech setting. The operator can override either.
func defaultFreeleechMode(kind string) string {
	if kind == domain.AppKindQui {
		return domain.FreeleechModeBypass
	}
	return domain.FreeleechModeHonor
}

// validateCreate checks the required fields and enumerated values of a create request.
// It normalizes p.BaseURL/p.HarbrrURL to their trimmed form in place (via the pointer)
// so the caller persists the normalized URLs.
func validateCreate(p *CreateConnectionParams) error {
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("%w: name is required", domain.ErrInvalid)
	}
	if err := validateKind(p.Kind); err != nil {
		return err
	}
	// Both URLs must be absolute http(s): harbrr calls BaseURL, and HarbrrURL is
	// embedded in each pushed indexer so the app can reach harbrr's feed — a
	// malformed/relative value would silently produce an unreachable connection.
	base, err := domain.ValidateAbsURL("base url", p.BaseURL)
	if err != nil {
		return err
	}
	p.BaseURL = base
	if strings.TrimSpace(p.APIKey) == "" {
		return fmt.Errorf("%w: api key is required", domain.ErrInvalid)
	}
	harbrr, err := domain.ValidateAbsURL("harbrr url", p.HarbrrURL)
	if err != nil {
		return err
	}
	p.HarbrrURL = harbrr
	if err := validateSyncLevel(p.SyncLevel); err != nil {
		return err
	}
	if err := validateIndexScope(p.IndexScope); err != nil {
		return err
	}
	return validateFreeleechMode(p.FreeleechMode)
}

// applyUpdate mutates conn from the non-nil patch fields, validating any enums it sets
// and rejecting a blank value for a required field (a present-but-empty patch must not
// silently store invalid connection state that create-time validation would reject).
func applyUpdate(conn *domain.AppConnection, p UpdateConnectionParams) error {
	if p.Name != nil {
		if err := requireNonBlank("name", *p.Name); err != nil {
			return err
		}
		conn.Name = *p.Name
	}
	if p.BaseURL != nil {
		base, err := domain.ValidateAbsURL("base url", *p.BaseURL)
		if err != nil {
			return err
		}
		conn.BaseURL = base
	}
	if p.HarbrrURL != nil {
		harbrr, err := domain.ValidateAbsURL("harbrr url", *p.HarbrrURL)
		if err != nil {
			return err
		}
		conn.HarbrrURL = harbrr
	}
	if p.Priority != nil {
		conn.Priority = *p.Priority
	}
	if p.SyncLevel != nil {
		if err := validateSyncLevel(*p.SyncLevel); err != nil {
			return err
		}
		conn.SyncLevel = *p.SyncLevel
	}
	if p.IndexScope != nil {
		if err := validateIndexScope(*p.IndexScope); err != nil {
			return err
		}
		conn.IndexScope = *p.IndexScope
	}
	if p.FreeleechMode != nil {
		if err := validateFreeleechMode(*p.FreeleechMode); err != nil {
			return err
		}
		conn.FreeleechMode = *p.FreeleechMode
	}
	// The ref was already validated (validateProfileRef) before applyUpdate; here it is
	// applied verbatim (a present nil clears the reference).
	if p.SyncProfileID.Present {
		conn.SyncProfileID = p.SyncProfileID.Value
	}
	return nil
}

// requireNonBlank rejects an empty/whitespace value for a required field.
func requireNonBlank(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: %s must not be blank", domain.ErrInvalid, field)
	}
	return nil
}

func validateKind(kind string) error {
	switch kind {
	case domain.AppKindSonarr, domain.AppKindRadarr, domain.AppKindLidarr,
		domain.AppKindReadarr, domain.AppKindWhisparr, domain.AppKindQui:
		return nil
	default:
		return fmt.Errorf("%w: kind must be sonarr, radarr, lidarr, readarr, whisparr, or qui (got %q)", domain.ErrInvalid, kind)
	}
}

func validateSyncLevel(level string) error {
	switch level {
	case domain.SyncLevelFull, domain.SyncLevelAddUpdate:
		return nil
	default:
		return fmt.Errorf("%w: sync_level must be full or add_update (got %q)", domain.ErrInvalid, level)
	}
}

func validateIndexScope(scope string) error {
	switch scope {
	case domain.IndexScopeAll, domain.IndexScopeSelected:
		return nil
	default:
		return fmt.Errorf("%w: index_scope must be all or selected (got %q)", domain.ErrInvalid, scope)
	}
}

func validateFreeleechMode(mode string) error {
	switch mode {
	case domain.FreeleechModeHonor, domain.FreeleechModeBypass:
		return nil
	default:
		return fmt.Errorf("%w: freeleech_mode must be honor or bypass (got %q)", domain.ErrInvalid, mode)
	}
}
