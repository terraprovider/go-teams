package teamsapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/terraprovider/go-msadmin/httpx"
)

// Transport selects which of the module's two HTTP client fingerprints a call
// mimics (see docs 05/06 — the fingerprints differ in User-Agent + headers).
type Transport string

const (
	// ConfigAPI is the AutoRest/ConfigAPI stack (tenant config + singleton settings).
	ConfigAPI Transport = "configapi"
	// MPA is the Modern Policy Administration / PolicyRp stack (the -CsTeams*Policy family).
	MPA Transport = "mpa"
)

// OpKind is the policy REST operation shape.
type OpKind int

const (
	PolicyList   OpKind = iota // GET  /Skype.Policy/configurations/{PolicyName}          → all instances
	PolicyGet                  // GET  …/{PolicyName}, filtered to Identity (module-style)
	PolicyNew                  // POST …/{PolicyName}                                      body = params
	PolicySet                  // PUT  …/{PolicyName}/configuration/{Identity}             body = params (no Identity)
	PolicyRemove               // DELETE …/{PolicyName}/configuration/{Identity}
	AutoRest                   // typed AutoRest op: Op.Method + Op.Path (with {identity} substituted)
	PolicyGrant                // Grant-Cs*Policy: assign PolicyName to a target (Global/Group/Identity)
)

// Op fully describes one config/policy call. Generated bindings build this from
// the derived catalog; the transport turns it into the right REST request +
// fingerprint. CmdletName is the internal operation id the module puts in
// User-Agent / X-MS-CmdletName (e.g. "Get-CsConfiguration_Get").
type Op struct {
	CmdletName string
	Transport  Transport
	Kind       OpKind
	PolicyName string // policy kinds: the {PolicyName} path segment
	Path       string // AutoRest kind: the path template (may contain {identity})
	Method     string // AutoRest kind: HTTP method; otherwise set by resolve()
}

// Result carries the returned objects plus any surfaced warnings.
type Result struct {
	Value    []map[string]any
	Warnings []string
}

