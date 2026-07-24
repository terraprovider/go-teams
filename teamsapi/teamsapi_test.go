package teamsapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// recorder captures the last request (headers + body) per "METHOD path".
type recorder struct {
	mu   sync.Mutex
	hdr  map[string]http.Header
	body map[string][]byte
}

func newRecorder() *recorder {
	return &recorder{hdr: map[string]http.Header{}, body: map[string][]byte{}}
}

func (rec *recorder) record(r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	key := r.Method + " " + r.URL.Path
	rec.hdr[key] = r.Header.Clone()
	rec.body[key] = b
}

func (rec *recorder) get(key string) (http.Header, []byte) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return rec.hdr[key], rec.body[key]
}

// testClient wires a Client to an in-process TLS server with a handler.
func testClient(t *testing.T, h http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	c, err := New(Options{
		Environment: Environment{Name: "Test", Endpoint: strings.TrimPrefix(srv.URL, "https://"), Resource: "res"},
		TenantID:    "tid",
		Tokens:      StaticTokenProvider("tok"),
		HTTPClient:  srv.Client(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c, srv
}

const meetingPolicyJSON = `[{"Identity":"Global","AllowMeetNow":true},{"Identity":"Tag:Kiosk","AllowMeetNow":false}]`

func discoveryHandler(rec *recorder, policyBody string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		switch {
		case r.URL.Path == "/Teams.Tenant/serviceDiscovery":
			io.WriteString(w, `{"Endpoints":{"ConfigApiEndpoint":"cfg","AdminServiceEndpoint":"admintest.example"},"Headers":{"X-MS-Forest":"ed9"}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/Skype.Policy/configurations/TeamsMeetingPolicy":
			io.WriteString(w, policyBody)
		case r.Method == http.MethodPut && r.URL.Path == "/Skype.Policy/configurations/TeamsMeetingPolicy/configuration/Global":
			io.WriteString(w, `[{"Identity":"Global","AllowMeetNow":false}]`)
		case r.Method == http.MethodDelete && r.URL.Path == "/Skype.Policy/configurations/TeamsMeetingPolicy/configuration/Global":
			w.WriteHeader(http.StatusOK)
		default:
			http.Error(w, `{"error":{"code":"NotFound","message":"no such thing"}}`, http.StatusNotFound)
		}
	}
}

func TestHandshakeAndPolicyGet(t *testing.T) {
	rec := newRecorder()
	c, srv := testClient(t, discoveryHandler(rec, meetingPolicyJSON))
	defer srv.Close()

	res, err := c.Invoke(context.Background(),
		Op{CmdletName: "Get-CsConfiguration_Get", Transport: ConfigAPI, Kind: PolicyGet, PolicyName: "TeamsMeetingPolicy"},
		map[string]any{"Identity": "Global"})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	// serviceDiscovery ran as the handshake.
	if h, _ := rec.get("GET /Teams.Tenant/serviceDiscovery"); h == nil {
		t.Fatal("serviceDiscovery was not called")
	}
	// PolicyGet fetched all instances and filtered to Identity=Global.
	if len(res.Value) != 1 || res.Value[0]["Identity"] != "Global" {
		t.Fatalf("expected 1 Global instance, got %v", res.Value)
	}

	// ConfigAPI fingerprint on the policy GET.
	h, _ := rec.get("GET /Skype.Policy/configurations/TeamsMeetingPolicy")
	wantUA := "Microsoft.Teams.ConfigAPI.Cmdlets/" + DefaultConfigAPIVersion + "/Get-CsConfiguration_Get/MicrosoftTeams/" + DefaultModuleVersion
	if got := h.Get("User-Agent"); got != wantUA {
		t.Errorf("User-Agent = %q, want %q", got, wantUA)
	}
	if got := h.Get("X-MS-Target-Uri"); got != "https://admintest.example/" {
		t.Errorf("X-MS-Target-Uri = %q, want the discovered admin endpoint", got)
	}
	if got := h.Get("X-MS-CmdletName"); got != "Get-CsConfiguration_Get" {
		t.Errorf("X-MS-CmdletName = %q", got)
	}
	if h.Get("X-MS-Correlation-Id") == "" {
		t.Error("missing X-MS-Correlation-Id")
	}
	if h.Get("Authorization") != "Bearer tok" {
		t.Errorf("Authorization = %q", h.Get("Authorization"))
	}
	if h.Get("MPACmdlet") != "" {
		t.Error("ConfigAPI request must not carry the MPA marker")
	}
}

func TestMPAFingerprint(t *testing.T) {
	rec := newRecorder()
	c, srv := testClient(t, discoveryHandler(rec, meetingPolicyJSON))
	defer srv.Close()

	_, err := c.Invoke(context.Background(),
		Op{CmdletName: "Get-CsTeamsMeetingPolicy", Transport: MPA, Kind: PolicyList, PolicyName: "TeamsMeetingPolicy"}, nil)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	h, _ := rec.get("GET /Skype.Policy/configurations/TeamsMeetingPolicy")
	wantUA := "Microsoft.Teams.Policy.Administration/" + DefaultMPAVersion + "/Get-CsTeamsMeetingPolicy/MicrosoftTeams/" + DefaultModuleVersion
	if got := h.Get("User-Agent"); got != wantUA {
		t.Errorf("User-Agent = %q, want %q", got, wantUA)
	}
	if h.Get("MPACmdlet") != "true" {
		t.Error("MPA request must carry MPACmdlet: true")
	}
	if h.Get("X-MS-Forest") != "ed9" {
		t.Errorf("X-MS-Forest = %q, want discovered forest", h.Get("X-MS-Forest"))
	}
}

func TestPolicySetBodyExcludesIdentity(t *testing.T) {
	rec := newRecorder()
	c, srv := testClient(t, discoveryHandler(rec, meetingPolicyJSON))
	defer srv.Close()

	_, err := c.Invoke(context.Background(),
		Op{CmdletName: "Set-CsConfiguration_Set", Transport: ConfigAPI, Kind: PolicySet, PolicyName: "TeamsMeetingPolicy"},
		map[string]any{"Identity": "Global", "AllowMeetNow": false})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	h, body := rec.get("PUT /Skype.Policy/configurations/TeamsMeetingPolicy/configuration/Global")
	if h == nil {
		t.Fatal("PUT to the instance path was not made")
	}
	if h.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", h.Get("Content-Type"))
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, body)
	}
	if _, ok := payload["Identity"]; ok {
		t.Errorf("PUT body must not contain Identity (it is in the path): %s", body)
	}
	if payload["AllowMeetNow"] != false {
		t.Errorf("PUT body missing the set property: %s", body)
	}
}

func TestIsNotFound(t *testing.T) {
	rec := newRecorder()
	c, srv := testClient(t, discoveryHandler(rec, meetingPolicyJSON))
	defer srv.Close()

	_, err := c.Invoke(context.Background(),
		Op{CmdletName: "Remove-CsConfiguration", Transport: MPA, Kind: PolicyRemove, PolicyName: "MissingPolicy"},
		map[string]any{"Identity": "Nope"})
	if err == nil {
		t.Fatal("expected an error for a 404")
	}
	if !IsNotFound(err) {
		t.Fatalf("IsNotFound = false for %v", err)
	}
}

// TestFilterByIdentityTagPrefix covers the scope-prefix normalisation: custom
// policy instances are stored as "Tag:<name>" but callers pass the bare name, so
// a query for "X" must also match "Tag:X" (while "Global" stays exact).
func TestFilterByIdentityTagPrefix(t *testing.T) {
	in := []map[string]any{
		{"Identity": "Global"},
		{"Identity": "Tag:Default"},
		{"Identity": "Tag:MyPolicy"},
	}
	for query, want := range map[string]string{
		"MyPolicy":     "Tag:MyPolicy", // bare name matches the scoped instance
		"Tag:MyPolicy": "Tag:MyPolicy", // already-scoped matches directly
		"Default":      "Tag:Default",
		"Global":       "Global", // built-in is not Tag-scoped
	} {
		got := filterByIdentity(in, query)
		if len(got) != 1 || got[0]["Identity"] != want {
			t.Errorf("filterByIdentity(%q) = %v, want single %q", query, got, want)
		}
	}
	if got := filterByIdentity(in, "Nonexistent"); len(got) != 0 {
		t.Errorf("filterByIdentity(Nonexistent) = %v, want empty", got)
	}
}

// TestPolicyGrantRoutes covers the grant/assignment routing: the policy TYPE and
// target live in the PATH (decompiled {User,Group,Global}GrantPolicy), and the
// body carries only the instance name (plus Rank for groups) — never PolicyType.
func TestPolicyGrantRoutes(t *testing.T) {
	var c Client
	op := Op{Kind: PolicyGrant, PolicyName: "TeamsMeetingPolicy"}
	cases := []struct {
		name, wantPath string
		params         map[string]any
	}{
		{"user", "/Skype.Policy/users/user@x/policies/TeamsMeetingPolicy",
			map[string]any{"Identity": "user@x", "PolicyName": "Tag:Foo"}},
		{"group", "/Skype.Policy/groupPolicyAssignments/g1/policyTypes/TeamsMeetingPolicy",
			map[string]any{"Group": "g1", "PolicyName": "Tag:Foo", "Rank": int64(1)}},
		{"global", "/Skype.Policy/tenants/policies/TeamsMeetingPolicy",
			map[string]any{"Global": true, "PolicyName": "Tag:Foo"}},
	}
	for _, tc := range cases {
		m, p, body, err := c.resolve(op, tc.params)
		if err != nil {
			t.Fatalf("%s: resolve: %v", tc.name, err)
		}
		if m != http.MethodPatch {
			t.Errorf("%s: method = %s, want PATCH", tc.name, m)
		}
		if p != tc.wantPath {
			t.Errorf("%s: path = %s, want %s", tc.name, p, tc.wantPath)
		}
		var b map[string]any
		if err := json.Unmarshal(body, &b); err != nil {
			t.Fatalf("%s: body not JSON: %v", tc.name, err)
		}
		if _, ok := b["PolicyType"]; ok {
			t.Errorf("%s: body must not carry PolicyType (it is in the path): %s", tc.name, body)
		}
		if b["PolicyName"] != "Tag:Foo" {
			t.Errorf("%s: body missing PolicyName: %s", tc.name, body)
		}
	}
}

// Body decoding (br/gzip/deflate) is covered by go-msadmin/httpx tests.
