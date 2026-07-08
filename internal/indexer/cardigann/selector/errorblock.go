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
// selector block when present and it matches, else the matched element's text —
// reproducing Jackett's `errorMessage = selection.TextContent; if (error.Message
// != null) errorMessage = handleSelector(error.Message, root)`. A non-matching
// block is skipped; a selector evaluation error propagates; no match across all
// blocks returns ("", false, nil).
//
// The returned message is trimmed and single-lined. It is definition-authored
// error text (e.g. "Database error."), never a credential.
func (e *Engine) CheckErrorBlocks(root Row, blocks []loader.ErrorBlock) (message string, matched bool, err error) {
	for i := range blocks {
		msg, ok, blkErr := e.evalErrorBlock(root, blocks[i])
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
func (e *Engine) evalErrorBlock(root Row, blk loader.ErrorBlock) (msg string, matched bool, err error) {
	probe := loader.SelectorBlock{Selector: blk.Selector}
	val, found, err := e.Field(root, probe)
	if err != nil {
		return "", false, fmt.Errorf("evaluating error selector %q: %w", blk.Selector, err)
	}
	if !found {
		return "", false, nil
	}
	if blk.Message != nil {
		mval, mfound, merr := e.Field(root, *blk.Message)
		if merr != nil {
			return "", false, fmt.Errorf("evaluating error message selector %q: %w", blk.Message.Selector, merr)
		}
		if mfound {
			return trimErrorMessage(mval), true, nil
		}
	}
	return trimErrorMessage(val), true, nil
}

// trimErrorMessage trims and single-lines an extracted error message before it is
// wrapped into a loud error. The message is definition-authored error text (e.g.
// "Invalid username or password"), not a credential, but we keep it compact and
// free of stray whitespace for clean logs and error strings.
func trimErrorMessage(s string) string {
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}
