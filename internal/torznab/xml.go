package torznab

import (
	"bytes"
	"encoding/xml"
	"fmt"
)

// xmlIndent is the per-level indent harbrr uses for its served XML. harbrr
// emits its OWN canonical, deterministic XML (goldens byte-compare harbrr
// output); parity with Jackett is structural — same elements, attributes,
// values and nesting that Sonarr/Radarr parse — not byte-identity with
// Jackett's AngleSharp/XDocument whitespace.
const xmlIndent = "  "

// marshalDocument renders v as a complete XML document: the canonical
// declaration, a newline, then the indented body. encoding/xml never emits the
// declaration itself, and renders an attribute-only element as <e></e> rather
// than <e/>; both are well-formed and parse identically for *arr consumers
// (recorded as a deliberate divergence in testdata/README.md).
func marshalDocument(root string, v any) ([]byte, error) {
	body, err := xml.MarshalIndent(v, "", xmlIndent)
	if err != nil {
		return nil, fmt.Errorf("torznab: marshaling %s document: %w", root, err)
	}
	var buf bytes.Buffer
	buf.Grow(len(xml.Header) + len(body) + 1)
	buf.WriteString(xml.Header) // <?xml version="1.0" encoding="UTF-8"?>\n
	buf.Write(body)
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}
