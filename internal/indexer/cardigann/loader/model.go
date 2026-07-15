package loader

import (
	"fmt"
	"strings"

	yaml "go.yaml.in/yaml/v3"
)

// Definition is the typed Cardigann definition model. It mirrors the
// SchemaRoot object in vendor/schema.json (JSON-Schema draft 2019-09).
// Every field carries a YAML tag matching the on-disk key. Validation
// against the schema happens before decoding into this struct, so the
// struct itself stays a faithful, lossless representation.
type Definition struct {
	ID          string   `yaml:"id"`
	Replaces    []string `yaml:"replaces,omitempty"`
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Language    string   `yaml:"language"`
	Type        string   `yaml:"type"`
	Encoding    string   `yaml:"encoding"`
	// FollowRedirect (definition-level) gates only Jackett's LOGIN/landing-page
	// redirect follow — never search (search reads the path-level flag alone; both
	// default false independently in Jackett's model). harbrr's login client always
	// follows redirects, a documented superset that subsumes this flag; the search
	// stage honors the path-level flag (see search/redirect.go).
	FollowRedirect  *bool           `yaml:"followredirect,omitempty"`
	TestLinkTorrent *bool           `yaml:"testlinktorrent,omitempty"`
	RequestDelay    *float64        `yaml:"requestDelay,omitempty"`
	Links           []string        `yaml:"links"`
	LegacyLinks     []string        `yaml:"legacylinks,omitempty"`
	Certificates    []string        `yaml:"certificates,omitempty"`
	Caps            Caps            `yaml:"caps"`
	Settings        []SettingsField `yaml:"settings,omitempty"`
	Login           *Login          `yaml:"login,omitempty"`
	Search          Search          `yaml:"search"`
	Download        *DownloadBlock  `yaml:"download,omitempty"`
	// Protocol is the acquisition protocol: "torrent" (default) or "usenet".
	// Vendored Cardigann defs and every native family except Newznab leave this
	// empty → torrent. The Newznab family sets it "usenet". Not a YAML field on
	// vendored defs (they are torrent-only); set in Go-built native definitions.
	Protocol string `yaml:"protocol,omitempty"`
}

// Acquisition protocols. Callers must read EffectiveProtocol, never the raw
// Definition.Protocol field, so the empty-default rule lives in one place.
const (
	ProtocolTorrent = "torrent"
	ProtocolUsenet  = "usenet"
)

// EffectiveProtocol returns the acquisition protocol, defaulting empty → torrent.
func (d *Definition) EffectiveProtocol() string {
	if d.Protocol == ProtocolUsenet {
		return ProtocolUsenet
	}
	return ProtocolTorrent
}

// Caps mirrors the Caps definition. Exactly one of Categories or
// CategoryMappings is present (schema oneOf), but both are modelled so the
// loader stays lossless.
type Caps struct {
	Categories        CategoriesBlock   `yaml:"categories,omitempty"`
	CategoryMappings  []CategoryMapping `yaml:"categorymappings,omitempty"`
	Modes             Modes             `yaml:"modes"`
	AllowRawSearch    *bool             `yaml:"allowrawsearch,omitempty"`
	AllowTVSearchIMDB *bool             `yaml:"allowtvsearchimdb,omitempty"`
}

// CategoryEntry is one (tracker id -> standard category name) pair from the
// caps.categories object form, in definition order.
type CategoryEntry struct {
	TrackerID string
	Name      string
}

// CategoriesBlock is the order-preserving caps.categories object form
// (tracker category id -> standard category name). Jackett deserializes this
// block into a YamlDotNet Dictionary, which preserves YAML document order, and
// appends to _categoryMapping in that order; the order reaches the rendered
// {{ .Categories }} request bytes (mapper querycats -> search request), so a
// plain Go map's randomized iteration would diverge from Jackett and vary
// across restarts. Mirrors InputsBlock/FieldsBlock: keys records source order,
// names the values.
type CategoriesBlock struct {
	keys  []string
	names map[string]string
}

