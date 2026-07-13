package selector

import (
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

func rootFromHTML(t *testing.T, html string) Row {
	t.Helper()
	doc, err := New().ParseHTML([]byte(html))
	if err != nil {
		t.Fatalf("ParseHTML: %v", err)
	}
	return doc.Root()
}

// TestCheckErrorBlocksMessageSelector pins evalErrorBlock's Message handling
// against Jackett's checkForError: `errorMessage = handleSelector(error.Message,
// root)` runs with handleSelector's default required=true, so when the outer
// error selector matches but blk.Message's own selector does NOT, Jackett throws
// — there is no fallback to the outer match's text.
func TestCheckErrorBlocksMessageSelector(t *testing.T) {
	t.Parallel()

	const page = `<div class="error">Login failed<p class="detail">Bad credentials</p></div>`

	t.Run("message selector matches — uses the message text, not the outer match", func(t *testing.T) {
		t.Parallel()
		root := rootFromHTML(t, page)
		blocks := []loader.ErrorBlock{{
			Selector: "div.error",
			Message:  &loader.SelectorBlock{Selector: "p.detail"},
		}}
		msg, matched, err := New().CheckErrorBlocks(root, blocks)
		if err != nil {
			t.Fatalf("CheckErrorBlocks: %v", err)
		}
		if !matched || msg != "Bad credentials" {
			t.Fatalf("matched=%v msg=%q, want matched=true msg=%q", matched, msg, "Bad credentials")
		}
	})

	t.Run("message selector does not match — propagates an error, no fallback", func(t *testing.T) {
		t.Parallel()
		root := rootFromHTML(t, page)
		blocks := []loader.ErrorBlock{{
			Selector: "div.error",
			Message:  &loader.SelectorBlock{Selector: "p.nonexistent"},
		}}
		msg, matched, err := New().CheckErrorBlocks(root, blocks)
		if err == nil {
			t.Fatalf("CheckErrorBlocks: want an error when the message selector doesn't match, got msg=%q matched=%v", msg, matched)
		}
		if !strings.Contains(err.Error(), "p.nonexistent") {
			t.Errorf("error = %v, want it to name the non-matching message selector", err)
		}
		if matched {
			t.Errorf("matched = true on an error return, want false")
		}
	})

	t.Run("no message block — falls back to the outer match's text", func(t *testing.T) {
		t.Parallel()
		root := rootFromHTML(t, page)
		blocks := []loader.ErrorBlock{{Selector: "div.error"}}
		msg, matched, err := New().CheckErrorBlocks(root, blocks)
		if err != nil {
			t.Fatalf("CheckErrorBlocks: %v", err)
		}
		if !matched || !strings.HasPrefix(msg, "Login failed") {
			t.Fatalf("matched=%v msg=%q, want matched=true msg starting with %q", matched, msg, "Login failed")
		}
	})

	t.Run("no block selector matches — no error, matched false", func(t *testing.T) {
		t.Parallel()
		root := rootFromHTML(t, page)
		blocks := []loader.ErrorBlock{{Selector: "div.nonexistent"}}
		msg, matched, err := New().CheckErrorBlocks(root, blocks)
		if err != nil || matched || msg != "" {
			t.Fatalf("CheckErrorBlocks = (%q, %v, %v), want (\"\", false, nil)", msg, matched, err)
		}
	})
}
