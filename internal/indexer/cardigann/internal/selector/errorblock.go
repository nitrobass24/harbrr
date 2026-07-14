package selector

import (
	"fmt"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// CheckErrorBlocks evaluates a definition's error selectors against a parsed
// document root, reproducing the per-block loop of Jackett's checkForError. It is
// shared by the login stage (checkForError on the login response) and the search
// stage (checkForError on the search response) so both extract the error message
// identically. The HTTP-401 short-circuit Jackett's checkForError does first is
// caller-specific (login treats it as ErrLoginFailed; search already fails fast on
// non-2xx upstream), so it stays with each caller rather than in this loop.
//
// For each block IN DEFINITION ORDER: the block's Selector is tested against root.
// The first match yields (message, true, nil); the message comes from the Message
// selector block when present, else the matched element's text — reproducing
// Jackett's `errorMessage = selection.TextContent; if (error.Message != null)
// errorMessage = handleSelector(error.Message, root)`. Jackett's handleSelector is
// called there with its default required=true, so when Message is set but its
// selector does NOT match, it throws — there is no fallback to the outer match;
// this propagates that as an error too. A non-matching block is skipped; a
// selector evaluation error propagates; no match across all blocks returns
// ("", false, nil).
//
// The returned message is trimmed and single-lined. The SELECTOR is definition-authored,
// but the extracted TEXT is lifted from the server's RESPONSE body (server-controlled), so
// it can echo a submitted credential. Any caller that SURFACES the message MUST value-scrub
// its own secrets out of it first: both do — the login stage in checkErrors and the search
// stage in checkSearchError, each via login.ScrubSecrets over the IsSecret-derived values.
// eval is the template-eval seam for this call, threaded through to every
// Field lookup below; nil defaults to identity.
func (e *Engine) CheckErrorBlocks(root Row, blocks []loader.ErrorBlock, eval EvalFunc) (message string, matched bool, err error) {
	for i := range blocks {
		msg, ok, blkErr := e.evalErrorBlock(root, blocks[i], eval)
		if blkErr != nil {
			return "", false, blkErr
		}
		if ok {
			return msg, true, nil
		}
	}
	return "", false, nil
}

// evalErrorBlock tests one error block's selector against root. When it matches,
// it extracts the error message: from the block's Message selector block when
// present, else the matched element's text. The returned message is
// trimmed/single-lined.
func (e *Engine) evalErrorBlock(root Row, blk loader.ErrorBlock, eval EvalFunc) (msg string, matched bool, err error) {
	probe := loader.SelectorBlock{Selector: blk.Selector}
	val, found, err := e.Field(root, probe, eval)
	if err != nil {
		return "", false, fmt.Errorf("evaluating error selector %q: %w", blk.Selector, err)
	}
	if !found {
		return "", false, nil
	}
	if blk.Message != nil {
		mval, mfound, merr := e.Field(root, *blk.Message, eval)
		if merr != nil {
			return "", false, fmt.Errorf("evaluating error message selector %q: %w", blk.Message.Selector, merr)
		}
		if !mfound {
			// Jackett's handleSelector(error.Message, ...) runs with its default
			// required=true, so a non-matching Message selector THROWS — there is
			// no fallback to the outer error-block match. Propagate a loud error
			// here too, rather than silently substituting val.
			return "", false, fmt.Errorf("error message selector %q didn't match", blk.Message.Selector)
		}
		return trimErrorMessage(mval), true, nil
	}
	return trimErrorMessage(val), true, nil
}

// trimErrorMessage trims and single-lines an extracted error message before it is
// wrapped into a loud error. The message is server-controlled text lifted from the
// response body (it can echo a submitted credential — callers value-scrub their own
// secrets); this just keeps it compact and free of stray whitespace for clean logs and
// error strings.
func trimErrorMessage(s string) string {
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}