// NewCategoriesBlock builds a CategoriesBlock from ordered entries, preserving
// the given order (first position, last value on a duplicate key). The loader
// builds these via UnmarshalYAML; this constructor is for assembling
// definitions directly (e.g. in tests) without losing order to a Go map
// literal.
func NewCategoriesBlock(entries ...CategoryEntry) CategoriesBlock {
	cb := CategoriesBlock{names: make(map[string]string, len(entries))}
	for _, e := range entries {
		if _, seen := cb.names[e.TrackerID]; !seen {
			cb.keys = append(cb.keys, e.TrackerID)
		}
		cb.names[e.TrackerID] = e.Name
	}
	return cb
}

// UnmarshalYAML decodes a mapping node into an order-preserving
// CategoriesBlock, keeping a duplicate key's FIRST position but LAST value
// (go-yaml map semantics), exactly as InputsBlock and FieldsBlock do.
func (cb *CategoriesBlock) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("categories: expected a mapping, got %s", kindName(node.Kind))
	}
	cb.keys = cb.keys[:0]
	cb.names = make(map[string]string, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i].Value
		var name string
		if err := node.Content[i+1].Decode(&name); err != nil {
			return fmt.Errorf("categories: decoding category %q: %w", key, err)
		}
		if _, seen := cb.names[key]; !seen {
			cb.keys = append(cb.keys, key)
		}
		cb.names[key] = name
	}
	return nil
}

// Ordered returns the category entries in definition (YAML) order.
func (cb CategoriesBlock) Ordered() []CategoryEntry {
	out := make([]CategoryEntry, 0, len(cb.keys))
	for _, k := range cb.keys {
		out = append(out, CategoryEntry{TrackerID: k, Name: cb.names[k]})
	}
	return out
}

// CategoryMapping mirrors CategoryMapping. The id is a scalar union
// (integer|string) normalized to its string form.
type CategoryMapping struct {
	ID      Scalar `yaml:"id"`
	Cat     string `yaml:"cat"`
	Desc    string `yaml:"desc,omitempty"`
	Default *bool  `yaml:"default,omitempty"`
}

// Modes mirrors Modes.
type Modes struct {
	Search      []string `yaml:"search"`
	TVSearch    []string `yaml:"tv-search,omitempty"`
	MovieSearch []string `yaml:"movie-search,omitempty"`
	MusicSearch []string `yaml:"music-search,omitempty"`
	BookSearch  []string `yaml:"book-search,omitempty"`
}

// SettingsField mirrors SettingsField. The default is a scalar union
// (string|integer|boolean) normalized to its string form.
type SettingsField struct {
	Name     string            `yaml:"name"`
	Label    string            `yaml:"label,omitempty"`
	Type     string            `yaml:"type"`
	Default  *Scalar           `yaml:"default,omitempty"`
	Options  map[string]string `yaml:"options,omitempty"`
	Defaults []string          `yaml:"defaults,omitempty"`
}

// secretNameTokens are substrings that mark a text-typed setting as a credential
// to encrypt at rest and redact (docs/security.md). The set is derived from an
// audit of the vendored corpus (see secret_classifier_test.go) and kept tight to
// avoid redacting benign fields: trackers spell credentials many ways
// (cookie/apikey/passkey/2facode/staffpass/…), but a checkbox like "usetoken" is
// excluded by the type rules below, not by trimming this list.
var secretNameTokens = []string{
	"cookie", "passkey", "apikey", "api_key", "authkey", "auth_key",
	"rsskey", "rss_key", "torrent_pass", "passid", "pass", "passphrase",
	"secret", "2fa", "otp", "token", "downloadtoken", "pin",
}

