package download

import (
	"os"
	"strings"
	"testing"
)

// assertGPLHeader fails t unless relPath (relative to this package's directory)
// starts with the exact GPL attribution header the #241 porting manifest
// requires every ported autobrr file to carry verbatim.
func assertGPLHeader(t *testing.T, relPath string) {
	t.Helper()
	const header = "// Copyright (c) 2021 - 2025, Ludvig Lundgren and the autobrr contributors.\n" +
		"// SPDX-License-Identifier: GPL-2.0-or-later\n"
	data, err := os.ReadFile(relPath) //nolint:gosec // G304: relPath is a fixed test-internal constant, not user input.
	if err != nil {
		t.Fatalf("read %s: %v", relPath, err)
	}
	if !strings.HasPrefix(string(data), header) {
		t.Fatalf("%s is missing the required GPL attribution header", relPath)
	}
}
