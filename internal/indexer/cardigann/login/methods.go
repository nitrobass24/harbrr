package login

import (
	"fmt"
	stdhttp "net/http"
	"net/url"
	"sort"
	"strings"

	apphttp "github.com/autobrr/seekbrr/internal/http"
	"github.com/autobrr/seekbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/seekbrr/internal/indexer/cardigann/selector"
	"github.com/autobrr/seekbrr/internal/indexer/cardigann/template"
)

// loginPost assembles Login.Inputs (template-rendered) and POSTs them as a form
// body to SubmitPath (falling back to Path), then runs the error selectors.
// Mirrors Jackett's Login.Method == "post" branch.
func (e *Executor) loginPost(def *loader.Definition) error {
	// Jackett seeds Login.Cookies before the POST in the post path too
	// (CardigannIndexer DoLogin post branch); get/oneurl do NOT seed them.
	if err := e.seedStaticCookies(def.Login.Cookies); err != nil {
		return err
	}
	pairs, err := e.renderInputs(def.Login.Inputs)
	if err != nil {
		return err
	}
	target := def.Login.SubmitPath
	if target == "" {
		target = def.Login.Path
	}
	return e.postForm(def, target, pairs)
}

// loginGet assembles Login.Inputs as a query string and GETs Path, then runs the
// error selectors. Mirrors Jackett's Login.Method == "get" branch (path + "?" +
// queryCollection).
func (e *Executor) loginGet(def *loader.Definition) error {
	pairs, err := e.renderInputs(def.Login.Inputs)
	if err != nil {
		return err
	}
	rawURL, err := e.resolvePath(def.Login.Path)
	if err != nil {
		return err
	}
	full, err := appendQuery(rawURL, pairs)
	if err != nil {
		return err
	}
	body, status, err := e.get(full, def.Login.Headers)
	if err != nil {
		return err
	}
	return e.checkErrors(def.Login, full, body, status)
}

// loginCookie is the manual-cookie fallback: render the "cookie" input to a raw
// Cookie header string, SEED the jar for the site domain (no login round-trip),
// then leave validation to the Test block. Mirrors Jackett setting
// configData.CookieHeader for Login.Method == "cookie".
func (e *Executor) loginCookie(def *loader.Definition) error {
	raw, ok := def.Login.Inputs["cookie"]
	if !ok {
		return fmt.Errorf("%w: cookie method requires a 'cookie' input", ErrLoginFailed)
	}
	rendered, err := template.Eval(raw.String(), e.templateContext())
	if err != nil {
		return fmt.Errorf("rendering cookie input: %w", err)
	}
	if err := e.seedCookies(rendered); err != nil {
		return err
	}
	return nil
}

// loginOneURL issues a single GET to Path + the "oneurl" input (no corpus def
// uses this method today; kept minimal and documented). Mirrors Jackett's
// resolvePath(Login.Path + OneUrl).
func (e *Executor) loginOneURL(def *loader.Definition) error {
	one := ""
	if v, ok := def.Login.Inputs["oneurl"]; ok {
		rendered, err := template.Eval(v.String(), e.templateContext())
		if err != nil {
			return fmt.Errorf("rendering oneurl input: %w", err)
		}
		one = rendered
	}
	rawURL, err := e.resolvePath(def.Login.Path)
	if err != nil {
		return err
	}
	body, status, err := e.get(rawURL+one, def.Login.Headers)
	if err != nil {
		return err
	}
	return e.checkErrors(def.Login, rawURL+one, body, status)
}

// postForm POSTs url.Values as application/x-www-form-urlencoded to the resolved
// target path, then runs the error selectors. Shared by post and form methods.
func (e *Executor) postForm(def *loader.Definition, target string, pairs url.Values) error {
	rawURL, err := e.resolvePath(target)
	if err != nil {
		return err
	}
	headers := mergeFormHeaders(def.Login.Headers)
	body, status, err := e.do(stdhttp.MethodPost, rawURL, strings.NewReader(pairs.Encode()), headers)
	if err != nil {
		return err
	}
	return e.checkErrors(def.Login, rawURL, body, status)
}

