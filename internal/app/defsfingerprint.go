package app

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"

	"github.com/autobrr/harbrr/internal/indexer/definitions"
)

// defsFingerprint hashes the definition content that shapes cached search
// results: the embedded Jackett vendor snapshot plus the on-disk dropin overrides
// at dropinDir (a missing or empty dropin dir contributes nothing). It is used at
// boot (buildSearchCache -> SearchCache.EnsureDefsFingerprint) to detect a
// def-content change across a restart — a vendor refresh shipped in a binary
// upgrade, or a dropin add/edit/remove — so already-cached rows shaped by the old
// definitions can be expired. It deliberately does NOT cover native-driver code
// changes (out of scope for autobrr/harbrr#347).
//
// The walk is lexical (fs.WalkDir's own guarantee) and every file's slash-
// separated relative path is hashed alongside its content, so a rename changes the
// fingerprint even when no file's bytes did. Only content is hashed — never
// mtimes — so touching a file without changing it never triggers an expiry.
func defsFingerprint(dropinDir string) (string, error) {
	h := sha256.New()
	if err := hashFS(h, definitions.Vendored); err != nil {
		return "", fmt.Errorf("hash vendored definitions: %w", err)
	}
	switch _, err := os.Stat(dropinDir); {
	case err == nil:
		if err := hashFS(h, os.DirFS(dropinDir)); err != nil {
			return "", fmt.Errorf("hash dropin definitions: %w", err)
		}
	case !errors.Is(err, os.ErrNotExist):
		return "", fmt.Errorf("stat dropin dir: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// hashFS walks root lexically, writing each regular file's path and content into
// h (a NUL separator between the two avoids a path/content ambiguity, e.g. path
// "ab"+content "c" vs path "a"+content "bc"). Directories are not hashed
// themselves, so an empty tree contributes nothing.
func hashFS(h hash.Hash, root fs.FS) error {
	err := fs.WalkDir(root, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walk %q: %w", path, err)
		}
		if d.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(root, path)
		if err != nil {
			return fmt.Errorf("read %q: %w", path, err)
		}
		if _, err := io.WriteString(h, path); err != nil {
			return fmt.Errorf("hash %q: %w", path, err)
		}
		if _, err := h.Write([]byte{0}); err != nil {
			return fmt.Errorf("hash %q: %w", path, err)
		}
		if _, err := h.Write(data); err != nil {
			return fmt.Errorf("hash %q: %w", path, err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk fs: %w", err)
	}
	return nil
}
