// Package xspeeds implements XSpeeds' managed form login and HTML browse dialect.
package xspeeds

import (
	"context"
	"errors"
	stdhttp "net/http"
	"net/url"
	"strings"
	"sync"

	"golang.org/x/sync/semaphore"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

type driver struct {
	native.Base

	persist   func(context.Context, string, string) error
	jar       stdhttp.CookieJar
	cookieURL *url.URL
	gate      *semaphore.Weighted

	stateMu   sync.RWMutex
	session   sessionState
	lastLogin loginResult
}

type sessionState struct {
	cookie     string
	generation uint64
}

type loginResult struct {
	number           uint64
	failedGeneration uint64
	err              error
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
	jar := cookieJar(p.Doer)
	if jar == nil {
		return nil, errors.New("xspeeds: HTTP doer must expose a non-nil cookie jar")
	}
	cookieURL, err := url.Parse(base.BaseURL)
	if err != nil || cookieURL.Scheme == "" || cookieURL.Host == "" {
		return nil, errors.New("xspeeds: invalid base URL")
	}

	stored := strings.TrimSpace(p.Cfg["cookie"])
	var session sessionState
	if stored != "" {
		jar.SetCookies(cookieURL, parseCookieHeader(stored))
		if seeded := serializeCookies(jar.Cookies(cookieURL)); seeded != "" {
			session = sessionState{cookie: seeded, generation: 1}
		}
	}

	return &driver{
		Base:      base,
		persist:   p.PersistSetting,
		jar:       jar,
		cookieURL: cookieURL,
		gate:      semaphore.NewWeighted(1),
		session:   session,
	}, nil
}

func cookieJar(doer search.Doer) stdhttp.CookieJar {
	if client, ok := doer.(*stdhttp.Client); ok {
		return client.Jar
	}
	if owner, ok := doer.(search.JarOwner); ok {
		return owner.CookieJar()
	}
	return nil
}

// NeedsResolver is false because published release URLs contain no credentials.
func (*driver) NeedsResolver() bool { return false }

// DownloadNeedsAuth is true because Grab must attach the server-side session cookie.
func (*driver) DownloadNeedsAuth() bool { return true }

// Test verifies credentials through an empty authenticated browse.
func (d *driver) Test(ctx context.Context) error {
	return native.TestViaSearch(ctx, d)
}

func (d *driver) snapshot() (sessionState, loginResult) {
	d.stateMu.RLock()
	defer d.stateMu.RUnlock()
	return d.session, d.lastLogin
}

func (d *driver) sessionSnapshot() sessionState {
	session, _ := d.snapshot()
	return session
}
