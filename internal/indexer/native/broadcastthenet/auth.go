package broadcastthenet

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	stdhttp "net/http"

	apphttp "github.com/autobrr/harbrr/internal/http"
)

// maxBodyBytes caps a getTorrents JSON-RPC response. A BTN page is small JSON (one
// results count plus up to results=100 torrent rows), so this is generous while still
// bounding a hostile or runaway body.
const maxBodyBytes = 8 << 20 // 8 MiB

// jsonMethod is the only JSON-RPC method the driver calls.
const jsonMethod = "getTorrents"

// rpcRequest is the JSON-RPC 2.0 envelope BTN expects. Params is positional: BTN's
// getTorrents takes [apiKey, parameters, results, offset]. The API key is params[0],
// so the ENTIRE body is secret-bearing and must never be logged. ID is a fixed 1 (BTN
// ignores its value; Prowlarr sends a random string, which is functionally equivalent).
type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
	ID      int    `json:"id"`
}

// buildRPCBody marshals the getTorrents JSON-RPC body for a query. The API key is read
// from cfg and placed as the first positional param; the parameters object, the page
// size (results) and the offset follow. The returned bytes are secret-bearing (they
// embed the API key) and must never be logged.
func (d *driver) buildRPCBody(params btnParameters, results, offset int) ([]byte, error) {
	body, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		Method:  jsonMethod,
		Params:  []any{d.cfg["apikey"], params, results, offset},
		ID:      1,
	})
	if err != nil {
		// The marshal error could quote the body (which holds the API key), so it is
		// scrubbed before it can surface.
		return nil, fmt.Errorf("broadcastthenet: build request body: %s", d.scrubAPIKey(err.Error()))
	}
	return body, nil
}

// post issues the JSON-RPC POST to the BTN endpoint. The body carries the API key as
// its first positional param, so it is never logged; a transport error routes the URL
// (never the body) through apphttp.RedactURL. The caller owns the returned body and
// interprets the status.
func (d *driver) post(ctx context.Context, body []byte) (*stdhttp.Response, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodPost, d.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("broadcastthenet: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("broadcastthenet: request to %s: %w", apphttp.RedactURL(d.baseURL), err)
	}
	return resp, nil
}
