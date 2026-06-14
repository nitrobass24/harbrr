package login

import (
	stdhttp "net/http"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// TestGetLoginIgnores401 pins the DigitalCore-style behavior: a get-login probe
// to an apikey-HEADER API endpoint returns 401 (the X-API-KEY header is sent on
// the SEARCH request, not the login probe), and harbrr must NOT fail login on
// that status. Jackett never fails a login on HTTP status — it relies on error
// selectors — and the real auth is validated by the (header-bearing) search.
func TestGetLoginIgnores401(t *testing.T) {
	t.Parallel()
	rt := newReplay(t, step{
		wantMethod: stdhttp.MethodGet,
		wantPath:   "/api/v1/torrents",
		status:     stdhttp.StatusUnauthorized,
	})
	def := &loader.Definition{Login: &loader.Login{Method: "get", Path: "api/v1/torrents"}}
	e := newExec(t, rt, map[string]string{})
	if err := e.Login(def); err != nil {
		t.Fatalf("get-login must ignore a 401 probe status, got %v", err)
	}
}