// Decode unmarshals Value into v (e.g. *[]models.TeamsMeetingPolicy). It round-
// trips through JSON, so v's fields need json tags matching the API property names.
func (r *Result) Decode(v any) error {
	b, err := json.Marshal(r.Value)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// APIError is the shared error type (go-msadmin/httpx) for a non-2xx response.
type APIError = httpx.APIError

// IsNotFound reports whether err is a 404 from the API (a missing instance).
// Terraform providers wire their isNotFound helper to this (or to httpx.IsNotFound
// directly).
func IsNotFound(err error) bool { return httpx.IsNotFound(err) }

// resolve turns an Op + params into (method, path, body).
func (c *Client) resolve(op Op, params map[string]any) (method, path string, body []byte, err error) {
	pn := url.PathEscape(op.PolicyName)
	base := "/Skype.Policy/configurations/" + pn
	identity, _ := params["Identity"].(string)
	switch op.Kind {
	case PolicyList, PolicyGet:
		return http.MethodGet, base, nil, nil
	case PolicyNew:
		b, e := json.Marshal(params)
		return http.MethodPost, base, b, e
	case PolicySet:
		if identity == "" {
			return "", "", nil, fmt.Errorf("teamsapi: %s: Identity is required for Set", op.PolicyName)
		}
		payload := map[string]any{}
		for k, v := range params {
			if k == "Identity" {
				continue
			}
			payload[k] = v
		}
		b, e := json.Marshal(payload)
		return http.MethodPut, base + "/configuration/" + url.PathEscape(identity), b, e
	case PolicyRemove:
		if identity == "" {
			return "", "", nil, fmt.Errorf("teamsapi: %s: Identity is required for Remove", op.PolicyName)
		}
		return http.MethodDelete, base + "/configuration/" + url.PathEscape(identity), nil, nil
	case AutoRest:
		m := op.Method
		if m == "" {
			m = http.MethodGet
		}
		p := op.Path
		if strings.Contains(p, "{identity}") {
			p = strings.ReplaceAll(p, "{identity}", url.PathEscape(identity))
		}
		var b []byte
		if m != http.MethodGet && m != http.MethodDelete && len(params) > 0 {
			payload := map[string]any{}
			for k, v := range params {
				if k == "Identity" && strings.Contains(op.Path, "{identity}") {
					continue // it's in the path
				}
				payload[k] = v
			}
			b, err = json.Marshal(payload)
		}
		return m, p, b, err
	case PolicyGrant:
		// op.PolicyName is the policy TYPE (e.g. "TeamsMeetingPolicy"). The type and
		// target go in the PATH (not the body); the body carries only the instance
		// name (and Rank for groups). Routes decompiled from {User,Group,Global}
		// GrantPolicy; the user route is live-validated. Grants materialise
		// asynchronously (the write returns before the assignment is effective).
		pt := url.PathEscape(op.PolicyName)
		body := map[string]any{}
		if v, ok := params["PolicyName"]; ok {
			body["PolicyName"] = v
		}
		if g, _ := params["Global"].(bool); g {
			b, err := json.Marshal(body)
			return http.MethodPatch, "/Skype.Policy/tenants/policies/" + pt, b, err
		}
		if grp, _ := params["Group"].(string); grp != "" {
			if r, ok := params["Rank"]; ok {
				body["Rank"] = r
			}
			b, err := json.Marshal(body)
			return http.MethodPatch, "/Skype.Policy/groupPolicyAssignments/" + url.PathEscape(grp) + "/policyTypes/" + pt, b, err
		}
		if user, _ := params["Identity"].(string); user != "" {
			b, err := json.Marshal(body)
			return http.MethodPatch, "/Skype.Policy/users/" + url.PathEscape(user) + "/policies/" + pt, b, err
		}
		return "", "", nil, fmt.Errorf("teamsapi: Grant %s: one of -Global/-Group/-Identity is required", op.PolicyName)
	}
	return "", "", nil, fmt.Errorf("teamsapi: unknown op kind %d", op.Kind)
}

// Invoke runs one config/policy operation and returns the resulting objects.
// params holds the bound parameters (generated request types build this map).
func (c *Client) Invoke(ctx context.Context, op Op, params map[string]any) (*Result, error) {
	if params == nil {
		params = map[string]any{}
	}
	method, path, body, err := c.resolve(op, params)
	if err != nil {
		return nil, err
	}
	op.Method = method
	resp, err := c.send(ctx, path, op, body)
	if err != nil {
		return nil, err
	}
	raw, err := httpx.DecodeBody(resp)
	if err != nil {
		return nil, err
	}
	if debugEnabled() {
		fmt.Fprintf(os.Stderr, "[teamsapi] <<< %d %s\n", resp.StatusCode, string(raw))
	}
	if resp.StatusCode >= 400 {
		return nil, parseAPIError(resp.StatusCode, raw)
	}
	out := &Result{}
	if w := resp.Header.Get("X-Ms-ConfigApi-PowerShell-WarningMessage"); w != "" {
		out.Warnings = append(out.Warnings, w)
	}
	out.Value, err = decodeValue(raw)
	if err != nil {
		return nil, fmt.Errorf("teamsapi: decode %s response: %w", op.PolicyName, err)
	}
	// PolicyGet fetches all instances (module behaviour) and filters to Identity.
	if op.Kind == PolicyGet {
		if id, _ := params["Identity"].(string); id != "" {
			out.Value = filterByIdentity(out.Value, id)
		}
	}
	return out, nil
}

// decodeValue accepts either a bare JSON array of objects (the policy read shape)
// or a single JSON object (some writes), normalising to []map[string]any.
func decodeValue(raw []byte) ([]map[string]any, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, nil
	}
	switch raw[0] {
	case '[':
		var a []map[string]any
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, err
		}
		return a, nil
	case '{':
		var o map[string]any
		if err := json.Unmarshal(raw, &o); err != nil {
			return nil, err
		}
		return []map[string]any{o}, nil
	}
	return nil, fmt.Errorf("unexpected response (not JSON object/array)")
}

// filterByIdentity keeps the instances whose Identity matches id. Custom policy
// instances are stored with a "Tag:" scope prefix (New-Cs*Policy -Identity X
// creates the instance "Tag:X"), but the cmdlets accept — and callers pass — the
// bare name; so a query for "X" also matches "Tag:X" (and "Global" still matches
// only "Global"). Passing the already-scoped "Tag:X" matches directly.
func filterByIdentity(in []map[string]any, id string) []map[string]any {
	var out []map[string]any
	for _, o := range in {
		s, _ := o["Identity"].(string)
		if strings.EqualFold(s, id) || strings.EqualFold(s, "Tag:"+id) {
			out = append(out, o)
		}
	}
	return out
}

func parseAPIError(status int, raw []byte) error {
	msg := strings.TrimSpace(string(raw))
	code := ""
	// Best-effort extraction of a structured error message.
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"Message"`
	}
	if json.Unmarshal(raw, &env) == nil {
		if env.Error.Message != "" {
			msg, code = env.Error.Message, env.Error.Code
		} else if env.Message != "" {
			msg = env.Message
		}
	}
	if len(msg) > 600 {
		msg = msg[:600]
	}
	if msg == "" {
		msg = "(no response body)"
	}
	if status == http.StatusForbidden {
		msg += " — the app's service principal likely lacks a Teams admin directory role (assign one for app-only)"
	}
	return &APIError{Status: status, Code: code, Message: msg, Body: string(raw)}
}
