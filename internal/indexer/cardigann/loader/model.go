package loader

import (
	"fmt"

	yaml "go.yaml.in/yaml/v3"
)

// Definition is the typed Cardigann definition model. It mirrors the
// SchemaRoot object in vendor/schema.json (JSON-Schema draft 2019-09).
// Every field carries a YAML tag matching the on-disk key. Validation
// against the schema happens before decoding into this struct, so the
// struct itself stays a faithful, lossless representation.
type Definition struct {
	ID              string          `yaml:"id"`
	Replaces        []string        `yaml:"replaces,omitempty"`
	Name            string          `yaml:"name"`
	Description     string          `yaml:"description"`
	Language        string          `yaml:"language"`
	Type            string          `yaml:"type"`
	Encoding        string          `yaml:"encoding"`
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
}

// Caps mirrors the Caps definition. Exactly one of Categories or
// CategoryMappings is present (schema oneOf), but both are modelled so the
// loader stays lossless.
type Caps struct {
	Categories        map[string]string `yaml:"categories,omitempty"`
	CategoryMappings  []CategoryMapping `yaml:"categorymappings,omitempty"`
	Modes             Modes             `yaml:"modes"`
	AllowRawSearch    *bool             `yaml:"allowrawsearch,omitempty"`
	AllowTVSearchIMDB *bool             `yaml:"allowtvsearchimdb,omitempty"`
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
	Selector  string            `yaml:"selector,omitempty"`
	Attribute string            `yaml:"attribute,omitempty"`
	Optional  *bool             `yaml:"optional,omitempty"`
	Default   *Scalar           `yaml:"default,omitempty"`
	Case      map[string]Scalar `yaml:"case,omitempty"`
	Remove    string            `yaml:"remove,omitempty"`
	Text      *Scalar           `yaml:"text,omitempty"`
	Filters   []FilterBlock     `yaml:"filters,omitempty"`
}

// Search mirrors Search. Exactly one of Path or Paths is present (schema
// oneOf); both are modelled to stay lossless.
type Search struct {
	Path                 string              `yaml:"path,omitempty"`
	Paths                []SearchPathBlock   `yaml:"paths,omitempty"`
	AllowEmptyInputs     *bool               `yaml:"allowEmptyInputs,omitempty"`
	Inputs               map[string]Scalar   `yaml:"inputs,omitempty"`
	Headers              map[string][]string `yaml:"headers,omitempty"`
	KeywordsFilters      []FilterBlock       `yaml:"keywordsfilters,omitempty"`
	Error                []ErrorBlock        `yaml:"error,omitempty"`
	PreprocessingFilters []FilterBlock       `yaml:"preprocessingfilters,omitempty"`
	Rows                 RowsBlock           `yaml:"rows"`
	Fields               FieldsBlock         `yaml:"fields"`
}

// SearchPathBlock mirrors SearchPathBlock.
type SearchPathBlock struct {
	Path           string            `yaml:"path"`
	Method         string            `yaml:"method,omitempty"`
	FollowRedirect *bool             `yaml:"followredirect,omitempty"`
	Categories     []Scalar          `yaml:"categories,omitempty"`
	Inputs         map[string]Scalar `yaml:"inputs,omitempty"`
	InheritInputs  *bool             `yaml:"inheritinputs,omitempty"`
	QuerySeparator string            `yaml:"queryseparator,omitempty"`
	Response       *ResponseBlock    `yaml:"response,omitempty"`
}

// ResponseBlock mirrors ResponseBlock.
type ResponseBlock struct {
	Type             string `yaml:"type"`
	NoResultsMessage string `yaml:"noResultsMessage,omitempty"`
}

// RowsBlock mirrors RowsBlock. It is a SelectorBlock-shaped block with extra
// row-specific fields and RowFilterBlock filters.
type RowsBlock struct {
	After                           *int              `yaml:"after,omitempty"`
	DateHeaders                     *SelectorBlock    `yaml:"dateheaders,omitempty"`
	Selector                        string            `yaml:"selector,omitempty"`
	Attribute                       string            `yaml:"attribute,omitempty"`
	Optional                        *bool             `yaml:"optional,omitempty"`
	Multiple                        *bool             `yaml:"multiple,omitempty"`
	MissingAttributeEqualsNoResults *bool             `yaml:"missingAttributeEqualsNoResults,omitempty"`
	Case                            map[string]string `yaml:"case,omitempty"`
	Remove                          string            `yaml:"remove,omitempty"`
	Text                            *Scalar           `yaml:"text,omitempty"`
	Filters                         []RowFilterBlock  `yaml:"filters,omitempty"`
	Count                           *SelectorBlock    `yaml:"count,omitempty"`
}

// FieldsBlock mirrors FieldsBlock: dynamic keys (title, category, _custom,
// "title|append", ...) each mapping to a SelectorBlock.
type FieldsBlock map[string]SelectorBlock

// DownloadBlock mirrors DownloadBlock.
type DownloadBlock struct {
	Method    string              `yaml:"method,omitempty"`
	Before    *BeforeBlock        `yaml:"before,omitempty"`
	Selectors []SelectorField     `yaml:"selectors,omitempty"`
	InfoHash  *InfoHashBlock      `yaml:"infohash,omitempty"`
	Headers   map[string][]string `yaml:"headers,omitempty"`
}

// BeforeBlock mirrors BeforeBlock.
type BeforeBlock struct {
	Path           string            `yaml:"path,omitempty"`
	PathSelector   *SelectorField    `yaml:"pathselector,omitempty"`
	Method         string            `yaml:"method,omitempty"`
	Inputs         map[string]Scalar `yaml:"inputs,omitempty"`
	QuerySeparator string            `yaml:"queryseparator,omitempty"`
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
