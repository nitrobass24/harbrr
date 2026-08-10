package app

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/definitions"
)

// vendorDefsDir is the subdirectory of the embedded definitions FS that holds the
// vendored Jackett snapshot.
const vendorDefsDir = "vendor"

// defsFingerprints hashes the definition content that shapes cached search
// results — the embedded Jackett vendor snapshot plus the on-disk dropin
// overrides at dropinDir — into ONE HASH PER DEFINITION, keyed by the
// definition's CONTENT id: (loader.ProbeID). That is the id an indexer instance
// stores in definition_id and the catalog offers, and for a handful of Jackett
// files it differs from the filename (darkpeers.yml carries darkpeers-api), so
// keying by filename would leave those indexers unmatched — and serving
// stale-shape rows — after a change. Dropin precedence is applied, so only the
// EFFECTIVE definition of each id is hashed: a dropin file byte-identical to the
// vendored one it shadows is not a change.
//
// It is used at boot (buildSearchCache -> SearchCache.EnsureDefsFingerprints) to
// detect def-content changes across a restart — a vendor refresh shipped in a
// binary upgrade, or a dropin add/edit/remove — so already-cached rows shaped by
// a CHANGED definition can be expired, and only those (autobrr/harbrr#388; the
// corpus-wide hash this replaced expired every indexer's rows on any change). It
// deliberately does NOT cover native-driver code changes (out of scope for
// autobrr/harbrr#347).
//
// Only content is hashed — never mtimes — so touching a file without changing it
// never triggers an expiry. Changing a definition's id is still a change: the id
// is the map key, so the old id disappears and the new one appears.
func defsFingerprints(dropinDir string) (map[string]string, error) {
	out := map[string]string{}
	if err := hashDefs(out, definitions.Vendored, vendorDefsDir); err != nil {
		return nil, fmt.Errorf("hash vendored definitions: %w", err)
	}
	if dropinDir != "" {
		if err := hashDefs(out, os.DirFS(dropinDir), "."); err != nil {
			return nil, fmt.Errorf("hash dropin definitions: %w", err)
		}
	}
	return out, nil
}

// hashDefs writes the sha256 of every .yml file in dir into out, keyed by its
// content id: — falling back to the basename minus ".yml" when the document will
// not decode or declares no id:, so a broken definition still gets a stable key
// instead of vanishing from the map (and reading as a removal on every boot).
// Entries are OVERWRITTEN, which is how dropin precedence is applied (dropins are
// hashed after the vendored snapshot). A missing dir contributes nothing; non-.yml
// files (schema.json, .jackett-ref) are ignored — they are not definitions and
// shape no cached row.
func hashDefs(out map[string]string, fsys fs.FS, dir string) error {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read definitions dir %q: %w", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yml") {
			continue
		}
		data, err := fs.ReadFile(fsys, path.Join(dir, e.Name()))
		if err != nil {
			return fmt.Errorf("read definition %q: %w", e.Name(), err)
		}
		id := loader.ProbeID(data)
		if id == "" {
			id = strings.TrimSuffix(e.Name(), ".yml")
		}
		sum := sha256.Sum256(data)
		out[id] = hex.EncodeToString(sum[:])
	}
	return nil
}
