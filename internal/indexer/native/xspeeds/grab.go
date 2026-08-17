package xspeeds

import (
	"context"
	"errors"
	"fmt"
	stdhttp "net/http"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// Grab fetches a same-origin torrent server-side with the current session and one renewal.
func (d *driver) Grab(ctx context.Context, link string) (*search.GrabResult, error) {
	return runOperation(ctx, d, "download", func(ctx context.Context, session sessionState) (*search.GrabResult, error) {
		resolved, err := resolveSameOriginURL(d.cookieURL, link)
		if err != nil {
			return nil, errors.New("xspeeds: invalid download URL")
		}
		request, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, resolved, nil)
		if err != nil {
			return nil, fmt.Errorf("xspeeds: build download request: %w", err)
		}
		response, err := d.DoDownload(noRedirects(ctx), request, classifySession, d.captureSecrets(session.cookie)...)
		if err != nil {
			return nil, err
		}
		if !isBencoded(response.Body) && d.isLoginPage(response.Body) {
			return nil, fmt.Errorf("xspeeds: download returned the login page: %w", login.ErrLoginFailed)
		}
		return &search.GrabResult{Body: response.Body, ContentType: response.Header.Get("Content-Type")}, nil
	})
}

func isBencoded(body []byte) bool {
	return len(body) > 0 && body[0] == 'd'
}
