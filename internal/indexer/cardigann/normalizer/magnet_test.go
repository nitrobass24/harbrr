package normalizer

import "testing"

// wantTrackerTail is the &tr=... suffix InfoHashToMagnet appends: the ten public
// trackers, each WebUtility-encoded (':' -> %3A, '/' -> %2F; '-' and '.' stay
// literal). Hand-derived from MagnetUtil._Trackers + the .NET encoding rules the
// encode package pins, NOT captured from harbrr's own output.
const wantTrackerTail = "&tr=udp%3A%2F%2Ftracker.opentrackr.org%3A1337%2Fannounce" +
	"&tr=udp%3A%2F%2Fopen.demonii.com%3A1337%2Fannounce" +
	"&tr=udp%3A%2F%2Fopen.stealth.si%3A80%2Fannounce" +
	"&tr=udp%3A%2F%2Ftracker.torrent.eu.org%3A451%2Fannounce" +
	"&tr=udp%3A%2F%2Fvito-tracker.space%3A6969%2Fannounce" +
	"&tr=udp%3A%2F%2Fvito-tracker.duckdns.org%3A6969%2Fannounce" +
	"&tr=udp%3A%2F%2Ftracker.theoks.net%3A6969%2Fannounce" +
	"&tr=udp%3A%2F%2Ftracker.srv00.com%3A6969%2Fannounce" +
	"&tr=udp%3A%2F%2Ftracker.qu.ax%3A6969%2Fannounce" +
	"&tr=udp%3A%2F%2Ftracker.corpscorp.online%3A80%2Fannounce"

func TestFromInfoHash(t *testing.T) {
	tests := []struct {
		name     string
		infoHash string
		title    string
		want     string
	}{
		{
			// Full golden: uppercase hash preserved, title space -> '+' and
			// '(' ')' LEFT LITERAL — the .NET WebUtility.UrlEncode STRING form
			// MagnetUtil.InfoHashToPublicMagnet emits (safe set includes ! * ( )),
			// NOT the on-the-wire form (which would percent-encode them). Then the
			// full tracker tail.
			name:     "full magnet with tracker tail",
			infoHash: "ABCDEF0123456789ABCDEF0123456789ABCDEF01",
			title:    "Big Buck Bunny (2008)",
			want:     "magnet:?xt=urn:btih:ABCDEF0123456789ABCDEF0123456789ABCDEF01&dn=Big+Buck+Bunny+(2008)" + wantTrackerTail,
		},
		{
			// The four sub-delimiters ! * ( ) stay literal (STRING form), while a
			// space still becomes '+' — proving the magnet encoder diverges from the
			// on-the-wire request encoder for exactly these chars.
			name:     "sub-delimiters left literal in dn",
			infoHash: "ABCDEF0123456789ABCDEF0123456789ABCDEF01",
			title:    "Mamma Mia! *Star* (Live)",
			want:     "magnet:?xt=urn:btih:ABCDEF0123456789ABCDEF0123456789ABCDEF01&dn=Mamma+Mia!+*Star*+(Live)" + wantTrackerTail,
		},
		{
			name:     "tilde in title percent-escaped (WebUtility, not Go)",
			infoHash: "abcdef0123456789abcdef0123456789abcdef01",
			title:    "a~b",
			want:     "magnet:?xt=urn:btih:abcdef0123456789abcdef0123456789abcdef01&dn=a%7Eb" + wantTrackerTail,
		},
		{name: "blank hash yields no magnet", infoHash: "", title: "Title", want: ""},
		{name: "blank title yields no magnet", infoHash: "ABCDEF01", title: "", want: ""},
		{name: "whitespace-only hash yields no magnet", infoHash: "  ", title: "Title", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FromInfoHash(tt.infoHash, tt.title); got != tt.want {
				t.Errorf("FromInfoHash(%q, %q)\n got = %q\nwant = %q", tt.infoHash, tt.title, got, tt.want)
			}
		})
	}
}

func TestToInfoHash(t *testing.T) {
	tests := []struct {
		name   string
		magnet string
		want   string
	}{
		{
			name:   "btih hash preserves case",
			magnet: "magnet:?xt=urn:btih:ABCdef0123456789abcdef0123456789ABCDEF01&dn=x",
			want:   "ABCdef0123456789abcdef0123456789ABCDEF01",
		},
		{
			name:   "non-btih urn returns final segment",
			magnet: "magnet:?xt=urn:sha1:ZYXW&dn=x",
			want:   "ZYXW",
		},
		{name: "no xt argument", magnet: "magnet:?dn=x", want: ""},
		{name: "no query string", magnet: "not-a-magnet", want: ""},
		{name: "fragment dropped before parse", magnet: "magnet:?xt=urn:btih:AABB#frag", want: "AABB"},
		{
			// A sibling dn carrying a bare '%' makes Go's url.ParseQuery error;
			// Jackett's lenient ParseQuery still extracts xt. The infohash must
			// survive the malformed sibling.
			name:   "bad percent in sibling dn does not drop xt",
			magnet: "magnet:?xt=urn:btih:DEADBEEF&dn=100%_Wolf",
			want:   "DEADBEEF",
		},
		{
			// A ';' in dn: Go treats it as an (invalid) separator and errors;
			// Jackett treats ';' as ordinary data. Either way xt must survive.
			name:   "semicolon in sibling dn does not drop xt",
			magnet: "magnet:?xt=urn:btih:CAFE&dn=a;b",
			want:   "CAFE",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := toInfoHash(tt.magnet); got != tt.want {
				t.Errorf("toInfoHash(%q) = %q, want %q", tt.magnet, got, tt.want)
			}
		})
	}
}

// TestRoundTrip proves synthesis then extraction recovers the original hash with
// case intact — the property the normalizer relies on when reconciling the two.
func TestRoundTrip(t *testing.T) {
	const hash = "DEADBEEF0123456789ABCDEF0123456789ABCDEF"
	got := toInfoHash(FromInfoHash(hash, "Some Title"))
	if got != hash {
		t.Errorf("round trip = %q, want %q", got, hash)
	}
}
