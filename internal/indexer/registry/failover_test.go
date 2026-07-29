package registry_test

import (
	"context"
	"errors"
	"io"
	"net"
	stdhttp "net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/registry"
	"github.com/autobrr/harbrr/internal/secrets"
)

// failoverDefYAML is a three-link definition with a credential-submitting login, so a
// candidate host can be made to answer HTTP and still fail login (a 401 on the login
// POST) — the case the failover must treat as a FAILED candidate.
const failoverDefYAML = `---
id: failtracker
name: Failover Test Tracker
description: Failover test fixture
language: en-US
type: private
encoding: UTF-8
links:
  - https://one.invalid/
  - https://two.invalid/
  - https://three.invalid/
caps:
  categorymappings:
    - {id: 1, cat: Movies}
  modes:
    search: [q]
login:
  path: /login
  method: post
  inputs:
    username: "{{ .Config.username }}"
    password: "{{ .Config.password }}"
settings:
  - name: username
    type: text
    label: Username
  - name: password
    type: password
    label: Password
search:
  path: /browse.php
  inputs:
    q: "{{ .Keywords }}"
  rows:
    selector: table.results > tbody > tr
  fields:
    title:
      selector: a.title
    download:
      selector: a.dl
      attribute: href
    category:
      selector: td.cat
      attribute: data-cat
    size:
      selector: td.size
    seeders:
      selector: td.seeders
    leechers:
      selector: td.leechers
`

const (
	hostOne   = "https://one.invalid/"
	hostTwo   = "https://two.invalid/"
	hostThree = "https://three.invalid/"
)

// hostReply is what the fake transport does for one host: an HTTP status + body, or a
// transport-level failure. A host with no entry at all is treated as unreachable, so a
// test only has to name the hosts it wants alive.
type hostReply struct {
	status int
	body   string
}

// hostDoer answers per request host and records every host it was asked for — the
// credential-scoping assertion (a probe must only ever talk to hosts the definition
// lists) is a property of this record.
type hostDoer struct {
	mu    sync.Mutex
	alive map[string]hostReply
	seen  []string
}

func (d *hostDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	d.mu.Lock()
	d.seen = append(d.seen, req.URL.Host)
	reply, ok := d.alive[req.URL.Host]
	d.mu.Unlock()
	if !ok {
		// Shaped like the real client's failure so it classifies as transport: a
		// *url.Error wrapping a *net.OpError is what a refused dial looks like.
		return nil, &url.Error{
			Op:  req.Method,
			URL: req.URL.String(),
			Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")},
		}
	}
	return &stdhttp.Response{
		StatusCode: reply.status,
		Header:     stdhttp.Header{},
		Body:       io.NopCloser(strings.NewReader(reply.body)),
		Request:    req,
	}, nil
}

// hosts returns the distinct hosts the doer was asked for, in first-seen order.
func (d *hostDoer) hosts() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []string
	for _, h := range d.seen {
		if !slices.Contains(out, h) {
			out = append(out, h)
		}
	}
	return out
}

// calls counts every request made so far (used to prove a backed-off cycle makes none).
func (d *hostDoer) calls() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.seen)
}

// liveHost is the canned 200 every reachable host answers with: the login POST sees a
// body with no error selectors, the search sees one parseable row.
func liveHost() hostReply {
	return hostReply{status: stdhttp.StatusOK, body: `<!DOCTYPE html><html><body>
<table class="results"><tbody>
<tr><td class="cat" data-cat="1"></td>
<td><a class="title" href="/d?id=1">Big Buck Bunny 1080p</a></td>
<td><a class="dl" href="/dl?id=1">dl</a></td>
<td class="size">2.5 GB</td><td class="seeders">42</td><td class="leechers">7</td></tr>
</tbody></table></body></html>`}
}

// movableClock is a test clock the failover tests step forward: the trigger is a
// failure STREAK measured in wall time, so a frozen clock can never reach it.
type movableClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *movableClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *movableClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// newFailoverRegistry builds a registry over the three-link failtracker def with the
// given doer, a steppable clock, and a recording health sink.
func newFailoverRegistry(t *testing.T, doer search.Doer, clk *movableClock, sink registry.HealthSink) (*registry.Registry, *database.DB) {
	t.Helper()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	dropin := t.TempDir()
	if err := os.WriteFile(filepath.Join(dropin, "failtracker.yml"), []byte(failoverDefYAML), 0o600); err != nil {
		t.Fatalf("write def: %v", err)
	}
	keyring, err := secrets.OpenKeyring(secrets.KeyringOptions{EncryptionKey: testHexKey}, zerolog.Nop())
	if err != nil {
		t.Fatalf("open keyring: %v", err)
	}
	opts := []registry.Option{
		registry.WithClock(clk.now),
		registry.WithDoerFactory(func(registry.ClientParams) (search.Doer, error) { return doer, nil }),
	}
	if sink != nil {
		opts = append(opts, registry.WithHealthSink(sink))
	}
	return registry.New(db, loader.New(dropin), keyring, nil, opts...), db
}

