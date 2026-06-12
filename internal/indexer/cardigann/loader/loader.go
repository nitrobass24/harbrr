package loader

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	yaml "go.yaml.in/yaml/v3"

	"github.com/autobrr/seekbrr/internal/indexer/definitions"
)

// vendorDir is the subdirectory of the embedded definitions FS that holds the
// vendored Jackett snapshot.
const vendorDir = "vendor"

// ErrNotFound is returned by Load when no definition with the requested id
// exists in either the drop-in directory or the vendored snapshot.
var ErrNotFound = errors.New("definition not found")

// Parse decodes a single Cardigann definition from its YAML bytes. It
// schema-validates the document before decoding into the typed model, so an
// invalid definition fails fast with a readable, secret-free error.
func Parse(data []byte) (*Definition, error) {
	var generic any
	if err := unmarshalYAML(data, &generic); err != nil {
		return nil, fmt.Errorf("parsing definition YAML: %w", err)
	}

	if err := validate(toJSONValue(generic)); err != nil {
		return nil, err
	}

	var def Definition
	if err := unmarshalYAML(data, &def); err != nil {
		return nil, fmt.Errorf("decoding definition into typed model: %w", err)
	}
	return &def, nil
}

// unmarshalYAML decodes a definition, absorbing one YamlDotNet compatibility
// difference: Jackett's .NET YAML parser resolves the escape sequence "\/" in
// double-quoted scalars to "/", whereas go-yaml (YAML 1.2) rejects it as an
// unknown escape. Definitions written against Jackett rely on that leniency
// (e.g. regex/selector args like "torrent-category-(\\d+)\/"). When and only
// when go-yaml trips on that escape, we retry with "\/" rewritten to "/",
// reproducing Jackett's result. The difference is absorbed here in the engine,
// never by hand-editing the vendored def.
func unmarshalYAML(data []byte, out any) error {
	err := yaml.Unmarshal(data, out)
	if err == nil {
		return nil
	}
	if !strings.Contains(err.Error(), "found unknown escape character") {
		return fmt.Errorf("yaml unmarshal: %w", err)
	}
	if retryErr := yaml.Unmarshal(rewriteSlashEscapes(data), out); retryErr != nil {
		return fmt.Errorf("yaml unmarshal (after \\/ rewrite): %w", retryErr)
	}
	return nil
}

// rewriteSlashEscapes replaces the YAML "\/" escape with a literal "/",
// matching YamlDotNet semantics. It is only applied as a fallback after
// go-yaml rejects the escape.
//
// It is backslash-run aware: an escaping backslash is one that terminates an
// odd-length run of backslashes. So "\/" (lone backslash, escapes the slash)
// becomes "/", while "\\/" (escaped backslash, then a literal slash) is left
// intact. A naive string replace would corrupt the latter.
func rewriteSlashEscapes(data []byte) []byte {
	out := make([]byte, 0, len(data))
	backslashRun := 0
	for i := 0; i < len(data); i++ {
		c := data[i]
		if c == '\\' {
			backslashRun++
			out = append(out, c)
			continue
		}
		if c == '/' && backslashRun%2 == 1 {
			// The last backslash escapes this slash; drop it.
			out = out[:len(out)-1]
		}
		out = append(out, c)
		backslashRun = 0
	}
	return out
}

// Loader resolves and parses Cardigann definitions, applying drop-in
// precedence over the embedded vendored snapshot.
type Loader struct {
	dropinDir string
}

// New constructs a Loader. dropinDir is the on-disk directory of user override
// definitions; an empty string disables drop-ins (vendored-only).
func New(dropinDir string) *Loader {
	return &Loader{dropinDir: dropinDir}
}

// Load resolves a definition by id with precedence dropin > vendored. It first
// tries <dropinDir>/<id>.yml on disk, then vendor/<id>.yml in the embedded
// snapshot. If neither exists it returns an error wrapping ErrNotFound.
func (l *Loader) Load(id string) (*Definition, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}

	if l.dropinDir != "" {
		data, ok, err := l.readDropin(id)
		if err != nil {
			return nil, err
		}
		if ok {
			def, err := Parse(data)
			if err != nil {
				return nil, fmt.Errorf("loading drop-in definition %q: %w", id, err)
			}
			return def, nil
		}
	}

	data, err := definitions.Vendored.ReadFile(vendorPath(id))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%q: %w", id, ErrNotFound)
		}
		return nil, fmt.Errorf("reading vendored definition %q: %w", id, err)
	}

	def, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("loading vendored definition %q: %w", id, err)
	}
	return def, nil
}

