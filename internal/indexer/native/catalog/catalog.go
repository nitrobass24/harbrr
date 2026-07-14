// Package catalog aggregates every native family driver's exported Families()
// into one map, so a caller (production wiring in cmd/harbrr, or a test that
// needs real drivers) can obtain the full native catalog without importing each
// of the 14 concrete driver packages itself. It cannot live in package native
// (the drivers import native for the Family type — that would cycle), so it sits
// one level up as the sole place that imports every driver.
package catalog

import (
	"github.com/autobrr/harbrr/internal/indexer/native"
	"github.com/autobrr/harbrr/internal/indexer/native/animebytes"
	"github.com/autobrr/harbrr/internal/indexer/native/avistaz"
	"github.com/autobrr/harbrr/internal/indexer/native/beyondhd"
	"github.com/autobrr/harbrr/internal/indexer/native/broadcastthenet"
	"github.com/autobrr/harbrr/internal/indexer/native/filelist"
	"github.com/autobrr/harbrr/internal/indexer/native/gazelle"
	"github.com/autobrr/harbrr/internal/indexer/native/gazellegames"
	"github.com/autobrr/harbrr/internal/indexer/native/hdbits"
	"github.com/autobrr/harbrr/internal/indexer/native/iptorrents"
	"github.com/autobrr/harbrr/internal/indexer/native/myanonamouse"
	"github.com/autobrr/harbrr/internal/indexer/native/newznab"
	"github.com/autobrr/harbrr/internal/indexer/native/nzbindex"
	"github.com/autobrr/harbrr/internal/indexer/native/passthepopcorn"
	"github.com/autobrr/harbrr/internal/indexer/native/torrentday"
	"github.com/autobrr/harbrr/internal/indexer/native/torznab"
)

// All builds the native-family catalog keyed by definition id, aggregating every
// driver package's exported Families(). This is the moved body of the registry
// package's former nativeFamilies() — same map, same entries.
func All() map[string]native.Family {
	m := make(map[string]native.Family)
	for _, fams := range [][]native.Family{
		animebytes.Families(),
		avistaz.Families(),
		beyondhd.Families(),
		broadcastthenet.Families(),
		filelist.Families(),
		myanonamouse.Families(),
		iptorrents.Families(),
		gazelle.Families(),
		gazellegames.Families(),
		hdbits.Families(),
		newznab.Families(),
		nzbindex.Families(),
		passthepopcorn.Families(),
		torrentday.Families(),
		torznab.Families(),
	} {
		for _, f := range fams {
			m[f.Definition.ID] = f
		}
	}
	return m
}