// IsSecret reports whether this setting holds a credential that must be encrypted
// at rest and redacted in API responses. It is type-aware, which is what makes
// the classification correct against the real corpus:
//
//   - info* types are display-only help text with no stored value → never secret;
//   - password is always a secret;
//   - checkbox / select / multi-select are toggles/enums → never secret (this is
//     what excludes name-colliding toggles like "usetoken"/"use_fl_tokens");
//   - otherwise (text) it is secret when its name matches a credential token,
//     catching the many text-typed secrets (cookie, apikey, 2facode, …).
func (s SettingsField) IsSecret() bool {
	switch {
	case strings.HasPrefix(s.Type, "info"):
		return false
	case s.Type == "password":
		return true
	case s.Type == "checkbox", s.Type == "select", s.Type == "multi-select":
		return false
	}
	return nameLooksSecret(s.Name)
}

// nameLooksSecret reports whether a setting name contains a credential token.
func nameLooksSecret(name string) bool {
	lower := strings.ToLower(name)
	for _, tok := range secretNameTokens {
		if strings.Contains(lower, tok) {
			return true
		}
	}
	return false
}

// SecretValues returns the non-empty, trimmed config VALUES of every setting the
// authoritative IsSecret classifier marks a credential, so a server-echoed secret
// can be value-scrubbed out of a diagnostic string (apphttp.ScrubValues). loader
// owns IsSecret, so this is the one place both the login stage and a native driver
// derive the scrub set from — without either importing the other.
//
// Deriving the set from IsSecret over the definition's OWN settings — rather than a
// hardcoded key list — is what makes it correct: it catches every credential the
// loader encrypts at rest (password/cookie/apikey/passkey/rsskey/authkey/2fa/otp/
// token/pin, and a def's differently-named field such as Bittorrentfiles' `pass`,
// type: password) and never scrubs a non-secret (a definition's `username` stays
// intact, so a legitimate "no such user 'dave'" survives).
//
// A value is trimmed before comparison: several drivers submit a trimmed config
// value (leading/trailing whitespace never reaches the tracker), so the raw,
// untrimmed value would not match a server's echo of it. The empty-guard (checked
// after trimming) drops an unset/blank credential so the caller's ReplaceAll is
// never handed "" (which would splice the placeholder between every rune).
//
// This is intentionally scoped to Settings, not the broader at-rest classifier
// (which also flags proxy_url/flaresolverr_url and an undeclared-name fallback):
// only a value the definition actually SENT to the tracker can be echoed back, and
// every tracker-submitted secret is a Settings field.
func SecretValues(settings []SettingsField, config map[string]string) []string {
	var vals []string
	for i := range settings {
		if !settings[i].IsSecret() {
			continue
		}
		if v := strings.TrimSpace(config[settings[i].Name]); v != "" {
			vals = append(vals, v)
		}
	}
	return vals
}

// Login mirrors Login.
type Login struct {
	Method          string                   `yaml:"method,omitempty"`
	Cookies         []string                 `yaml:"cookies,omitempty"`
	Path            string                   `yaml:"path,omitempty"`
	SubmitPath      string                   `yaml:"submitpath,omitempty"`
	Form            string                   `yaml:"form,omitempty"`
	Captcha         *CaptchaBlock            `yaml:"captcha,omitempty"`
	Inputs          map[string]Scalar        `yaml:"inputs,omitempty"`
	Selectors       *bool                    `yaml:"selectors,omitempty"`
	SelectorInputs  map[string]SelectorBlock `yaml:"selectorinputs,omitempty"`
	GetSelectorInps map[string]SelectorBlock `yaml:"getselectorinputs,omitempty"`
	Error           []ErrorBlock             `yaml:"error,omitempty"`
	Test            *PageTestBlock           `yaml:"test,omitempty"`
	Headers         map[string][]string      `yaml:"headers,omitempty"`
}

// PageTestBlock mirrors PageTestBlock.
type PageTestBlock struct {
	Path     string `yaml:"path"`
	Selector string `yaml:"selector"`
}

// CaptchaBlock mirrors CaptchaBlock.
type CaptchaBlock struct {
	Type     string `yaml:"type"`
	Selector string `yaml:"selector"`
	Input    string `yaml:"input"`
}

// ErrorBlock mirrors ErrorBlock.
type ErrorBlock struct {
	Path     string         `yaml:"path,omitempty"`
	Selector string         `yaml:"selector"`
	Message  *SelectorBlock `yaml:"message,omitempty"`
}

