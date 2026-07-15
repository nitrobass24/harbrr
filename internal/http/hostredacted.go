package http

import "errors"

// hostRedactedError marks err as already carrying a host-only-redacted transport
// prefix (op + scheme://host, as built by SafeTransportDetail/redactDoErr-shaped
// callers), so a caller further up the chain that would otherwise prepend its own
// method+SchemeHost prefix can detect that and skip it. It embeds err rather than
// wrapping its text, so Error() and Unwrap() are exact passthroughs — marking never
// changes what is logged or what errors.Is/As can see below it.
type hostRedactedError struct {
	err error
}

func (e *hostRedactedError) Error() string { return e.err.Error() }
func (e *hostRedactedError) Unwrap() error { return e.err }

// MarkHostRedacted wraps err to record that its message already carries a
// host-only-redacted transport prefix, so a caller composing further context (e.g.
// the cardigann search layer re-wrapping a paced-client transport failure) can call
// IsHostRedacted to avoid re-prepending its own scheme+host and double-printing the
// host (autobrr/harbrr#181). A nil err returns nil. The marker is transparent to
// errors.Is/As: it changes neither the error's message nor its unwrap chain, so
// sentinels below it (context.Canceled, rate-limit types, ...) remain reachable.
func MarkHostRedacted(err error) error {
	if err == nil {
		return nil
	}
	return &hostRedactedError{err: err}
}

// IsHostRedacted reports whether err's chain carries a MarkHostRedacted marker.
func IsHostRedacted(err error) bool {
	var marked *hostRedactedError
	return errors.As(err, &marked)
}
