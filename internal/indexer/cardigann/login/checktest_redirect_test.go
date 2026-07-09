package login

import (
	stdhttp "net/http"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// These tests pin CheckTest to Jackett's TestLogin redirect handling
// (CardigannIndexer.TestLogin): fetch WITHOUT auto-following, follow a
// same-domain redirect exactly once, then treat a still-redirecting response as
// login-needed BEFORE looking at the selector. They run through newExec, whose
// real *http.Client owns apphttp.RedirectPolicy — so the no-follow stamp inside
// CheckTest (getNoFollow) is what surfaces the raw 3xx, exactly as on the live
// path. No credential or cookie is asserted or logged.

const logoutSelector = `a[href^="/logout.php"]`

// locationHeader builds a response Header carrying one Location line for a 3xx.
func locationHeader(loc string) stdhttp.Header {
	return stdhttp.Header{"Location": {loc}}
}

// testDef is a minimal definition with a Login.Test block; sel == "" makes it a
// selector-less test block (the redirect signal is then its only evidence).
func testDef(sel string) *loader.Definition {
	return &loader.Definition{Login: &loader.Login{
		Method: "post",
		Path:   "takelogin.php",
		Test:   &loader.PageTestBlock{Path: "index.php", Selector: sel},
	}}
}

// TestCheckTestCrossDomainRedirectSelectorless is the U4-F4 fail-before /
// pass-after case: a selector-less test block whose path redirects OFF-SITE
// (expired session -> external login host). Jackett never follows a cross-domain
// redirect, so its only signal is the redirect -> login needed. The pre-fix
// CheckTest followed the redirect and, seeing a 200 with no selector, reported
// "logged in" (returned true) with dead credentials.
func TestCheckTestCrossDomainRedirectSelectorless(t *testing.T) {
	t.Parallel()

	rt := newReplay(
		t,
		// Expired session: the test path 302s off-site to a login host.
		step{wantMethod: stdhttp.MethodGet, wantPath: "/index.php", status: stdhttp.StatusFound, respHeader: locationHeader("https://login.other.example/")},
		// Reached ONLY by the pre-fix code, which followed the redirect and read
		// the off-site 200 as a logged-in page.
		step{wantMethod: stdhttp.MethodGet, wantPath: "/", bodyFile: "logged_out.html"},
	)
	e := newExec(t, rt, nil)

	ok, err := e.CheckTest(t.Context(), testDef(""))
	if err != nil {
		t.Fatalf("CheckTest: %v", err)
	}
	if ok {
		t.Fatal("CheckTest = true on a cross-domain redirect (dead session reported as logged in); want false")
	}
	if rt.requestCount() != 1 {
		t.Fatalf("made %d requests, want 1 (a cross-domain redirect must not be followed)", rt.requestCount())
	}
}

// TestCheckTestCrossDomainRedirectSelectorBearing confirms the bail happens
// BEFORE the selector check (Jackett throws on the redirect at ~888, never
// reaching the selector at ~909): a selector-bearing block on a cross-domain
// redirect is login-needed and no follow-up request is made to evaluate a
// selector.
func TestCheckTestCrossDomainRedirectSelectorBearing(t *testing.T) {
	t.Parallel()

	rt := newReplay(
		t,
		step{wantMethod: stdhttp.MethodGet, wantPath: "/index.php", status: stdhttp.StatusFound, respHeader: locationHeader("https://login.other.example/login")},
	)
	e := newExec(t, rt, nil)

	ok, err := e.CheckTest(t.Context(), testDef(logoutSelector))
	if err != nil {
		t.Fatalf("CheckTest: %v", err)
	}
	if ok {
		t.Fatal("CheckTest = true on a cross-domain redirect; want false (bail before selector)")
	}
	if rt.requestCount() != 1 {
		t.Fatalf("made %d requests, want 1 (no follow, no selector fetch)", rt.requestCount())
	}
}

// TestCheckTestSameDomainRedirectFollowedOnce pins Jackett's single same-domain
// follow (FollowIfRedirect maxRedirects:1): a same-domain 302 to a non-redirect
// page is followed once; a selector-less block then trusts that landing.
func TestCheckTestSameDomainRedirectFollowedOnce(t *testing.T) {
	t.Parallel()

	rt := newReplay(
		t,
		step{wantMethod: stdhttp.MethodGet, wantPath: "/index.php", status: stdhttp.StatusFound, respHeader: locationHeader("/dashboard.php")},
		step{wantMethod: stdhttp.MethodGet, wantPath: "/dashboard.php", bodyFile: "logged_out.html"},
	)
	e := newExec(t, rt, nil)

	ok, err := e.CheckTest(t.Context(), testDef(""))
	if err != nil {
		t.Fatalf("CheckTest: %v", err)
	}
	if !ok {
		t.Fatal("CheckTest = false; a same-domain 302 -> 200 with no selector is followed once and reports logged in")
	}
	if rt.requestCount() != 2 {
		t.Fatalf("made %d requests, want 2 (test path + one same-domain follow)", rt.requestCount())
	}
}

// TestCheckTestSameDomainRedirectToLoggedInPage guards the corpus: a legitimate
// same-domain redirect to a logged-in page whose selector IS present must stay
// logged in. A naive "any 3xx => login needed" fix would wrongly report false
// here and trigger a needless relogin.
func TestCheckTestSameDomainRedirectToLoggedInPage(t *testing.T) {
	t.Parallel()

	rt := newReplay(
		t,
		step{wantMethod: stdhttp.MethodGet, wantPath: "/index.php", status: stdhttp.StatusFound, respHeader: locationHeader("/home.php")},
		step{wantMethod: stdhttp.MethodGet, wantPath: "/home.php", bodyFile: "logged_in.html"},
	)
	e := newExec(t, rt, nil)

	ok, err := e.CheckTest(t.Context(), testDef(logoutSelector))
	if err != nil {
		t.Fatalf("CheckTest: %v", err)
	}
	if !ok {
		t.Fatal("CheckTest = false; a same-domain redirect to a page carrying the selector is logged in")
	}
	if rt.requestCount() != 2 {
		t.Fatalf("made %d requests, want 2 (test path + one same-domain follow)", rt.requestCount())
	}
}

// TestCheckTestRedirectChainBailsAfterOneHop pins the single-hop cap: after the
// one same-domain follow the response is STILL a redirect, so login is needed
// (Jackett re-checks IsRedirect after FollowIfRedirect maxRedirects:1).
func TestCheckTestRedirectChainBailsAfterOneHop(t *testing.T) {
	t.Parallel()

	rt := newReplay(
		t,
		step{wantMethod: stdhttp.MethodGet, wantPath: "/index.php", status: stdhttp.StatusFound, respHeader: locationHeader("/a.php")},
		step{wantMethod: stdhttp.MethodGet, wantPath: "/a.php", status: stdhttp.StatusFound, respHeader: locationHeader("/b.php")},
	)
	e := newExec(t, rt, nil)

	ok, err := e.CheckTest(t.Context(), testDef(""))
	if err != nil {
		t.Fatalf("CheckTest: %v", err)
	}
	if ok {
		t.Fatal("CheckTest = true on a redirect chain still redirecting after one hop; want false")
	}
	if rt.requestCount() != 2 {
		t.Fatalf("made %d requests, want 2 (test path + exactly one follow)", rt.requestCount())
	}
}

// TestCheckTestSelectorlessOK is the unchanged happy path: a non-redirect 200
// with no selector is logged in.
func TestCheckTestSelectorlessOK(t *testing.T) {
	t.Parallel()

	rt := newReplay(
		t,
		step{wantMethod: stdhttp.MethodGet, wantPath: "/index.php", bodyFile: "logged_out.html"},
	)
	e := newExec(t, rt, nil)

	ok, err := e.CheckTest(t.Context(), testDef(""))
	if err != nil {
		t.Fatalf("CheckTest: %v", err)
	}
	if !ok {
		t.Fatal("CheckTest = false on a non-redirect 200 with no selector; want true")
	}
}

// TestCheckTestSelectorBearingOK is the unchanged selector happy path: a
// non-redirect 200 whose selector matches is logged in.
func TestCheckTestSelectorBearingOK(t *testing.T) {
	t.Parallel()

	rt := newReplay(
		t,
		step{wantMethod: stdhttp.MethodGet, wantPath: "/index.php", bodyFile: "logged_in.html"},
	)
	e := newExec(t, rt, nil)

	ok, err := e.CheckTest(t.Context(), testDef(logoutSelector))
	if err != nil {
		t.Fatalf("CheckTest: %v", err)
	}
	if !ok {
		t.Fatal("CheckTest = false on a 200 whose selector matches; want true")
	}
}

// TestCheckTestNon200SelectorBearing: a non-redirect error status (403) whose
// selector cannot match the error body is login-needed — Jackett's selector
// check fails (selection.Length == 0). Only the redirect is special-cased; a
// non-redirect status flows into the normal selector logic.
func TestCheckTestNon200SelectorBearing(t *testing.T) {
	t.Parallel()

	rt := newReplay(
		t,
		step{wantMethod: stdhttp.MethodGet, wantPath: "/index.php", status: stdhttp.StatusForbidden},
	)
	e := newExec(t, rt, nil)

	ok, err := e.CheckTest(t.Context(), testDef(logoutSelector))
	if err != nil {
		t.Fatalf("CheckTest: %v", err)
	}
	if ok {
		t.Fatal("CheckTest = true on a 403 whose selector does not match; want false")
	}
}

// TestCheckTestLookAlikeHostRedirectNotFollowed guards the trailing-slash
// normalization in crossDomainRedirect. The test's BaseURL ("https://tracker.example",
// no trailing slash — a value a user can save unnormalized) must not prefix-match
// a look-alike host. A redirect to "https://tracker.example.evil.com/…" is
// cross-domain (Jackett's SiteLink is slash-terminated precisely to catch this),
// so CheckTest must NOT follow it — the replay declares only the one request, so a
// follow would fail with "unexpected extra request".
func TestCheckTestLookAlikeHostRedirectNotFollowed(t *testing.T) {
	t.Parallel()

	rt := newReplay(
		t,
		step{wantMethod: stdhttp.MethodGet, wantPath: "/index.php", status: stdhttp.StatusFound, respHeader: locationHeader("https://tracker.example.evil.com/login")},
	)
	e := newExec(t, rt, nil)

	ok, err := e.CheckTest(t.Context(), testDef(""))
	if err != nil {
		t.Fatalf("CheckTest: %v", err)
	}
	if ok {
		t.Fatal("CheckTest = true on a look-alike-host redirect; a non-slash BaseURL must not prefix-match tracker.example.evil.com (want false)")
	}
}

// TestCheckTestNon200Selectorless documents a DELIBERATE parity choice: Jackett's
// TestLogin bails only on a redirect or a failed selector, so a non-redirect
// status with no selector returns true (it never inspects the status code). This
// intentionally differs from the U4-F4 finding's suggestion of treating any
// non-2xx as login-needed; matching Jackett is the prime directive.
func TestCheckTestNon200Selectorless(t *testing.T) {
	t.Parallel()

	rt := newReplay(
		t,
		step{wantMethod: stdhttp.MethodGet, wantPath: "/index.php", status: stdhttp.StatusForbidden},
	)
	e := newExec(t, rt, nil)

	ok, err := e.CheckTest(t.Context(), testDef(""))
	if err != nil {
		t.Fatalf("CheckTest: %v", err)
	}
	if !ok {
		t.Fatal("CheckTest = false on a non-redirect 403 with no selector; Jackett returns true (only redirect/selector are checked)")
	}
}