// SelectorBlock mirrors SelectorBlock. Text and Default are scalar unions
// (string|number) normalized to their string form.
type SelectorBlock struct {
	Selector  string        `yaml:"selector,omitempty"`
	Attribute string        `yaml:"attribute,omitempty"`
	Optional  *bool         `yaml:"optional,omitempty"`
	Default   *Scalar       `yaml:"default,omitempty"`
	Case      CaseBlock     `yaml:"case,omitempty"`
	Remove    string        `yaml:"remove,omitempty"`
	Text      *Scalar       `yaml:"text,omitempty"`
	Filters   []FilterBlock `yaml:"filters,omitempty"`
}

// CaseEntry is one (selector-key -> value) arm of a case switch, in definition
// order. Value is a scalar union normalized to its string form.
type CaseEntry struct {
	Key   string
	Value Scalar
}

// CaseBlock is the order-preserving `case:` switch of a SelectorBlock/RowsBlock.
// Jackett deserializes Selector.Case into a YamlDotNet Dictionary (document
// order) and, in handleSelector/handleJsonSelector, iterates it in that order,
// breaking on the FIRST matching arm — with "*" tested inline as an ordinary key
// (selection.Matches("*") for HTML, jcase.Key == "*" for JSON), so "*" is
// POSITIONAL, not a deferred default. A plain Go map would randomize iteration
// and, for a cell that satisfies two arms, could return a different arm than
// Jackett's first-defined one. Mirrors FieldsBlock/InputsBlock: keys records
// source order, values the scalars.
type CaseBlock struct {
	keys   []string
	values map[string]Scalar
}

// NewCaseBlock builds a CaseBlock from ordered arms, preserving the given order
// (first position, last value on a duplicate key). The loader builds these via
// UnmarshalYAML; this constructor is for assembling blocks directly (e.g. in
// tests) without losing order to a Go map literal.
func NewCaseBlock(entries ...CaseEntry) CaseBlock {
	cb := CaseBlock{values: make(map[string]Scalar, len(entries))}
	for _, e := range entries {
		if _, seen := cb.values[e.Key]; !seen {
			cb.keys = append(cb.keys, e.Key)
		}
		cb.values[e.Key] = e.Value
	}
	return cb
}

// UnmarshalYAML decodes a mapping node into an order-preserving CaseBlock,
// keeping a duplicate key's FIRST position but LAST value (go-yaml map
// semantics), exactly as FieldsBlock/InputsBlock do.
func (cb *CaseBlock) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("case: expected a mapping, got %s", kindName(node.Kind))
	}
	cb.keys = cb.keys[:0]
	cb.values = make(map[string]Scalar, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i].Value
		var v Scalar
		if err := node.Content[i+1].Decode(&v); err != nil {
			return fmt.Errorf("case: decoding arm %q: %w", key, err)
		}
		if _, seen := cb.values[key]; !seen {
			cb.keys = append(cb.keys, key)
		}
		cb.values[key] = v
	}
	return nil
}

// Ordered returns the case arms in definition (YAML) order.
func (cb CaseBlock) Ordered() []CaseEntry {
	out := make([]CaseEntry, 0, len(cb.keys))
	for _, k := range cb.keys {
		out = append(out, CaseEntry{Key: k, Value: cb.values[k]})
	}
	return out
}

// Len reports the number of case arms.
func (cb CaseBlock) Len() int { return len(cb.keys) }

// Search mirrors Search. Exactly one of Path or Paths is present (schema
// oneOf); both are modelled to stay lossless.
type Search struct {
	Path                 string              `yaml:"path,omitempty"`
	Paths                []SearchPathBlock   `yaml:"paths,omitempty"`
	AllowEmptyInputs     *bool               `yaml:"allowEmptyInputs,omitempty"`
	Inputs               InputsBlock         `yaml:"inputs,omitempty"`
	Headers              map[string][]string `yaml:"headers,omitempty"`
	KeywordsFilters      []FilterBlock       `yaml:"keywordsfilters,omitempty"`
	Error                []ErrorBlock        `yaml:"error,omitempty"`
	PreprocessingFilters []FilterBlock       `yaml:"preprocessingfilters,omitempty"`
	Rows                 RowsBlock           `yaml:"rows"`
	Fields               FieldsBlock         `yaml:"fields"`
}

