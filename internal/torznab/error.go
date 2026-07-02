package torznab

import "encoding/xml"

// errorDocument is the Torznab/Newznab <error code=".." description=".."> response
// (Jackett's CreateErrorXML).
type errorDocument struct {
	XMLName     xml.Name `xml:"error"`
	Code        int      `xml:"code,attr"`
	Description string   `xml:"description,attr"`
}

// MarshalError renders a Torznab error document. description MUST be a fixed,
// secret-free string — never a raw engine error, since wrapped errors can embed
// request URLs that carry passkeys. Marshaling an all-scalar document cannot
// fail; the fallback exists only so the caller always receives valid XML.
func MarshalError(code int, description string) []byte {
	out, err := marshalDocument("error", errorDocument{Code: code, Description: description})
	if err != nil {
		return []byte(xml.Header + `<error code="900" description="internal error"></error>` + "\n")
	}
	return out
}