// addFailtracker configures the fixture indexer with the given extra settings.
func addFailtracker(t *testing.T, reg *registry.Registry, extra map[string]string) {
	t.Helper()
	settings := map[string]string{"username": "u", "password": "NOTREALSECRET00"}
	for k, v := range extra {
		settings[k] = v
	}
	if _, err := reg.Add(context.Background(), registry.AddParams{
		Slug: "ft", DefinitionID: "failtracker", Settings: settings,
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
}

// failTwice drives two failing searches ~11 minutes apart — the minimum evidence the
// failover asks for (a streak at least failoverAfter long, and a repeated failure
// rather than a first one). advance is how far the clock moves between them, which a
// harsher kind's longer disable window forces up.
func failTwice(t *testing.T, reg *registry.Registry, clk *movableClock, advance time.Duration) {
	t.Helper()
	ctx := context.Background()
	for i := range 2 {
		idx, ok := reg.Indexer(ctx, "ft")
		if !ok {
			t.Fatal("Indexer(ft) not resolved")
		}
		if _, err := idx.Search(ctx, search.Query{Keywords: "bunny"}); err == nil {
			t.Fatalf("search %d returned nil error, want a classified failure", i+1)
		}
		if i == 0 {
			clk.advance(advance)
		}
	}
}

// promotedBaseURL reads the stored failover promotion straight from the settings, which
// is where a promotion durably lives (and how it is reverted).
func promotedBaseURL(t *testing.T, reg *registry.Registry) string {
	t.Helper()
	_, views, err := reg.Get(context.Background(), "ft")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	for _, v := range views {
		if v.Name == "failover_base_url" {
			if v.Secret {
				t.Fatal("failover_base_url must not be classified secret; the operator has to be able to read it")
			}
			return v.Value
		}
	}
	return ""
}

// TestFailoverPromotesNextLinkOnTransportFailure is the happy path: the configured host
// stops answering at the transport level, the failure repeats, and the indexer is moved
// onto the next host the definition already lists — recorded, notified, and immediately
// usable (the circuit's disable window is cleared, not left gating the new host).
func TestFailoverPromotesNextLinkOnTransportFailure(t *testing.T) {
	t.Parallel()
	doer := &hostDoer{alive: map[string]hostReply{"two.invalid": liveHost()}}
	clk := &movableClock{t: fixedClock()}
	sink := &recordingSink{}
	reg, db := newFailoverRegistry(t, doer, clk, sink)
	ctx := context.Background()
	addFailtracker(t, reg, nil)

	failTwice(t, reg, clk, 11*time.Minute)

	if got := promotedBaseURL(t, reg); got != hostTwo {
		t.Fatalf("promoted base URL = %q, want %q", got, hostTwo)
	}
	if got := doer.hosts(); !slices.Equal(got, []string{"one.invalid", "two.invalid"}) {
		t.Errorf("hosts contacted = %v, want the dead configured host then the promoted one", got)
	}

	// The promotion is in the indexer's visible timeline...
	inst, err := database.Instances{}.GetBySlug(ctx, db, "ft")
	if err != nil {
		t.Fatalf("get instance: %v", err)
	}
	events, err := database.Health{}.Recent(ctx, db, inst.ID, 5)
	if err != nil {
		t.Fatalf("recent events: %v", err)
	}
	if len(events) == 0 || events[0].Kind != domain.HealthBaseURLPromoted {
		t.Fatalf("newest health event = %+v, want a %s event", events, domain.HealthBaseURLPromoted)
	}
	if !strings.Contains(events[0].Detail, hostTwo) {
		t.Errorf("promotion detail = %q, want it to name the promoted host", events[0].Detail)
	}

	// ...and was announced, because failover must never be silent.
	var announced bool
	for _, ev := range sink.events() {
		if ev.kind == domain.HealthBaseURLPromoted {
			announced = true
		}
	}
	if !announced {
		t.Error("no promotion notification reached the health sink")
	}

	// The circuit no longer gates dispatch, so the promoted host is usable at once.
	circuit, err := database.Circuit{}.Get(ctx, db, inst.ID)
	if err != nil {
		t.Fatalf("get circuit: %v", err)
	}
	if circuit.IsDisabled(clk.now()) {
		t.Error("circuit must not still be open after a successful promotion")
	}

	// And the next search actually reaches the promoted host and succeeds.
	idx, ok := reg.Indexer(ctx, "ft")
	if !ok {
		t.Fatal("Indexer(ft) not resolved after promotion")
	}
	releases, err := idx.Search(ctx, search.Query{Keywords: "bunny"})
	if err != nil {
		t.Fatalf("search after promotion: %v", err)
	}
	if len(releases) == 0 {
		t.Error("search after promotion returned no releases")
	}
}

// TestFailoverNeverRotatesOnNonHostFailures is the rule that matters most: a failure
// that means the host is FINE — bad credentials, a rate limit — must never rotate. The
// assertion is the strong one: the doer is never called against a second host at all,
// so no mirror is ever offered the credentials that just failed.
func TestFailoverNeverRotatesOnNonHostFailures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		reply hostReply
		// advance clears the disable window that kind's escalation rung sets, so the
		// second failure actually reaches the tracker instead of being gated.
		advance time.Duration
	}{
		{
			name:    "auth failure",
			reply:   hostReply{status: stdhttp.StatusUnauthorized, body: "<html></html>"},
			advance: 2 * time.Hour,
		},
		{
			name:    "rate limited",
			reply:   hostReply{status: stdhttp.StatusServiceUnavailable, body: "<html></html>"},
			advance: 30 * time.Minute,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			doer := &hostDoer{alive: map[string]hostReply{
				"one.invalid": tt.reply,
				// Both mirrors are perfectly healthy — the only thing stopping a
				// rotation is the failure's SHAPE.
				"two.invalid":   liveHost(),
				"three.invalid": liveHost(),
			}}
			clk := &movableClock{t: fixedClock()}
			reg, _ := newFailoverRegistry(t, doer, clk, nil)
			addFailtracker(t, reg, nil)

			failTwice(t, reg, clk, tt.advance)

			if got := doer.hosts(); !slices.Equal(got, []string{"one.invalid"}) {
				t.Errorf("hosts contacted = %v, want only the configured host", got)
			}
			if got := promotedBaseURL(t, reg); got != "" {
				t.Errorf("promoted base URL = %q, want none", got)
			}
		})
	}
}