// SearchPathBlock mirrors SearchPathBlock.
type SearchPathBlock struct {
	Path   string `yaml:"path"`
	Method string `yaml:"method,omitempty"`
	// FollowRedirect opts this path's search response into a manual redirect
	// follow (Jackett FollowIfRedirect: ≤5 GET hops). Unset/false means a 3xx is
	// NOT followed — it is a logged-out signal (defs with login) or parsed as-is,
	// matching Jackett's no-auto-follow WebClient. There is no fallback to the
	// definition-level flag (see the Definition.FollowRedirect note).
	FollowRedirect *bool          `yaml:"followredirect,omitempty"`
	Categories     []Scalar       `yaml:"categories,omitempty"`
	Inputs         InputsBlock    `yaml:"inputs,omitempty"`
	InheritInputs  *bool          `yaml:"inheritinputs,omitempty"`
	QuerySeparator string         `yaml:"queryseparator,omitempty"`
	Response       *ResponseBlock `yaml:"response,omitempty"`
}

// ResponseBlock mirrors ResponseBlock.
type ResponseBlock struct {
	Type string `yaml:"type"`
	// NoResultsMessage is a pointer because Jackett distinguishes ABSENT
	// (null — no check) from PRESENT-EMPTY (`noResultsMessage: ""` — an
	// exactly-empty body means zero results): a non-empty message matches the
	// raw body by substring. See search.noResultsMatch.
	NoResultsMessage *string `yaml:"noResultsMessage,omitempty"`
}

// RowsBlock mirrors RowsBlock. It is a SelectorBlock-shaped block with extra
// row-specific fields and RowFilterBlock filters.
type RowsBlock struct {
	After                           *int             `yaml:"after,omitempty"`
	DateHeaders                     *SelectorBlock   `yaml:"dateheaders,omitempty"`
	Selector                        string           `yaml:"selector,omitempty"`
	Attribute                       string           `yaml:"attribute,omitempty"`
	Optional                        *bool            `yaml:"optional,omitempty"`
	Multiple                        *bool            `yaml:"multiple,omitempty"`
	MissingAttributeEqualsNoResults *bool            `yaml:"missingAttributeEqualsNoResults,omitempty"`
	Case                            CaseBlock        `yaml:"case,omitempty"`
	Remove                          string           `yaml:"remove,omitempty"`
	Text                            *Scalar          `yaml:"text,omitempty"`
	Filters                         []RowFilterBlock `yaml:"filters,omitempty"`
	Count                           *SelectorBlock   `yaml:"count,omitempty"`
}

// FieldEntry is one (key, block) pair from a FieldsBlock, in definition order.
// Key is the raw YAML key, which may carry modifiers ("title|append").
type FieldEntry struct {
	Key   string
	Block SelectorBlock
}

// FieldsBlock mirrors FieldsBlock: dynamic keys (title, category, _custom,
// "title|append", ...) each mapping to a SelectorBlock.
//
// Jackett's ParseFields iterates the fields in DEFINITION ORDER and accumulates
// a per-row Result map as it goes, so a later field's template can read an
// earlier field via {{ .Result.<name> }}. A plain Go map randomizes iteration
// and would break that contract, so FieldsBlock preserves the YAML key order via
// a custom UnmarshalYAML: keys records the order, blocks the values. Read access
// goes through Ordered; schema validation still runs on the generic decode,
// unaffected by this typed shape.
type FieldsBlock struct {
	keys   []string
	blocks map[string]SelectorBlock
}

