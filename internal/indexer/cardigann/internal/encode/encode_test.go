package encode

import "testing"

func TestWebUtilityEncode(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"plain", "ubuntu", "ubuntu"},
		{"space to plus", "hello world", "hello+world"},
		// Sub-delimiters ! * ( ) are percent-encoded (on-the-wire form — WAF-safe).
		{"bang encoded", "Mamma Mia!", "Mamma+Mia%21"},
		{"star encoded", "a*b", "a%2Ab"},
		{"parens encoded", "(a)", "%28a%29"},
		{"all four encoded", "!*()", "%21%2A%28%29"},
		// ~ is the only divergence from url.QueryEscape (.NET escapes it, Go does not).
		{"tilde escaped", "~", "%7E"},
		{"tilde in word", "a~b", "a%7Eb"},
		// Apostrophe is NOT a divergence: both Go and .NET emit %27.
		{"apostrophe escaped", "'", "%27"},
		{"real title with apostrophe", "Bob's Burgers", "Bob%27s+Burgers"},
		// Unreserved that both leave literal.
		{"unreserved kept", "a-b_c.d", "a-b_c.d"},
		// Literal plus is escaped (only spaces become '+').
		{"literal plus", "a+b", "a%2Bb"},
		// Reserved chars both escape identically.
		{"ampersand equals", "a&b=c", "a%26b%3Dc"},
		{"percent", "100%", "100%25"},
		{"slash colon", "a/b:c", "a%2Fb%3Ac"},
		// Unicode -> UTF-8 percent octets (uppercase hex), same as .NET.
		{"unicode jp", "日本語", "%E6%97%A5%E6%9C%AC%E8%AA%9E"},
		{"accented", "café", "caf%C3%A9"},
		// A mixed string exercising every rule at once.
		{"mixed", "Star*Trek (2009)! ~café's", "Star%2ATrek+%282009%29%21+%7Ecaf%C3%A9%27s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := WebUtilityEncode(tt.in); got != tt.want {
				t.Errorf("WebUtilityEncode(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestWebUtilityStringEncode(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"plain", "ubuntu", "ubuntu"},
		{"space to plus", "hello world", "hello+world"},
		// The four sub-delimiters ! * ( ) are LEFT LITERAL — the one divergence
		// from WebUtilityEncode (the .NET STRING form, matching MagnetUtil).
		{"bang literal", "Mamma Mia!", "Mamma+Mia!"},
		{"star literal", "a*b", "a*b"},
		{"parens literal", "(a)", "(a)"},
		{"all four literal", "!*()", "!*()"},
		{"title year", "Big Buck Bunny (2008)", "Big+Buck+Bunny+(2008)"},
		// ~ is still escaped, ' is still %27 — same as WebUtilityEncode.
		{"tilde escaped", "a~b", "a%7Eb"},
		{"apostrophe escaped", "Bob's Burgers", "Bob%27s+Burgers"},
		// Reserved chars outside the safe set still escape identically.
		{"ampersand equals", "a&b=c", "a%26b%3Dc"},
		{"percent", "100%", "100%25"},
		{"slash colon", "a/b:c", "a%2Fb%3Ac"},
		// Unicode -> UTF-8 percent octets, same as .NET.
		{"unicode jp", "日本語", "%E6%97%A5%E6%9C%AC%E8%AA%9E"},
		// Mixed: sub-delims literal, space '+', ~ %7E, ' %27, unicode octets.
		{"mixed", "Star*Trek (2009)! ~café's", "Star*Trek+(2009)!+%7Ecaf%C3%A9%27s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := WebUtilityStringEncode(tt.in); got != tt.want {
				t.Errorf("WebUtilityStringEncode(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestPathEscape(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"space to pct20", "hello world", "hello%20world"},
		{"star encoded", "a*b", "a%2Ab"},
		{"parens encoded", "(a)", "%28a%29"},
		{"tilde escaped", "~", "%7E"},
		{"apostrophe escaped", "Bob's Burgers", "Bob%27s%20Burgers"},
		{"literal plus", "a+b", "a%2Bb"},
		{"unicode jp", "日本語", "%E6%97%A5%E6%9C%AC%E8%AA%9E"},
		{"mixed", "Star Trek (2009)!~", "Star%20Trek%20%282009%29%21%7E"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PathEscape(tt.in); got != tt.want {
				t.Errorf("PathEscape(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
