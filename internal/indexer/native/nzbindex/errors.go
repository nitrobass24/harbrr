package nzbindex

import (
	"fmt"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// normalizeReadError keeps the pre-Base health sentinel for mid-body API read
// failures while leaving transport/status errors in Base's native form.
func normalizeReadError(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "read request response") {
		return fmt.Errorf("%w: %w", err, search.ErrParseError)
	}
	return err
}