// renderInputs template-renders each Login.Inputs value into url.Values. Keys
// are definition-authored field names; values may contain credentials and are
// NEVER logged. A render error references the field name only.
func (e *Executor) renderInputs(inputs map[string]loader.Scalar) (url.Values, error) {
	out := url.Values{}
	for _, name := range sortedKeys(inputs) {
		rendered, err := template.Eval(inputs[name].String(), e.templateContext())
		if err != nil {
			return nil, fmt.Errorf("rendering login input %q: %w", name, err)
		}
		out.Set(name, rendered)
	}
	return out, nil
}

// checkErrors evaluates the login error selectors against the response body.
// 401 is a hard failure (Jackett throws on Unauthorized). Otherwise, the first
// matching error selector yields its message (optionally via a Message selector
// block), wrapped into ErrLoginFailed. The message is definition error text, not
// a credential; the URL is redacted.
func (e *Executor) checkErrors(l *loader.Login, rawURL string, body []byte, status int) error {
	if status == stdhttp.StatusUnauthorized {
		return fmt.Errorf("%w: 401 Unauthorized from %s", ErrLoginFailed, apphttp.RedactURL(rawURL))
	}
	// An anti-bot interstitial (Cloudflare, etc.) means the real response never
	// loaded, so the error selectors would all miss and we would wrongly report
	// success. The form flow already screens its landing page; screen the
	// post/get/oneurl response here too. Fails loud via ErrSolverRequired.
	if err := detectAntiBot(body); err != nil {
		return err
	}
	if len(l.Error) == 0 {
		return nil
	}
	doc, err := e.Selector.ParseHTML(body)
	if err != nil {
		return fmt.Errorf("parsing login response from %s: %w", apphttp.RedactURL(rawURL), err)
	}
	root := doc.Root()
	for i := range l.Error {
		msg, matched, err := e.evalErrorBlock(root, l.Error[i])
		if err != nil {
			return err
		}
		if matched {
			return fmt.Errorf("%w: %s (from %s)", ErrLoginFailed, msg, apphttp.RedactURL(rawURL))
		}
	}
	return nil
}

// evalErrorBlock tests one error selector. When it matches, it extracts the
// error message: from the Message selector block if present, else the matched
// element's text. The returned message is trimmed/single-lined.
func (e *Executor) evalErrorBlock(root selector.Row, blk loader.ErrorBlock) (msg string, matched bool, err error) {
	probe := loader.SelectorBlock{Selector: blk.Selector}
	val, found, err := e.Selector.Field(root, probe)
	if err != nil {
		return "", false, fmt.Errorf("evaluating error selector %q: %w", blk.Selector, err)
	}
	if !found {
		return "", false, nil
	}
	if blk.Message != nil {
		mval, mfound, merr := e.Selector.Field(root, *blk.Message)
		if merr != nil {
			return "", false, fmt.Errorf("evaluating error message selector %q: %w", blk.Message.Selector, merr)
		}
		if mfound {
			return trimMessage(mval), true, nil
		}
	}
	return trimMessage(val), true, nil
}

// mergeFormHeaders returns the login headers with a form-urlencoded Content-Type
// added when the definition did not set one. A copy is returned; the input map
// is not mutated.
func mergeFormHeaders(in map[string][]string) map[string][]string {
	out := make(map[string][]string, len(in)+1)
	hasContentType := false
	for k, v := range in {
		out[k] = v
		if strings.EqualFold(k, "Content-Type") {
			hasContentType = true
		}
	}
	if !hasContentType {
		out["Content-Type"] = []string{"application/x-www-form-urlencoded"}
	}
	return out
}

// appendQuery appends url.Values to rawURL's query string, preserving any query
// already present in the resolved path (the get-method corpus puts fixed params
// directly in Login.Path).
func appendQuery(rawURL string, pairs url.Values) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parsing login URL %s: %w", apphttp.RedactURL(rawURL), err)
	}
	q := u.Query()
	for k, vs := range pairs {
		for _, v := range vs {
			q.Add(k, v)
		}
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// sortedKeys returns map keys in a deterministic order so rendered form bodies
// and query strings are stable (test-assertable) regardless of map iteration.
func sortedKeys(m map[string]loader.Scalar) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