// TestFailoverSkipsCandidateThatFailsLogin proves "answers HTTP but fails login is not a
// success": the second link 401s the login POST, so the walk continues to the third.
func TestFailoverSkipsCandidateThatFailsLogin(t *testing.T) {
	t.Parallel()
	doer := &hostDoer{alive: map[string]hostReply{
		"two.invalid":   {status: stdhttp.StatusUnauthorized, body: "<html></html>"},
		"three.invalid": liveHost(),
	}}
	clk := &movableClock{t: fixedClock()}
	reg, _ := newFailoverRegistry(t, doer, clk, nil)
	addFailtracker(t, reg, nil)

	failTwice(t, reg, clk, 11*time.Minute)

	if got := promotedBaseURL(t, reg); got != hostThree {
		t.Fatalf("promoted base URL = %q, want %q (the 401 candidate must be skipped)", got, hostThree)
	}
	if got := doer.hosts(); !slices.Equal(got, []string{"one.invalid", "two.invalid", "three.invalid"}) {
		t.Errorf("hosts contacted = %v, want every link tried in order", got)
	}
}

// TestFailoverBacksOffWhenEveryCandidateFails proves the list is walked once and then
// left alone: a second qualifying failure inside the backoff makes no probe requests at
// all, so a definition whose every host is dead is not re-walked on every query.
func TestFailoverBacksOffWhenEveryCandidateFails(t *testing.T) {
	t.Parallel()
	doer := &hostDoer{alive: map[string]hostReply{}} // every host unreachable
	clk := &movableClock{t: fixedClock()}
	reg, _ := newFailoverRegistry(t, doer, clk, nil)
	addFailtracker(t, reg, nil)

	failTwice(t, reg, clk, 11*time.Minute)
	if got := doer.hosts(); !slices.Equal(got, []string{"one.invalid", "two.invalid", "three.invalid"}) {
		t.Fatalf("hosts contacted = %v, want one full walk of the link list", got)
	}
	afterCycle := doer.calls()

	// A third qualifying failure, still inside failoverRetry: the search itself hits
	// the configured host, and nothing else.
	clk.advance(11 * time.Minute)
	idx, ok := reg.Indexer(context.Background(), "ft")
	if !ok {
		t.Fatal("Indexer(ft) not resolved")
	}
	if _, err := idx.Search(context.Background(), search.Query{Keywords: "bunny"}); err == nil {
		t.Fatal("third search returned nil error, want a transport failure")
	}
	if got, want := doer.calls(), afterCycle+1; got != want {
		t.Errorf("requests after the backed-off failure = %d, want %d (the failing search only)", got, want)
	}
}

