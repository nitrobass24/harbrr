package native

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	stdhttp "net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/semaphore"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// SessionState is an immutable snapshot of a cookie session. Generation advances
// whenever an automatic login publishes a replacement cookie, letting a failed request
// suppress a duplicate renewal without confusing its own cookie with the current one.
type SessionState struct {
	Cookie     string
	Generation uint64
}

// LoginFunc performs one login. It is always called with the single-login gate held,
// and failedGeneration is the generation the caller found stale (a driver that does not
// track which login failed ignores it).
type LoginFunc func(ctx context.Context, failedGeneration uint64) error

// loginFailure is a login error remembered against the generation it failed on, so a
// driver that wants fail-fast behaviour does not re-run a login that just failed for
// every concurrent caller. A driver that never calls RememberLoginError/CompleteLogin
// with an error leaves this zero, and every check below is inert for it.
type loginFailure struct {
	generation uint64
	err        error
}

// CookieSession is the cookie-session half of a form-login driver (the Gazelle
// AlphaRatio regime, XSpeeds): the private cookie jar, the single-login gate, the
// current session, and the last remembered login failure. A driver embeds it and
// supplies only its own LoginFunc; the driver's login flow, its persistence, and its
// scrub set stay its own.
type CookieSession struct {
	// Jar is the per-instance cookie jar, nil when the doer owns none (a driver that
	// attaches the session as an explicit Cookie header instead).
	Jar stdhttp.CookieJar
	// CookieURL is the jar key and the same-origin reference: the driver's base URL.
	CookieURL *url.URL
	// PathlessJarDeletions also writes a Path-less deletion cookie for every jar entry
	// being cleared, reaching entries a "/"-rooted deletion alone does not. Set it at
	// construction; it must not change afterwards.
	PathlessJarDeletions bool
	// LoginGate serializes login and renewal to one at a time. It is exported so a
	// driver test can hold it to pin concurrent callers at the gate.
	LoginGate *semaphore.Weighted

	family  string
	mu      sync.RWMutex
	session SessionState
	failure loginFailure
}

// NewCookieSession builds the session state for one driver instance. family prefixes
// the gate-wait errors; seed is the session restored from the persisted cookie setting
// (zero when there is none).
func NewCookieSession(family string, jar stdhttp.CookieJar, cookieURL *url.URL, seed SessionState) *CookieSession {
	return &CookieSession{
		Jar:       jar,
		CookieURL: cookieURL,
		family:    family,
		LoginGate: semaphore.NewWeighted(1),
		session:   seed,
	}
}

// Snapshot returns the current session.
func (s *CookieSession) Snapshot() SessionState {
	session, _ := s.snapshot()
	return session
}

func (s *CookieSession) snapshot() (SessionState, loginFailure) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.session, s.failure
}

// live returns the current session, or the login failure remembered against its
// generation — the "this session is known bad" fail-fast read.
func (s *CookieSession) live() (SessionState, error) {
	session, failure := s.snapshot()
	if failure.err != nil && session.Generation == failure.generation {
		return SessionState{}, failure.err
	}
	return session, nil
}

// Publish replaces the current session and forgets any remembered login failure.
func (s *CookieSession) Publish(session SessionState) {
	s.CompleteLogin(0, session, nil)
}

// CompleteLogin publishes session and, when err is a login failure, remembers it
// against failedGeneration — under one lock, so a concurrent snapshot never sees the
// session without the failure that belongs to it.
func (s *CookieSession) CompleteLogin(failedGeneration uint64, session SessionState, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.session = session
	s.failure = loginFailure{}
	if errors.Is(err, login.ErrLoginFailed) {
		s.failure = loginFailure{generation: failedGeneration, err: err}
	}
}

// RememberLoginError records a login failure against failedGeneration, but only while
// that is still the current generation — a session published since then is the newer
// truth and must not be poisoned.
func (s *CookieSession) RememberLoginError(failedGeneration uint64, err error) {
	if !errors.Is(err, login.ErrLoginFailed) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session.Generation == failedGeneration {
		s.failure = loginFailure{generation: failedGeneration, err: err}
	}
}