// SkipEntry records a definition that could not be loaded, so failures are
// surfaced explicitly rather than silently dropped.
type SkipEntry struct {
	ID     string
	Reason string
}

// LoadAll loads every vendored definition plus any drop-in override, applying
// dropin > vendored precedence by id. Per-definition parse/validation failures
// are collected into skipped (visible, never silent); err is reserved for
// catastrophic filesystem errors that prevent enumeration.
func (l *Loader) LoadAll() (defs []*Definition, skipped []SkipEntry, err error) {
	// A schema-compile failure is systemic, not per-definition: it would
	// otherwise surface identically as a "skip" for every id, masking the real
	// root cause. Probe it once and treat it as a catastrophic error.
	if _, err := schema(); err != nil {
		return nil, nil, fmt.Errorf("compiling Cardigann schema: %w", err)
	}

	ids, err := l.allIDs()
	if err != nil {
		return nil, nil, err
	}

	for _, id := range ids {
		def, loadErr := l.Load(id)
		if loadErr != nil {
			skipped = append(skipped, SkipEntry{ID: id, Reason: loadErr.Error()})
			continue
		}
		defs = append(defs, def)
	}
	return defs, skipped, nil
}

// allIDs returns the sorted union of vendored ids and drop-in ids.
func (l *Loader) allIDs() ([]string, error) {
	set := map[string]struct{}{}

	entries, err := definitions.Vendored.ReadDir(vendorDir)
	if err != nil {
		return nil, fmt.Errorf("enumerating vendored definitions: %w", err)
	}
	for _, e := range entries {
		if id, ok := definitionID(e.Name(), e.IsDir()); ok {
			set[id] = struct{}{}
		}
	}

	if err := l.collectDropinIDs(set); err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

func (l *Loader) collectDropinIDs(set map[string]struct{}) error {
	if l.dropinDir == "" {
		return nil
	}
	entries, err := os.ReadDir(l.dropinDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("enumerating drop-in definitions: %w", err)
	}
	for _, e := range entries {
		if id, ok := definitionID(e.Name(), e.IsDir()); ok {
			set[id] = struct{}{}
		}
	}
	return nil
}

// validateID rejects ids that are not a bare definition name. Without this an
// id like "../../etc/foo" would escape dropinDir via filepath.Join and turn
// Load into an arbitrary-file-read primitive. The embedded vendored FS already
// rejects traversal, but the on-disk drop-in path does not, so the guard runs
// for every Load. Definition ids are bare filenames (no separators), so this
// rejects nothing legitimate.
func validateID(id string) error {
	if id == "" || id == "." || id == ".." ||
		id != filepath.Base(id) || strings.ContainsAny(id, `/\`) {
		return fmt.Errorf("invalid definition id %q: must be a bare name without path separators", id)
	}
	return nil
}

// readDropin reads <dropinDir>/<id>.yml. ok is false (with nil error) when the
// file does not exist. The id is validated by Load (validateID) before this is
// reached, so the join cannot escape dropinDir.
func (l *Loader) readDropin(id string) (data []byte, ok bool, err error) {
	path := filepath.Join(l.dropinDir, id+".yml")
	data, err = os.ReadFile(path) //nolint:gosec // id is validated by validateID (no separators / ..) before this join
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("reading drop-in definition %q: %w", id, err)
	}
	return data, true, nil
}

// definitionID extracts a definition id from a filename, returning ok=false for
// directories and non-.yml files (e.g. schema.json, .jackett-ref).
func definitionID(name string, isDir bool) (string, bool) {
	if isDir {
		return "", false
	}
	if !strings.HasSuffix(name, ".yml") {
		return "", false
	}
	return strings.TrimSuffix(name, ".yml"), true
}

func vendorPath(id string) string {
	return vendorDir + "/" + id + ".yml"
}