// UnmarshalYAML decodes a mapping node into an order-preserving FieldsBlock. A
// YAML mapping node stores its entries as a flat [key0, val0, key1, val1, ...]
// Content slice in source order, which is exactly the order Jackett relies on.
// A duplicate key keeps its FIRST position but its LAST value, matching go-yaml's
// last-wins map semantics while leaving the field loop's order stable.
func (fb *FieldsBlock) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("fields: expected a mapping, got %s", kindName(node.Kind))
	}
	fb.keys = fb.keys[:0]
	fb.blocks = make(map[string]SelectorBlock, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i].Value
		var block SelectorBlock
		if err := node.Content[i+1].Decode(&block); err != nil {
			return fmt.Errorf("fields: decoding field %q: %w", key, err)
		}
		if _, seen := fb.blocks[key]; !seen {
			fb.keys = append(fb.keys, key)
		}
		fb.blocks[key] = block
	}
	return nil
}

// Ordered returns the field entries in definition (YAML) order.
func (fb FieldsBlock) Ordered() []FieldEntry {
	out := make([]FieldEntry, 0, len(fb.keys))
	for _, k := range fb.keys {
		out = append(out, FieldEntry{Key: k, Block: fb.blocks[k]})
	}
	return out
}

// InputEntry is one (key, value) search input in definition order.
type InputEntry struct {
	Key   string
	Value Scalar
}

// InputsBlock is an order-preserving search-inputs map. Jackett builds the GET
// query / POST body by iterating Search.Inputs then SearchPath.Inputs in
// DEFINITION ORDER and appending each pair to an ordered collection
// (CardigannIndexer.PerformQuery), so the rendered query reproduces the def's
// key order — a plain Go map would randomize it and diverge from Jackett. This
// mirrors FieldsBlock: keys records source order, values the scalars.
type InputsBlock struct {
	keys   []string
	values map[string]Scalar
}

// NewInputsBlock builds an InputsBlock from ordered entries, preserving the
// given order (first position, last value on a duplicate key). The loader builds
// these via UnmarshalYAML; this constructor is for assembling definitions
// directly (e.g. in tests) without losing order to a Go map literal.
func NewInputsBlock(entries ...InputEntry) InputsBlock {
	ib := InputsBlock{values: make(map[string]Scalar, len(entries))}
	for _, e := range entries {
		if _, seen := ib.values[e.Key]; !seen {
			ib.keys = append(ib.keys, e.Key)
		}
		ib.values[e.Key] = e.Value
	}
	return ib
}

// UnmarshalYAML decodes a mapping node into an order-preserving InputsBlock,
// keeping a duplicate key's FIRST position but LAST value (go-yaml map
// semantics), exactly as FieldsBlock does.
func (ib *InputsBlock) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("inputs: expected a mapping, got %s", kindName(node.Kind))
	}
	ib.keys = ib.keys[:0]
	ib.values = make(map[string]Scalar, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i].Value
		var v Scalar
		if err := node.Content[i+1].Decode(&v); err != nil {
			return fmt.Errorf("inputs: decoding input %q: %w", key, err)
		}
		if _, seen := ib.values[key]; !seen {
			ib.keys = append(ib.keys, key)
		}
		ib.values[key] = v
	}
	return nil
}

// Ordered returns the input entries in definition (YAML) order.
func (ib InputsBlock) Ordered() []InputEntry {
	out := make([]InputEntry, 0, len(ib.keys))
	for _, k := range ib.keys {
		out = append(out, InputEntry{Key: k, Value: ib.values[k]})
	}
	return out
}

// DownloadBlock mirrors DownloadBlock.
type DownloadBlock struct {
	Method    string              `yaml:"method,omitempty"`
	Before    *BeforeBlock        `yaml:"before,omitempty"`
	Selectors []SelectorField     `yaml:"selectors,omitempty"`
	InfoHash  *InfoHashBlock      `yaml:"infohash,omitempty"`
	Headers   map[string][]string `yaml:"headers,omitempty"`
}

