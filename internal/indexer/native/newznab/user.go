package newznab

import (
	"context"
	"encoding/xml"
	"fmt"
	stdhttp "net/http"
	"strings"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// Reserved instance-setting keys for the request budget. The first three are read by
// the registry's RequestBudget (internal/indexer/registry/budget.go); the *_source
// markers are the provenance this driver writes beside a cap it SEEDED, so the usage
// meter can tell a detected cap from an operator-typed one (autobrr/harbrr#377). The
// names are duplicated rather than imported because the registry imports this package.
const (
	settingQueryLimit       = "query_limit"
	settingGrabLimit        = "grab_limit"
	settingLimitsUnit       = "limits_unit"
	settingQueryLimitSource = "query_limit_source"
	settingGrabLimitSource  = "grab_limit_source"
	limitSourceDetected     = "detected"
)

// budgetLimits is the instance's stored request-budget knobs as they stood when the
// driver was built — the baseline a t=user seed is applied against. query/grab are 0
// when unset (or stored non-numeric); a non-zero value is never overwritten, so the
// provenance markers are read by the meter for display only, never by the seed decision.
type budgetLimits struct {
	query   int
	grab    int
	unitSet bool
}

// readBudgetLimits snapshots the budget settings out of the instance config.
func readBudgetLimits(cfg map[string]string) budgetLimits {
	return budgetLimits{
		query:   positiveIntOr(cfg[settingQueryLimit], 0),
		grab:    positiveIntOr(cfg[settingGrabLimit], 0),
		unitSet: strings.TrimSpace(cfg[settingLimitsUnit]) != "",
	}
}

// seedable reports whether a probe could change anything: only an UNSET cap can be
// seeded, so when both already carry a value there is nothing discovery could say and
// the request is not worth making.
func (l budgetLimits) seedable() bool {
	return l.query == 0 || l.grab == 0
}

// userRoot is the <user .../> document a Newznab server returns for ?t=user. Only the
// two request caps are modelled: apirequests is the account's daily API cap and
// downloadrequests its daily grab cap. The response also carries username/role/grabs/
// createddate — deliberately not decoded, since nothing reads them and the username has
// no reason to enter a log. Attributes are decoded as strings for the same reason
// capsLimits does: a bare int would coerce a missing attribute to 0 instead of "absent".
type userRoot struct {
	XMLName          xml.Name
	APIRequests      string `xml:"apirequests,attr"`
	DownloadRequests string `xml:"downloadrequests,attr"`
}

// queryCap / grabCap are the parsed daily caps, 0 when the attribute is absent, blank,
// zero, negative, or not a number — every one of which means "nothing discovered".
func (u *userRoot) queryCap() int { return positiveIntOr(u.APIRequests, 0) }
func (u *userRoot) grabCap() int  { return positiveIntOr(u.DownloadRequests, 0) }

// seedBudget probes ?t=user and seeds the instance's request-budget caps from the
// account's own advertised limits (autobrr/harbrr#377). It is OPPORTUNISTIC, not a
// capability: t=user is optional in the wild, so anything other than a well-formed
// <user> with positive caps — an error, a 404, an HTML page, a missing attribute — is
// swallowed and leaves exactly today's behaviour. No health event is recorded, nothing
// is retried and no error reaches the caller; the outcome is recorded at debug only
// (autobrr/harbrr#440), with the cause routed through RedactError because the probe
// URL embeds the apikey. It runs only from Test (the operator-initiated add / test
// action), never on a schedule and never on the search or grab path, so it costs at
// most one extra request per test — and none at all when there is no way to persist the
// result or nothing left to seed.
func (d *driver) seedBudget(ctx context.Context) {
	if d.persist == nil || !d.limits.seedable() {
		d.Log.Debug().Str("driver", d.Def.ID).
			Msg("newznab: budget seed not attempted (caps already set or nowhere to persist)")
		return
	}
	user, err := d.fetchUser(ctx)
	if err != nil {
		d.Log.Debug().Str("driver", d.Def.ID).Str("cause", apphttp.RedactError(err)).
			Msg("newznab: budget seed probe failed")
		return
	}
	// seeded vs failed keeps the log honest about persistence: a discovered cap whose
	// write failed is a failed seed, not "nothing discovered" — the retry semantics
	// (seedLimit's write order) already guarantee the next Test completes it.
	var seededKeys, failedKeys []string
	seedOne := func(key, sourceKey string, current, discovered int) {
		if !shouldSeed(current, discovered) {
			return
		}
		if d.seedLimit(ctx, key, sourceKey, current, discovered) {
			seededKeys = append(seededKeys, key)
		} else {
			failedKeys = append(failedKeys, key)
		}
	}
	seedOne(settingQueryLimit, settingQueryLimitSource, d.limits.query, user.queryCap())
	seedOne(settingGrabLimit, settingGrabLimitSource, d.limits.grab, user.grabCap())
	if len(seededKeys) == 0 && len(failedKeys) == 0 {
		d.Log.Debug().Str("driver", d.Def.ID).
			Msg("newznab: budget seed probed, nothing discovered")
		return
	}
	// apirequests/downloadrequests are DAILY caps, so the unit that makes them true is
	// day — written only when the operator has not chosen one (and only when a cap was
	// actually seeded, so a probe that discovered nothing writes nothing).
	if len(seededKeys) > 0 && !d.limits.unitSet {
		_ = d.persist(ctx, settingLimitsUnit, "day")
	}
	if len(failedKeys) > 0 {
		d.Log.Debug().Str("driver", d.Def.ID).Strs("seeded", seededKeys).Strs("failed", failedKeys).
			Msg("newznab: budget seed persist failed, the next Test retries")
		return
	}
	d.Log.Debug().Str("driver", d.Def.ID).Strs("seeded", seededKeys).
		Msg("newznab: budget seeded")
}

// seedLimit writes discovered into key (plus its provenance marker) when shouldSeed
// allows it, reporting whether it wrote. A persist failure is swallowed like the caps
// cache's: the seed is an optimisation, and the next Test retries it.
//
// The provenance marker is written FIRST so that retry is real. shouldSeed only
// revisits an UNSET cap, so writing the cap first would make a failed marker write
// permanent — the cap would be set, the seed would never run again, and the meter
// would show an operator-typed cap forever. In this order a half-written seed always
// leaves the cap unset, so the next Test simply seeds again; the worst residue is a
// marker with no cap yet, which the meter reads only alongside a cap.
func (d *driver) seedLimit(ctx context.Context, key, sourceKey string, current, discovered int) bool {
	if !shouldSeed(current, discovered) {
		return false
	}
	if err := d.persist(ctx, sourceKey, limitSourceDetected); err != nil {
		return false
	}
	if err := d.persist(ctx, key, itoa(discovered)); err != nil {
		return false
	}
	return true
}

// shouldSeed decides whether a discovered cap may be written: only into an UNSET one.
// Nothing discovered (non-positive) never writes, and a cap that already has a value is
// never touched — whoever put it there, operator or an earlier probe.
//
// Deliberately NOT re-seeding a previously-detected cap with a stricter value: the
// provenance marker records where a number came from, but it cannot tell whether the
// operator has since edited it, so re-seeding would silently overwrite a value they
// typed. Discovery corrects ignorance, never intent — and the drift case it would have
// covered (the tracker lowering your cap) is already handled by the reactive learned
// latch, which marks the budget spent from the tracker's own quota error.
func shouldSeed(current, discovered int) bool {
	return discovered > 0 && current == 0
}

// fetchUser GETs ?t=user and parses the account document. The URL embeds the apikey, so
// (exactly like getCaps) a request-build failure surfaces only the endpoint's
// scheme://host with the cause routed through apphttp.RedactURLError, and the transport
// error from Do is already host-redacted structurally.
func (d *driver) fetchUser(ctx context.Context) (*userRoot, error) {
	rawurl := d.buildAPIURL("user")
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, rawurl, nil)
	if err != nil {
		return nil, fmt.Errorf("newznab: build user request to %s: %w", apphttp.SchemeHost(rawurl), apphttp.RedactURLError(err))
	}
	req.Header.Set("Accept", "application/rss+xml, application/xml, text/xml")
	resp, err := d.Do(ctx, req, native.ClassifyRateLimit403)
	if err != nil {
		return nil, err
	}
	return parseUser(resp.Body)
}

// parseUser decodes a ?t=user response body. Any body that is not a <user> document —
// malformed XML, an HTML error page, a Newznab <error> envelope, a <caps> document from
// a server that ignores unknown t= functions — is a parse error, which the sole caller
// treats as "nothing discovered".
func parseUser(body []byte) (*userRoot, error) {
	var root userRoot
	if err := xml.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("newznab: decode user response: %s: %w", apphttp.DecodeErrorDetail(err, body), search.ErrParseError)
	}
	if root.XMLName.Local != "user" {
		return nil, fmt.Errorf("newznab: user response root is <%s>, want <user>: %w", root.XMLName.Local, search.ErrParseError)
	}
	return &root, nil
}
