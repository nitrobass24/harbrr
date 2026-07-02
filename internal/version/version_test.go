package version

import (
	"strings"
	"testing"
)

func TestString(t *testing.T) {
	t.Parallel()
	if got := String(); !strings.Contains(got, Version) {
		t.Fatalf("String() = %q, want it to contain %q", got, Version)
	}
}
