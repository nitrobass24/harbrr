package native

import "testing"

// TestFlexIntDecode proves FlexInt accepts a JSON string and a bare number and degrades
// blank/garbage/null/a bare float to 0. No adopting tracker's wire format sends a float in
// these fields, but a hostile/malformed response must not fail the whole page.
func TestFlexIntDecode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want int64
	}{
		{`"527749302"`, 527749302},
		{`42`, 42},
		{`""`, 0},
		{`null`, 0},
		{`"notanumber"`, 0},
		{`3.5`, 0},
	}
	for _, c := range cases {
		var n FlexInt
		if err := n.UnmarshalJSON([]byte(c.in)); err != nil {
			t.Errorf("UnmarshalJSON(%s): %v", c.in, err)
			continue
		}
		if got := n.Int64(); got != c.want {
			t.Errorf("FlexInt(%s).Int64() = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestFlexStringDecode proves FlexString accepts both a JSON string and a bare JSON
// number, and that Int64() parses tolerantly (blank/garbage -> 0).
func TestFlexStringDecode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want int64
	}{
		{`"42"`, 42},
		{`42`, 42},
		{`""`, 0},
		{`null`, 0},
		{`"notanumber"`, 0},
	}
	for _, c := range cases {
		var s FlexString
		if err := s.UnmarshalJSON([]byte(c.in)); err != nil {
			t.Errorf("UnmarshalJSON(%s): %v", c.in, err)
			continue
		}
		if got := s.Int64(); got != c.want {
			t.Errorf("FlexString(%s).Int64() = %d, want %d", c.in, got, c.want)
		}
	}
}
