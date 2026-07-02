package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// poster carries the shared HTTP machinery both senders reuse: a JSON POST to the
// (secret) destination URL that never echoes the request URL — it can carry a bearer
// token — into an error.
type poster struct {
	// kind labels the sender in error messages (not the destination).
	kind string
	// url is the SECRET destination; never logged or echoed.
	url    string
	client *http.Client
}

// post marshals body to JSON and POSTs it to the destination URL, returning a scrubbed
// error for any transport failure or non-2xx status. The response body is discarded and
// never surfaced: it can reproduce the request, which carries the secret URL.
func (p *poster) post(ctx context.Context, body any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("notify: %s: marshal request: %w", p.kind, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("notify: %s: build request: %w", p.kind, scrubURLError(err))
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("notify: %s: send: %w", p.kind, scrubURLError(err))
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Body not echoed (it can reproduce the secret-bearing request); only the status.
		return fmt.Errorf("notify: %s: unexpected status %d", p.kind, resp.StatusCode)
	}
	return nil
}

// scrubURLError strips the request URL (which may carry a bearer token) from a
// *url.Error so a transport failure never leaks the destination.
func scrubURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s: %w", ue.Op, ue.Err)
	}
	return err
}
