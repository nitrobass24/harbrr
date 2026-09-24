// Package xspeeds implements XSpeeds' managed form login and HTML browse dialect.
package xspeeds

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/native"
)

type driver struct {
	native.Base
	*native.CookieSession

	persist func(context.Context, string, string) error
}

var _ native.Driver = (*driver)(nil)

// New builds one XSpeeds instance and seeds its private cookie jar from the hidden
// persisted cookie setting. A jar is required because login and tracker operations
// share server-managed session state.
func New(p native.Params) (native.Driver, error) {
	base, err := native.NewBase("xspeeds", p)
	if err != nil {
		return nil, err
	}
	jar := native.CookieJarOf(p.Doer)
	if jar == nil {
		return nil, errors.New("xspeeds: HTTP doer must expose a non-nil cookie jar")
	}
	cookieURL, err := url.Parse(base.BaseURL)
	if err != nil || cookieURL.Scheme == "" || cookieURL.Host == "" {
		return nil, errors.New("xspeeds: invalid base URL")
	}

	stored := strings.TrimSpace(p.Cfg["cookie"])
	var session native.SessionState
	if stored != "" {
		jar.SetCookies(cookieURL, native.ParseCookieHeader(stored))
		if seeded := native.SerializeCookies(jar.Cookies(cookieURL)); seeded != "" {
			session = native.SessionState{Cookie: seeded, Generation: 1}
		}
	}

	cookies := native.NewCookieSession("xspeeds", jar, cookieURL, session)
	// XSpeeds clears a jar entry with a Path-less deletion as well as a "/"-rooted one,
	// reaching cookies the tracker scoped to a sub-path.
	cookies.PathlessJarDeletions = true
	return &driver{
		Base:          base,
		CookieSession: cookies,
		persist:       p.PersistSetting,
	}, nil
}

// NeedsResolver is false because published release URLs contain no credentials.
func (*driver) NeedsResolver() bool { return false }

// DownloadNeedsAuth is true because Grab must attach the server-side session cookie.
func (*driver) DownloadNeedsAuth() bool { return true }

// Test verifies credentials through an empty authenticated browse.
func (d *driver) Test(ctx context.Context) error {
	return native.TestViaSearch(ctx, d)
}