// BeforeBlock mirrors BeforeBlock. Inputs is the order-preserving InputsBlock (not
// a plain map): the before request renders them into a GET query / POST body in
// DEFINITION ORDER, so the bytes reproduce Jackett's ordered queryCollection.
type BeforeBlock struct {
	Path           string         `yaml:"path,omitempty"`
	PathSelector   *SelectorField `yaml:"pathselector,omitempty"`
	Method         string         `yaml:"method,omitempty"`
	Inputs         InputsBlock    `yaml:"inputs,omitempty"`
	QuerySeparator string         `yaml:"queryseparator,omitempty"`
}

// InfoHashBlock mirrors InfoHashBlock.
type InfoHashBlock struct {
	Hash              *SelectorField `yaml:"hash,omitempty"`
	Title             *SelectorField `yaml:"title,omitempty"`
	UseBeforeResponse *bool          `yaml:"usebeforeresponse,omitempty"`
}

// SelectorField mirrors SelectorField.
type SelectorField struct {
	Selector          string        `yaml:"selector,omitempty"`
	Attribute         string        `yaml:"attribute,omitempty"`
	UseBeforeResponse *bool         `yaml:"usebeforeresponse,omitempty"`
	Filters           []FilterBlock `yaml:"filters,omitempty"`
}

// FilterBlock mirrors FilterBlock. Args is an array|string|integer union
// normalized to []string.
type FilterBlock struct {
	Name string     `yaml:"name"`
	Args FilterArgs `yaml:"args,omitempty"`
}

// RowFilterBlock mirrors RowFilterBlock (row-level filters: andmatch, strdump).
type RowFilterBlock struct {
	Name string     `yaml:"name"`
	Args FilterArgs `yaml:"args,omitempty"`
}

// Scalar is a oneOf scalar union (string|number|boolean) normalized to its
// string form, mirroring how Jackett's deserializer coerces these values to
// strings. The decoded YAML node may have been an int, float, bool, or
// string; this type captures whichever was present as a canonical string.
type Scalar struct {
	// Value is the canonical string form.
	Value string
	// Set reports whether the field was present with a non-null scalar value.
	// This distinguishes an explicit empty string ("") from an absent field.
	// An explicit YAML null (~/null) leaves Set false, because go-yaml does
	// not invoke UnmarshalYAML for a null node; the schema's scalar unions do
	// not admit null, so such a document is rejected at validation anyway.
	Set bool
}

// String returns the canonical string form of the scalar.
func (s Scalar) String() string { return s.Value }

// UnmarshalYAML decodes a scalar union node into its canonical string form.
func (s *Scalar) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("scalar value: expected a scalar (string, number, or bool), got %s", kindName(node.Kind))
	}
	s.Value = node.Value
	s.Set = true
	return nil
}

// FilterArgs is a oneOf union (array|string|integer) normalized to []string,
// mirroring Jackett's filter-argument coercion. A scalar collapses to a
// single-element slice; an array maps element-by-element to strings.
type FilterArgs []string

// UnmarshalYAML decodes filter args from either a scalar or a sequence into a
// flat []string. A YAML null node never reaches this method (go-yaml leaves
// the receiver untouched), so null decodes to a nil slice, treated as no args.
func (a *FilterArgs) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		*a = FilterArgs{node.Value}
		return nil
	}
	if node.Kind != yaml.SequenceNode {
		return fmt.Errorf("filter args: expected a scalar or array, got %s", kindName(node.Kind))
	}
	out := make(FilterArgs, 0, len(node.Content))
	for _, item := range node.Content {
		if item.Kind != yaml.ScalarNode {
			return fmt.Errorf("filter args: array elements must be scalar, got %s", kindName(item.Kind))
		}
		out = append(out, item.Value)
	}
	*a = out
	return nil
}

func kindName(k yaml.Kind) string {
	switch k {
	case yaml.SequenceNode:
		return "array"
	case yaml.MappingNode:
		return "mapping"
	case yaml.ScalarNode:
		return "scalar"
	case yaml.AliasNode:
		return "alias"
	case yaml.DocumentNode:
		return "document"
	default:
		return "unknown"
	}
}