// Ensure returns the current session, driving login once under the single-login gate
// when there is none. Waiting for another login observes ctx cancellation, and the
// session is rechecked after acquisition so concurrent callers share its result.
func (s *CookieSession) Ensure(ctx context.Context, login LoginFunc) (SessionState, error) {
	if session, err := s.live(); err != nil || session.Cookie != "" {
		return session, err
	}
	if err := s.LoginGate.Acquire(ctx, 1); err != nil {
		return SessionState{}, fmt.Errorf("%s: wait for automatic login: %w", s.family, err)
	}
	defer s.LoginGate.Release(1)
	session, err := s.live()
	if err != nil || session.Cookie != "" {
		return session, err
	}
	if err := login(ctx, session.Generation); err != nil {
		return SessionState{}, err
	}
	return s.Snapshot(), nil
}

// Renew replaces the session failedGeneration names, under the single-login gate.
// Waiting observes ctx cancellation; after acquisition a newer non-empty generation
// suppresses the duplicate login so the caller can simply retry with it.
func (s *CookieSession) Renew(ctx context.Context, failedGeneration uint64, login LoginFunc) error {
	if err := s.LoginGate.Acquire(ctx, 1); err != nil {
		return fmt.Errorf("%s: wait for automatic session renewal: %w", s.family, err)
	}
	defer s.LoginGate.Release(1)
	session, failure := s.snapshot()
	if session.Cookie != "" && session.Generation != failedGeneration {
		return nil
	}
	if failure.err != nil && session.Generation == failure.generation {
		return failure.err
	}
	return login(ctx, failedGeneration)
}

// JarCookieHeader serializes the jar's cookies for the session URL into a Cookie
// header value; "" when there is no jar.
func (s *CookieSession) JarCookieHeader() string {
	if s.Jar == nil {
		return ""
	}
	return SerializeCookies(s.Jar.Cookies(s.CookieURL))
}

// ReplaceJarCookies expires every cookie the jar holds for the session URL and then
// seeds it with raw (a serialized Cookie header), so a login always starts from a clean
// jar and a failed login can put the previous session back. A driver with no jar is a
// no-op.
func (s *CookieSession) ReplaceJarCookies(raw string) {
	if s.Jar == nil {
		return
	}
	current := s.Jar.Cookies(s.CookieURL)
	expired := make([]*stdhttp.Cookie, 0, len(current)*2)
	for _, cookie := range current {
		expired = append(expired, deletionCookie(cookie.Name, "/"))
		if s.PathlessJarDeletions {
			expired = append(expired, deletionCookie(cookie.Name, ""))
		}
	}
	if len(expired) > 0 {
		s.Jar.SetCookies(s.CookieURL, expired)
	}
	if cookies := ParseCookieHeader(raw); len(cookies) > 0 {
		s.Jar.SetCookies(s.CookieURL, cookies)
	}
}

func deletionCookie(name, path string) *stdhttp.Cookie {
	//nolint:gosec // G124: deletion cookies are written only into the private per-instance jar; response security attributes are irrelevant.
	return &stdhttp.Cookie{
		Name:    name,
		Value:   "",
		Path:    path,
		MaxAge:  -1,
		Expires: time.Unix(1, 0),
	}
}

// CookieJarOf returns the jar a doer owns, or nil: a plain *http.Client carries it
// directly, and the paced client exposes it through search.JarOwner.
func CookieJarOf(doer search.Doer) stdhttp.CookieJar {
	if client, ok := doer.(*stdhttp.Client); ok {
		return client.Jar
	}
	if owner, ok := doer.(search.JarOwner); ok {
		return owner.CookieJar()
	}
	return nil
}

// ParseCookieHeader parses a serialized Cookie header value into cookies.
func ParseCookieHeader(raw string) []*stdhttp.Cookie {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	req := &stdhttp.Request{Header: stdhttp.Header{"Cookie": []string{raw}}}
	return req.Cookies()
}

// SerializeCookies renders named, non-empty cookies as a Cookie header value, ordered
// by name so the same jar always serializes identically.
func SerializeCookies(cookies []*stdhttp.Cookie) string {
	usable := make([]*stdhttp.Cookie, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie != nil && strings.TrimSpace(cookie.Name) != "" && cookie.Value != "" {
			usable = append(usable, cookie)
		}
	}
	slices.SortFunc(usable, func(a, b *stdhttp.Cookie) int { return cmp.Compare(a.Name, b.Name) })
	req := &stdhttp.Request{Header: stdhttp.Header{}}
	for _, cookie := range usable {
		req.AddCookie(cookie)
	}
	return req.Header.Get("Cookie")
}
