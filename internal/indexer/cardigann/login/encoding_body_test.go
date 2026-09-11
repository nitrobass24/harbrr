package login

import (
	"context"
	"errors"
	stdhttp "net/http"
	"strings"
	"testing"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// cp1251ErrorDef mirrors 0daykiev.yml's login block (encoding windows-1251,
// method post, a Cyrillic :contains() error selector plus a message selector) —
// the vendored shape autobrr/harbrr#633 names. Several non-UTF-8 vendored defs
// carry it; the defs are read-only, so the fixture reproduces the shape here.
func cp1251ErrorDef() *loader.Definition {
	return &loader.Definition{Login: &loader.Login{
		Method: "post",
		Path:   "takelogin.php",
		Inputs: map[string]loader.Scalar{
			"username": scalar("{{ .Config.username }}"),
			"password": scalar("{{ .Config.password }}"),
		},
		Error: []loader.ErrorBlock{{
			Selector: `div.maintitle:contains("Ошибка")`,
			Message:  &loader.SelectorBlock{Selector: "div.borderwrap table.embedded"},
		}},
	}}
}

// TestLoginBodyEncoding covers autobrr/harbrr#633: the login executor must
// transcode a non-UTF-8 response body through the definition's declared encoding
// before any selector runs, exactly as Jackett evaluates checkForError on
// WebResult.ContentString. Without the transcode a cp1251 bad-credentials page is
// invisible to the def's own Cyrillic error selector and the login reports success.
func TestLoginBodyEncoding(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		bodyFile    string
		enc         encoding.Encoding
		wantFailure bool
		wantMessage string
	}{
		{
			name:        "cp1251 body decoded through the def encoding",
			bodyFile:    "login_error_cp1251.html",
			enc:         charmap.Windows1251,
			wantFailure: true,
			wantMessage: "Вы ввели неверный пароль",
		},
		{
			// The pre-fix behaviour, pinned: raw cp1251 bytes reach goquery as
			// mojibake, the :contains() selector matches nothing, and a
			// bad-credentials page is reported as a successful login.
			name:     "cp1251 body with no encoding stays undetected",
			bodyFile: "login_error_cp1251.html",
			enc:      nil,
		},
		{
			// A UTF-8 def is untouched: nil encoding is pass-through, so the same
			// page in UTF-8 matches exactly as it did before the fix.
			name:        "utf-8 body needs no transcoding",
			bodyFile:    "login_error_utf8.html",
			enc:         nil,
			wantFailure: true,
			wantMessage: "Вы ввели неверный пароль",
		},
		{
			// The mirror image: decoding UTF-8 bytes as cp1251 mangles them, so the
			// selector misses. The decode is unconditional, as Jackett's is — it is
			// the DEFINITION's declared encoding that decides, not a sniff.
			name:     "utf-8 body decoded as cp1251 no longer matches",
			bodyFile: "login_error_utf8.html",
			enc:      charmap.Windows1251,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rt := newReplay(t, step{
				wantMethod: stdhttp.MethodPost,
				wantPath:   "/takelogin.php",
				bodyFile:   tt.bodyFile,
			})
			exec := newExec(t, rt, map[string]string{
				"username": "user",
				"password": "wrong-password",
			}, WithEncoding(tt.enc))

			err := exec.Login(context.Background(), cp1251ErrorDef())

			if !tt.wantFailure {
				if err != nil {
					t.Fatalf("Login() = %v, want nil (the error selector must not match)", err)
				}
				return
			}
			if !errors.Is(err, ErrLoginFailed) {
				t.Fatalf("Login() = %v, want ErrLoginFailed", err)
			}
			if !strings.Contains(err.Error(), tt.wantMessage) {
				t.Errorf("Login() error = %q, want it to carry the tracker message %q", err, tt.wantMessage)
			}
		})
	}
}
