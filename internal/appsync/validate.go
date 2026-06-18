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
	return p
}

// validateCreate checks the required fields and enumerated values of a create request.
func validateCreate(p CreateConnectionParams) error {
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("%w: name is required", ErrInvalid)
	}
	if err := validateKind(p.Kind); err != nil {
		return err
	}
	if strings.TrimSpace(p.BaseURL) == "" {
		return fmt.Errorf("%w: base url is required", ErrInvalid)
	}
	if strings.TrimSpace(p.APIKey) == "" {
		return fmt.Errorf("%w: api key is required", ErrInvalid)
	}
	if strings.TrimSpace(p.HarbrrURL) == "" {
		return fmt.Errorf("%w: harbrr url is required", ErrInvalid)
	}
	if err := validateSyncLevel(p.SyncLevel); err != nil {
		return err
	}
	return validateIndexScope(p.IndexScope)
}

// applyUpdate mutates conn from the non-nil patch fields, validating any enums it sets.
func applyUpdate(conn *domain.AppConnection, p UpdateConnectionParams) error {
	if p.Name != nil {
		conn.Name = *p.Name
	}
	if p.BaseURL != nil {
		conn.BaseURL = *p.BaseURL
	}
	if p.HarbrrURL != nil {
		conn.HarbrrURL = *p.HarbrrURL
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
	return nil
}

func validateKind(kind string) error {
	switch kind {
	case domain.AppKindSonarr, domain.AppKindRadarr, domain.AppKindQui:
		return nil
	default:
		return fmt.Errorf("%w: kind must be sonarr, radarr, or qui (got %q)", ErrInvalid, kind)
	}
}

func validateSyncLevel(level string) error {
	switch level {
	case domain.SyncLevelFull, domain.SyncLevelAddUpdate:
		return nil
	default:
		return fmt.Errorf("%w: sync_level must be full or add_update (got %q)", ErrInvalid, level)
	}
}

func validateIndexScope(scope string) error {
	switch scope {
	case domain.IndexScopeAll, domain.IndexScopeSelected:
		return nil
	default:
		return fmt.Errorf("%w: index_scope must be all or selected (got %q)", ErrInvalid, scope)
	}
}
