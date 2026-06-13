package torznab

import (
	"encoding/xml"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
)

// serverTitle is the <server title> harbrr advertises. Jackett emits "Jackett";
// harbrr emits its own identity. The field is cosmetic — Sonarr/Radarr ignore
// it — so this is a deliberate divergence (testdata/README.md), not a parity
// concern.
const serverTitle = "harbrr"

// Cardigann never customizes the Torznab limits, so harbrr emits Jackett's
// fixed defaults (TorznabCapabilities ctor: LimitsDefault = LimitsMax = 100).
// The limit also bounds the served result page (see the request handler).
const (
	limitsDefault = 100
	limitsMax     = 100
)

// capsDocument is the root <caps> element. Field order is the emitted element
// order: server, limits, searching, categories (mirrors
// TorznabCapabilities.GetXDocument).
type capsDocument struct {
	XMLName    xml.Name       `xml:"caps"`
	Server     capsServer     `xml:"server"`
	Limits     capsLimits     `xml:"limits"`
	Searching  capsSearching  `xml:"searching"`
	Categories []capsCategory `xml:"categories>category"`
}

type capsServer struct {
	Title string `xml:"title,attr"`
}

// capsLimits emits default then max (Jackett's attribute order).
type capsLimits struct {
	Default int `xml:"default,attr"`
	Max     int `xml:"max,attr"`
}

// capsSearching holds the six mode elements in Jackett's fixed order.
type capsSearching struct {
	Modes []capsMode
}

// capsMode is one <searching> child (search, tv-search, ...). The XMLName is set
// per element from the mode's xmlElem so a single slice renders heterogeneous
// element names in order. Attribute order: available, supportedParams,
// searchEngine.
type capsMode struct {
	XMLName         xml.Name
	Available       string `xml:"available,attr"`
	SupportedParams string `xml:"supportedParams,attr"`
	SearchEngine    string `xml:"searchEngine,attr,omitempty"`
}

// MarshalCaps renders the Torznab capabilities document (t=caps) for an
// indexer's capabilities. Caps/category correctness is the Phase 3 gate: every
// search mode is advertised (undeclared modes as available="no"
// supportedParams="q"), supported params are re-derived in Jackett's canonical
// order (with the AllowTVSearchIMDB imdbid gate), AllowRawSearch adds
// searchEngine="raw" to every mode, and the category tree is rebuilt + sorted to
// match Jackett. caps must be non-nil (the engine always produces one).
func MarshalCaps(caps *mapper.Capabilities) ([]byte, error) {
	doc := capsDocument{
		Server:     capsServer{Title: serverTitle},
		Limits:     capsLimits{Default: limitsDefault, Max: limitsMax},
		Searching:  buildSearching(caps),
		Categories: buildCategoryTree(caps.Categories),
	}
	return marshalDocument("caps", doc)
}

// buildSearching produces the six mode elements. Every mode is always emitted;
// availability and supported params come from the definition's declared modes.
func buildSearching(caps *mapper.Capabilities) capsSearching {
	modes := make([]capsMode, 0, len(searchModes))
	for _, m := range searchModes {
		modes = append(modes, capsMode{
			XMLName:         xml.Name{Local: m.xmlElem},
			Available:       availableAttr(m.available(caps)),
			SupportedParams: m.supportedParams(caps),
			SearchEngine:    rawSearchEngine(caps.AllowRawSearch),
		})
	}
	return capsSearching{Modes: modes}
}

// availableAttr renders the Jackett yes/no available attribute.
func availableAttr(ok bool) string {
	if ok {
		return "yes"
	}
	return "no"
}

// rawSearchEngine returns the searchEngine attribute value when the indexer
// supports raw search ("" omits the attribute via omitempty).
func rawSearchEngine(allowRaw bool) string {
	if allowRaw {
		return "raw"
	}
	return ""
}
