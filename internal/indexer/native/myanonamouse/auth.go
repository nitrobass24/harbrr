package myanonamouse

import (
	"context"
	"encoding/json"
	"fmt"
	stdhttp "net/http"
	"slices"
	"strings"
	"time"

	apphttp "github.com/autobrr/harbrr/internal/http"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// mamIDCookie is the session cookie name MAM authenticates with and rotates.
const mamIDCookie = "mam_id"

// userDataPath is the account endpoint carrying the user class; vipTTL is how long
// the derived VIP standing is memoized (the oracle's 1h).
const (
	userDataPath = "jsonLoad.php"
	vipTTL       = time.Hour
)

// vipUserClasses are the classes the oracle treats as VIP for fl_vip freeleech
// (compared case-insensitively).
var vipUserClasses = []string{"VIP", "Elite VIP"}

// userDataResponse is the jsonLoad.php subset: the account's class name.
type userDataResponse struct {
	ClassName string `json:"classname"`
}

// hasUserVIP reports whether the configured account is VIP, which is what makes an
// fl_vip row actually freeleech. It fetches jsonLoad.php through the existing session
// and memoizes the answer for vipTTL, matching the oracle. A failure (any transport,
// status, or decode error) is logged at debug and treated as non-VIP — this must never
// fail a search, and non-VIP is the conservative answer (a paid row is served as paid).
func (d *driver) hasUserVIP(ctx context.Context) bool {
	d.mu.Lock()
	if time.Now().Before(d.vipExpires) {
		cached := d.vipCached
		d.mu.Unlock()
		return cached
	}
	d.mu.Unlock()

	vip, err := d.fetchUserVIP(ctx)
	if err != nil {
		d.Log.Debug().Str("driver", d.Def.ID).Str("cause", apphttp.RedactError(err)).
			Msg("myanonamouse: user class lookup failed, treating the account as non-VIP")
		return false
	}
	d.mu.Lock()
	d.vipCached = vip
	d.vipExpires = time.Now().Add(vipTTL)
	d.mu.Unlock()
	return vip
}

// fetchUserVIP performs the jsonLoad.php lookup and maps the class name to VIP.
func (d *driver) fetchUserVIP(ctx context.Context) (bool, error) {
	req, err := d.newRequest(ctx, d.BaseURL+userDataPath, "application/json")
	if err != nil {
		return false, err
	}
	resp, err := d.do(ctx, req)
	if err != nil {
		return false, err
	}
	var data userDataResponse
	if err := json.Unmarshal(resp.Body, &data); err != nil {
		return false, fmt.Errorf("myanonamouse: decode user data: %s", apphttp.DecodeErrorDetail(err, resp.Body))
	}
	class := strings.TrimSpace(data.ClassName)
	return slices.ContainsFunc(vipUserClasses, func(v string) bool { return strings.EqualFold(v, class) }), nil
}

// classifyMAM is MAM's status dialect: a 403 means the mam_id session cookie expired
// or is invalid (there is no 401), wrapped with login.ErrLoginFailed so the registry
// records an auth_failure health event.
var classifyMAM = native.ClassifyAuthOnly403.WithAuthReason("mam_id expired or invalid")

// mamID returns the current (possibly rotated) mam_id under the mutex.
func (d *driver) mamID() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.currentMamID
}

// scrub removes the mam_id session cookie (both the currently ROTATED value and the
// originally configured one) from s so a server-echoed error string cannot leak it.
// MAM's error text is server-controlled free text that reaches a persisted health
// event / webhook, and mam_id is the sole secret, so it is value-scrubbed at the echo
// site via Base.Scrub. The originally configured mam_id is IsSecret-classified (type
// "password"), so it comes from Base.Scrub's derived set (b.Cfg); the rotated value
// lives only in currentMamID (never written back to Cfg — captureRotatedMamID updates
// it and persists it separately), so it is passed as an explicit extra, read under the
// mutex it shares with captureRotatedMamID's writer.
func (d *driver) scrub(s string) string {
	d.mu.Lock()
	current := d.currentMamID
	d.mu.Unlock()
	return d.Scrub(s, current)
}

// captureRotatedMamID scans a response's Set-Cookie headers for a refreshed mam_id
// and, when it changed, updates the in-memory current value and persists it back to
// the encrypted store so the session survives a restart instead of reverting to the
// stored value. The persist is synchronous: MAM rotates mam_id on every response and
// the per-host paced doer serializes MAM requests, so an in-line write keeps the
// stored value in rotation order — a detached goroutine could race and persist an
// older token last. The write is best-effort (the registry logs a failure; it never
// fails the search), and the new value is a secret never logged here.
func (d *driver) captureRotatedMamID(ctx context.Context, header stdhttp.Header) {
	// http.Response.Cookies is the stdlib Set-Cookie parser; a header-only shell
	// response reuses it without carrying a body.
	shell := stdhttp.Response{Header: header}
	for _, c := range shell.Cookies() {
		if c.Name == mamIDCookie && c.Value != "" {
			d.mu.Lock()
			changed := c.Value != d.currentMamID
			d.currentMamID = c.Value
			persist := d.persist
			d.mu.Unlock()
			if changed && persist != nil {
				_ = persist(ctx, mamIDCookie, c.Value)
			}
			return
		}
	}
}

// newRequest builds an authenticated GET with the Cookie: mam_id=… header. The cookie
// rides as a header, never the URL, so the URL carries no secret.
func (d *driver) newRequest(ctx context.Context, rawurl, accept string) (*stdhttp.Request, error) {
	req, err := d.NewRequest(ctx, stdhttp.MethodGet, rawurl, nil)
	if err != nil {
		return nil, err
	}
	// Set the request Cookie header directly (not http.Cookie): the mam_id is an
	// opaque session token, and Secure/HttpOnly/SameSite are response-only attributes
	// that never ride a request cookie anyway.
	req.Header.Set("Cookie", mamIDCookie+"="+d.mamID())
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	return req, nil
}

// do routes a request through the base Do and captures any rotated mam_id from the
// response headers — including a classified-status response (the base returns the
// header shell alongside the error), so a rotation riding a 403/429 is never lost.
func (d *driver) do(ctx context.Context, req *stdhttp.Request) (*native.Response, error) {
	resp, err := d.Do(ctx, req, classifyMAM)
	if resp != nil {
		d.captureRotatedMamID(ctx, resp.Header)
	}
	return resp, err
}

// doDownload is do for the grab path (torrent cap + download error wording).
func (d *driver) doDownload(ctx context.Context, req *stdhttp.Request) (*native.Response, error) {
	resp, err := d.DoDownload(ctx, req, classifyMAM)
	if resp != nil {
		d.captureRotatedMamID(ctx, resp.Header)
	}
	return resp, err
}

// Test verifies the configured mam_id authenticates (the management "test indexer"
// action) via a cheap authenticated search: a 403 surfaces as login.ErrLoginFailed
// (mam_id expired or invalid), a rate-limit status as a RateLimitedError, and a 2xx
// confirms the session. The body is not parsed — only the classification matters.
func (d *driver) Test(ctx context.Context) error {
	req, err := d.newRequest(ctx, d.buildSearchURL(search.Query{}), "application/json")
	if err != nil {
		return err
	}
	_, err = d.do(ctx, req)
	return err
}
