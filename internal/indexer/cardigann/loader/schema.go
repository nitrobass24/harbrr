package loader

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/autobrr/harbrr/internal/indexer/definitions"
)

// schemaResourceURL is the in-memory URL under which the embedded schema is
// registered with the jsonschema compiler. It is never fetched over the
// network; the compiler resolves it from the added resource.
const schemaResourceURL = "mem://harbrr/cardigann-schema.json"

// schemaPath is the path of the authoritative JSON-Schema inside the embedded
// vendored definitions tree.
const schemaPath = "vendor/schema.json"

var (
	compiledSchema *jsonschema.Schema
	compileOnce    sync.Once
	errCompile     error
)

// schema lazily compiles the embedded Cardigann JSON-Schema exactly once and
// returns the compiled validator. Compilation is deferred (no init() panic);
// any failure is captured and returned to the caller.
func schema() (*jsonschema.Schema, error) {
	compileOnce.Do(func() {
		compiledSchema, errCompile = compileSchema()
	})
	if errCompile != nil {
		return nil, errCompile
	}
	return compiledSchema, nil
}

func compileSchema() (*jsonschema.Schema, error) {
	raw, err := definitions.Vendored.ReadFile(schemaPath)
	if err != nil {
		return nil, fmt.Errorf("loader: reading embedded %s: %w", schemaPath, err)
	}

	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("loader: parsing embedded %s: %w", schemaPath, err)
	}

	c := jsonschema.NewCompiler()
	if err := c.AddResource(schemaResourceURL, doc); err != nil {
		return nil, fmt.Errorf("loader: registering schema resource: %w", err)
	}

	sch, err := c.Compile(schemaResourceURL)
	if err != nil {
		return nil, fmt.Errorf("loader: compiling Cardigann schema: %w", err)
	}
	return sch, nil
}

// toJSONValue converts a YAML-decoded generic value into the value contract
// the jsonschema validator expects (map[string]any, []any, scalars). YAML
// mappings with non-string keys (e.g. numeric option/case keys) decode as
// map[any]any; this stringifies those keys recursively so validation matches
// how the same document is treated once keyed by string in the typed model.
func toJSONValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = toJSONValue(val)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[fmt.Sprintf("%v", k)] = toJSONValue(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = toJSONValue(val)
		}
		return out
	default:
		return v
	}
}

// validate checks a YAML-decoded generic document against the Cardigann
// schema. On failure it returns a readable error listing the schema paths that
// failed. The supplied document must use jsonschema's value contract
// (map[string]any, []any, string, float64/int, bool, nil) — Parse decodes
// YAML into exactly that shape.
//
// Crucially, the error reports only the failing instance *locations* (schema
// paths), never the failing instance *values*. jsonschema/v6's default
// Error() echoes the offending value (e.g. for a `pattern` mismatch), and
// Cardigann definitions routinely carry passkeys/cookies in those values, so a
// raw validator error could leak a secret into logs or a SkipEntry.Reason.
func validate(doc any) error {
	sch, err := schema()
	if err != nil {
		return err
	}
	verr := sch.Validate(doc)
	if verr == nil {
		return nil
	}
	var ve *jsonschema.ValidationError
	if !errors.As(verr, &ve) {
		// Non-validation error (e.g. unsupported instance type). It does not
		// echo instance values, so it is safe to surface as-is.
		return fmt.Errorf("schema validation failed: %w", verr)
	}
	return fmt.Errorf("schema validation failed at: %s", strings.Join(failingLocations(ve), ", "))
}

// failingLocations walks a jsonschema ValidationError tree and returns the
// sorted, de-duplicated set of instance locations (as JSON-pointer-ish paths)
// that failed. It deliberately drops the ErrorKind, which embeds the offending
// instance value and could carry a secret.
func failingLocations(ve *jsonschema.ValidationError) []string {
	seen := map[string]struct{}{}
	collectLocations(ve, seen)

	locs := make([]string, 0, len(seen))
	for loc := range seen {
		locs = append(locs, loc)
	}
	sort.Strings(locs)
	return locs
}

func collectLocations(ve *jsonschema.ValidationError, seen map[string]struct{}) {
	if len(ve.Causes) == 0 {
		seen[instanceLocation(ve.InstanceLocation)] = struct{}{}
		return
	}
	for _, cause := range ve.Causes {
		collectLocations(cause, seen)
	}
}

func instanceLocation(parts []string) string {
	if len(parts) == 0 {
		return "(root)"
	}
	return "/" + strings.Join(parts, "/")
}
