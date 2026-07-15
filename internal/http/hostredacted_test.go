package http

import (
	"errors"
	"fmt"
	"testing"
)

// TestMarkHostRedacted_RoundTrip proves the marker is detectable, transparent to
// Error(), and never claims an unmarked error is marked.
func TestMarkHostRedacted_RoundTrip(t *testing.T) {
	t.Parallel()
	inner := errors.New("Get https://t.invalid: connection refused")

	marked := MarkHostRedacted(inner)
	if !IsHostRedacted(marked) {
		t.Fatal("IsHostRedacted(marked) = false, want true")
	}
	if marked.Error() != inner.Error() {
		t.Fatalf("marked.Error() = %q, want unchanged %q", marked.Error(), inner.Error())
	}
	if IsHostRedacted(inner) {
		t.Fatal("IsHostRedacted(unmarked) = true, want false")
	}
	if IsHostRedacted(nil) {
		t.Fatal("IsHostRedacted(nil) = true, want false")
	}
	if MarkHostRedacted(nil) != nil {
		t.Fatal("MarkHostRedacted(nil) != nil")
	}
}

// TestMarkHostRedacted_SurvivesFurtherWrapping proves a later %w wrap (as the paced
// client's "registry: %w" and any caller's own context) does not hide the marker
// from IsHostRedacted, and does not break errors.Is/As reaching a sentinel below it.
func TestMarkHostRedacted_SurvivesFurtherWrapping(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("rate limited")
	marked := MarkHostRedacted(fmt.Errorf("Get https://t.invalid: %w", sentinel))
	outer := fmt.Errorf("registry: %w", marked)

	if !IsHostRedacted(outer) {
		t.Fatal("IsHostRedacted(outer) = false, want true after further %w wrapping")
	}
	if !errors.Is(outer, sentinel) {
		t.Fatal("errors.Is(outer, sentinel) = false, want true — marker must not break the chain")
	}
}