// TestFailoverPinDisablesRotation proves the operator pin: pinned is pinned, even when
// the configured host is definitively gone and a healthy mirror is right there.
func TestFailoverPinDisablesRotation(t *testing.T) {
	t.Parallel()
	doer := &hostDoer{alive: map[string]hostReply{"two.invalid": liveHost()}}
	clk := &movableClock{t: fixedClock()}
	reg, _ := newFailoverRegistry(t, doer, clk, nil)
	addFailtracker(t, reg, map[string]string{"failover_disabled": "true"})

	failTwice(t, reg, clk, 11*time.Minute)

	if got := doer.hosts(); !slices.Equal(got, []string{"one.invalid"}) {
		t.Errorf("hosts contacted = %v, want only the configured host on a pinned indexer", got)
	}
	if got := promotedBaseURL(t, reg); got != "" {
		t.Errorf("promoted base URL = %q, want none on a pinned indexer", got)
	}
}

// TestFailoverIsNoOpForSingleLinkDefinition covers the cheap path: a definition with one
// link has nothing to rotate to, so a repeated transport failure costs no probe at all.
// (testtracker, the shared fixture, has exactly one link.)
func TestFailoverIsNoOpForSingleLinkDefinition(t *testing.T) {
	t.Parallel()
	doer := &hostDoer{alive: map[string]hostReply{}}
	clk := &movableClock{t: fixedClock()}
	db, ldr, keyring := newRegistryDeps(t)
	reg := registry.New(
		db, ldr, keyring, nil,
		registry.WithClock(clk.now),
		registry.WithDoerFactory(func(registry.ClientParams) (search.Doer, error) { return doer, nil }),
	)
	ctx := context.Background()
	if _, err := reg.Add(ctx, registry.AddParams{
		Slug: "tt", DefinitionID: "testtracker", Settings: map[string]string{"apikey": "x"},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	for i := range 2 {
		idx, ok := reg.Indexer(ctx, "tt")
		if !ok {
			t.Fatal("Indexer(tt) not resolved")
		}
		if _, err := idx.Search(ctx, search.Query{Keywords: "bunny"}); err == nil {
			t.Fatalf("search %d returned nil error, want a transport failure", i+1)
		}
		if i == 0 {
			clk.advance(11 * time.Minute)
		}
	}
	if got := doer.hosts(); !slices.Equal(got, []string{"html.invalid"}) {
		t.Errorf("hosts contacted = %v, want only the definition's single link", got)
	}
}

// TestFailoverStateSurfacesEffectiveHostAndReverts is the API-side contract: after a
// promotion the effective host differs from the configured one and is reported as
// promoted, and clearing the setting — an ordinary settings PATCH — puts it back.
func TestFailoverStateSurfacesEffectiveHostAndReverts(t *testing.T) {
	t.Parallel()
	doer := &hostDoer{alive: map[string]hostReply{"two.invalid": liveHost()}}
	clk := &movableClock{t: fixedClock()}
	reg, db := newFailoverRegistry(t, doer, clk, nil)
	ctx := context.Background()
	addFailtracker(t, reg, nil)

	inst, err := database.Instances{}.GetBySlug(ctx, db, "ft")
	if err != nil {
		t.Fatalf("get instance: %v", err)
	}
	before, err := reg.FailoverState(ctx, inst)
	if err != nil {
		t.Fatalf("FailoverState: %v", err)
	}
	if before.EffectiveBaseURL != hostOne || before.PromotedBaseURL != "" || before.Disabled {
		t.Fatalf("FailoverState before failover = %+v, want the configured host and no promotion", before)
	}

	failTwice(t, reg, clk, 11*time.Minute)

	after, err := reg.FailoverState(ctx, inst)
	if err != nil {
		t.Fatalf("FailoverState: %v", err)
	}
	if after.EffectiveBaseURL != hostTwo || after.PromotedBaseURL != hostTwo {
		t.Fatalf("FailoverState after failover = %+v, want %q as both effective and promoted", after, hostTwo)
	}

	// Revert: clearing the reserved setting is the whole mechanism.
	if err := reg.Update(ctx, "ft", registry.UpdateParams{
		Settings: map[string]string{"failover_base_url": ""},
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	reverted, err := reg.FailoverState(ctx, inst)
	if err != nil {
		t.Fatalf("FailoverState: %v", err)
	}
	if reverted.EffectiveBaseURL != hostOne || reverted.PromotedBaseURL != "" {
		t.Fatalf("FailoverState after revert = %+v, want the configured host back", reverted)
	}
}
