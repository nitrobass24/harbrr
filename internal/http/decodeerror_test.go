package http

import (
	"encoding/json"
	"encoding/xml"
	"strings"
	"testing"
)

func TestDecodeErrorDetail(t *testing.T) {
	t.Parallel()

	// A JSON body whose "torrents" field is an array where an object is expected —
	// the canonical PHP "empty array as object" breakage (#63).
	type browse struct {
		Torrents struct {
			ID int `json:"id"`
		} `json:"torrents"`
	}
	var typeMismatch browse
	typeErr := json.Unmarshal([]byte(`{"torrents":[]}`), &typeMismatch)

	var syntaxTarget browse
	jsonSyntaxErr := json.Unmarshal([]byte(`{"torrents":`), &syntaxTarget)

	type xmlDoc struct {
		XMLName xml.Name `xml:"caps"`
	}
	var xmlTarget xmlDoc
	xmlSyntaxErr := xml.Unmarshal([]byte(`<caps><unclosed>`), &xmlTarget)

	tests := []struct {
		name       string
		err        error
		body       []byte
		wantHas    []string
		wantNoLeak []string
	}{
		{
			name:    "json type mismatch names field, types, offset",
			err:     typeErr,
			body:    []byte(`{"torrents":[]}`),
			wantHas: []string{"torrents", "offset"},
		},
		{
			name:    "json syntax error reports offset",
			err:     jsonSyntaxErr,
			body:    []byte(`{"torrents":`),
			wantHas: []string{"invalid JSON", "offset"},
		},
		{
			name:    "xml syntax error reports line",
			err:     xmlSyntaxErr,
			body:    []byte(`<caps><unclosed>`),
			wantHas: []string{"invalid XML", "line"},
		},
		{
			name:    "nil error yields empty detail",
			err:     nil,
			body:    []byte(`whatever`),
			wantHas: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := DecodeErrorDetail(tt.err, tt.body)
			if tt.err == nil && got != "" {
				t.Fatalf("DecodeErrorDetail(nil) = %q, want empty", got)
			}
			for _, want := range tt.wantHas {
				if !strings.Contains(got, want) {
					t.Errorf("detail %q missing %q", got, want)
				}
			}
			for _, leak := range tt.wantNoLeak {
				if strings.Contains(got, leak) {
					t.Errorf("detail %q leaked %q", got, leak)
				}
			}
		})
	}
}

// TestDecodeErrorDetailNonJSONNeverEchoesBody is the always-on leak gate: when the
// body is not the expected structure (e.g. an HTML login wall carrying a passkey,
// an api_key query, and a Cookie), the always-on detail reports only size/shape and
// must echo none of the body's bytes.
func TestDecodeErrorDetailNonJSONNeverEchoesBody(t *testing.T) {
	t.Parallel()

	const passkey = "deadbeefdeadbeefdeadbeefdeadbeef" // bare 32-hex, no key
	body := []byte(`<!DOCTYPE html><html><a href="/dl?apikey=` + passkey +
		`&passkey=` + passkey + `">login</a><!-- Cookie: cf_clearance=` + passkey + ` --></html>`)

	var target struct {
		Torrents []string `json:"torrents"`
	}
	err := json.Unmarshal(body, &target)
	if err == nil {
		t.Fatal("expected a decode error for an HTML body")
	}

	got := DecodeErrorDetail(err, body)
	if !strings.Contains(got, "bytes") {
		t.Errorf("detail %q should report the body size", got)
	}
	if strings.Contains(got, passkey) {
		t.Errorf("detail %q leaked the passkey", got)
	}
	if strings.Contains(got, "href") || strings.Contains(got, "DOCTYPE") {
		t.Errorf("detail %q echoed raw body bytes", got)
	}
}
