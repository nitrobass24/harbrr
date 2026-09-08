package broadcastthenet

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	stdhttp "net/http"

	"github.com/autobrr/harbrr/internal/indexer/native"
)

// jsonMethod is the only JSON-RPC method the driver calls.
const jsonMethod = "getTorrents"

// rpcRequest is the JSON-RPC 2.0 envelope BTN expects. ID is a fixed 1 (BTN ignores
// its value; Prowlarr sends a random string, which is functionally equivalent).
type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	// Params is BTN getTorrents' positional argument tuple [apiKey, parameters,
	// results, offset] — a heterogeneous JSON array with no struct form. apiKey is
	// params[0], so the ENTIRE marshalled body is secret-bearing and never logged.
	Params []any `json:"params"`
	ID     int   `json:"id"`
}

// buildRPCBody marshals the getTorrents JSON-RPC body for a query. The API key is read
// from cfg and placed as the first positional param, followed by the parameters object
// and the page size (results). The trailing offset is always 0: harbrr fetches one page
// and paginates response-side downstream (a deliberate design choice mirroring FileList,
// NOT Prowlarr parity, which supports server-side paging). The returned bytes are
// secret-bearing (they embed the API key) and must never be logged.
func (d *driver) buildRPCBody(params btnParameters, results int) ([]byte, error) {
	body, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		Method:  jsonMethod,
		Params:  []any{d.Cfg["apikey"], params, results, 0},
		ID:      1,
	})
	if err != nil {
		// The marshal error could quote the body (which holds the API key), so it is
		// scrubbed before it can surface — via ScrubErr, so the error chain stays
		// intact for errors.Is/As while the displayed message is redacted.
		return nil, fmt.Errorf("broadcastthenet: build request body: %w", d.ScrubErr(err))
	}
	return body, nil
}

// post issues the JSON-RPC POST to the BTN endpoint. The body carries the API key as
// its first positional param, so it is never logged; a transport error surfaces only
// the endpoint's scheme://host (apphttp.SchemeHost) with the cause routed through
// apphttp.RedactURLError.
func (d *driver) post(ctx context.Context, body []byte) (*native.Response, error) {
	req, err := d.NewRequest(ctx, stdhttp.MethodPost, d.BaseURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return d.Do(ctx, req, native.ClassifyAuth403)
}
