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
	JSONRPC string    `json:"jsonrpc"`
	Method  string    `json:"method"`
	Params  rpcParams `json:"params"`
	ID      int       `json:"id"`
}

// rpcParams is BTN getTorrents' positional argument tuple [apiKey, parameters, results,
// offset]. It is a typed struct (not a bare []any) so the order and types are explicit;
// MarshalJSON emits the positional array BTN expects, so the wire format is unchanged.
// APIKey is params[0], so the ENTIRE marshalled body is secret-bearing and never logged.
type rpcParams struct {
	APIKey     string
	Parameters btnParameters
	Results    int
	Offset     int
}

// MarshalJSON renders the params as BTN's positional [apiKey, parameters, results,
// offset] array, keeping the wire format identical to the raw tuple it replaces. Any
// marshal error is wrapped (it surfaces via buildRPCBody's json.Marshal, which scrubs
// the API key before the error is returned); a type-based marshal error carries no value.
func (p rpcParams) MarshalJSON() ([]byte, error) {
	b, err := json.Marshal([]any{p.APIKey, p.Parameters, p.Results, p.Offset})
	if err != nil {
		return nil, fmt.Errorf("broadcastthenet: marshal rpc params: %w", err)
	}
	return b, nil
}

// buildRPCBody marshals the getTorrents JSON-RPC body for a query. The API key is read
// from cfg and placed as the first positional param; the parameters object, the page
// size (results) and the offset follow. The returned bytes are secret-bearing (they
// embed the API key) and must never be logged.
func (d *driver) buildRPCBody(params btnParameters, results, offset int) ([]byte, error) {
	body, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		Method:  jsonMethod,
		Params:  rpcParams{APIKey: d.Cfg["apikey"], Parameters: params, Results: results, Offset: offset},
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
// its first positional param, so it is never logged; a transport error surfaces only
// the endpoint's scheme://host (apphttp.SchemeHost) with the cause routed through
// apphttp.RedactURLError.
func (d *driver) post(ctx context.Context, body []byte) (*native.Response, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodPost, d.BaseURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("broadcastthenet: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	return d.Do(ctx, req, native.ClassifyAuth403)
}
