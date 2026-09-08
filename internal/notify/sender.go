package notify

import (
	"context"
	"net/http"

	apphttp "github.com/autobrr/harbrr/internal/http"
)

// poster carries the shared HTTP machinery both senders reuse: a JSON POST to the
// (secret) destination URL that never echoes the request URL — it can carry a bearer
// token — nor the response body (which can reproduce the request) into an error.
type poster struct {
	jc *apphttp.JSONClient
	// url is the SECRET destination; never logged or echoed.
	url string
}

// newPoster builds the transport for one sender. kind labels it in every error
// ("notify: discord: ..."), never the destination.
//
// The destination rides as the request PATH over an EMPTY Base rather than as the Base
// itself, deliberately: NewJSONClient trims a trailing "/" off Base, which would
// silently rewrite a user's webhook path, and Secret is the same string, so the client's
// value-scrub removes the URL byte-for-byte from every error it emits.
func newPoster(kind, url string, client *http.Client) poster {
	return poster{
		jc: apphttp.NewJSONClient(apphttp.JSONClient{
			Prefix: "notify: " + kind,
			Client: client,
			Secret: url,
		}),
		url: url,
	}
}

// post marshals body to JSON and POSTs it to the destination URL, returning a scrubbed
// error for any transport failure or non-2xx status. The response body is discarded and
// never surfaced (Reason is nil — status only).
func (p poster) post(ctx context.Context, body any) error {
	_, err := p.jc.Do(ctx, http.MethodPost, p.url, body, nil)
	return err
}
