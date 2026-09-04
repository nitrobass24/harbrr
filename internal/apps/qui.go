package apps

import (
	"context"
	"fmt"
	"net/http"

	"github.com/autobrr/harbrr/internal/domain"
	apphttp "github.com/autobrr/harbrr/internal/http"
)

// QuiInstance is one qui-managed qBittorrent instance (github.com/autobrr/qui): the
// id + display name the download-client dialog picks from. Only the subset harbrr
// needs from qui's GET /api/instances response.
type QuiInstance struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// QuiInstances proxies qui's instance list server-side so the browser never sees the
// API key: it decrypts the app's key, calls GET {base_url}/api/instances with
// X-API-Key, and returns the {id,name} list. Rejects a non-qui app.
//
// ponytail: small parse duplication with download/qui.go's instance decode (which
// reads only id) — accepted to avoid an apps→download import cycle and a premature
// shared qui-client package.
func (s *Service) QuiInstances(ctx context.Context, id int64) ([]QuiInstance, error) {
	app, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if app.Kind != domain.AppKindQui {
		return nil, fmt.Errorf("%w: app %d is not a qui app", domain.ErrInvalid, id)
	}
	key, err := s.DecryptKey(app)
	if err != nil {
		return nil, err
	}

	// NewJSONClient normalises the base URL, so an app stored with a trailing slash
	// no longer asks qui for "//api/instances".
	jc := apphttp.NewJSONClient(apphttp.JSONClient{
		Prefix: "apps: qui instances",
		Base:   app.BaseURL,
		Auth:   http.Header{"X-API-Key": {key}},
		Client: s.client,
		Secret: key,
	})
	var instances []QuiInstance
	if _, err := jc.Do(ctx, http.MethodGet, "/api/instances", nil, &instances); err != nil {
		return nil, err
	}
	return instances, nil
}
