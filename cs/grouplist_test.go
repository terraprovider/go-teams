package cs

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/terraprovider/go-teams/teamsapi"
)

func TestGroupPolicyAssignments(t *testing.T) {
	// Unordered, mixed types (the server ignores an unknown query parameter and
	// then returns every type), mixed casing, a "Tag:" prefix.
	rows := rowsOf(t, `[
	  {"groupId":"g3","policyType":"TeamsMeetingPolicy","policyName":"C","priority":3},
	  {"groupId":"g1","policyType":"TeamsMeetingPolicy","policyName":"Tag:A","priority":1},
	  {"groupId":"g9","policyType":"TeamsMessagingPolicy","policyName":"Chat","priority":1},
	  {"GroupId":"g2","PolicyType":"teamsmeetingpolicy","PolicyName":"B","Priority":"2"}
	]`)
	got, err := groupPolicyAssignments(rows, "TeamsMeetingPolicy")
	if err != nil {
		t.Fatal(err)
	}
	want := []GroupAssignment{{"g1", "A", 1}, {"g2", "B", 2}, {"g3", "C", 3}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if got, err := groupPolicyAssignments(rowsOf(t, `[]`), "TeamsMeetingPolicy"); err != nil || len(got) != 0 {
		t.Errorf("empty: got %v, %v", got, err)
	}
}

func TestGroupPolicyAssignmentsErrors(t *testing.T) {
	cases := map[string]string{
		"duplicate group":  `[{"groupId":"g1","policyType":"T","policyName":"A","priority":1},{"groupId":"G1","policyType":"T","policyName":"B","priority":2}]`,
		"missing group":    `[{"policyType":"T","policyName":"A","priority":1}]`,
		"missing name":     `[{"groupId":"g1","policyType":"T","priority":1}]`,
		"missing priority": `[{"groupId":"g1","policyType":"T","policyName":"A"}]`,
		"zero priority":    `[{"groupId":"g1","policyType":"T","policyName":"A","priority":0}]`,
		"fractional":       `[{"groupId":"g1","policyType":"T","policyName":"A","priority":1.5}]`,
	}
	for name, raw := range cases {
		if _, err := groupPolicyAssignments(rowsOf(t, raw), "T"); err == nil {
			t.Errorf("%s: want error, got nil", name)
		}
	}
	// A bad row of another type is filtered out before it is checked.
	if _, err := groupPolicyAssignments(rowsOf(t, `[{"groupId":"g1","policyType":"Other"}]`), "T"); err != nil {
		t.Errorf("other type: %v", err)
	}
}

// listServer serves the service discovery and the group assignment list, and
// records the list request's query.
func listServer(t *testing.T, status int, body string) (*Service, *string) {
	t.Helper()
	var mu sync.Mutex
	var query string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/Teams.Tenant/serviceDiscovery":
			io.WriteString(w, `{"Endpoints":{"ConfigApiEndpoint":"cfg","AdminServiceEndpoint":"admintest.example"},"Headers":{}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/Skype.Policy/groupPolicyAssignments":
			mu.Lock()
			query = r.URL.RawQuery
			mu.Unlock()
			w.WriteHeader(status)
			io.WriteString(w, body)
		default:
			http.Error(w, `{"error":{"code":"NotFound","message":"no route"}}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	c, err := teamsapi.New(teamsapi.Options{
		Environment: teamsapi.Environment{Name: "Test", Endpoint: strings.TrimPrefix(srv.URL, "https://"), Resource: "res"},
		TenantID:    "tid",
		Tokens:      teamsapi.StaticTokenProvider("tok"),
		HTTPClient:  srv.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return New(c), &query
}

func TestGroupPolicyAssignmentsRequest(t *testing.T) {
	s, query := listServer(t, http.StatusOK, `[
	  {"groupId":"g2","policyType":"TeamsMeetingPolicy","policyName":"B","priority":2},
	  {"groupId":"g1","policyType":"TeamsMeetingPolicy","policyName":"A","priority":1}
	]`)
	n, err := s.GroupPolicyAssignmentCount(context.Background(), "TeamsMeetingPolicy")
	if err != nil || n != 2 {
		t.Fatalf("count: got %d, %v; want 2", n, err)
	}
	if *query != "policyType=TeamsMeetingPolicy" {
		t.Errorf("query: got %q", *query)
	}
	if _, err := s.GroupPolicyAssignments(context.Background(), ""); err == nil {
		t.Error("empty policyType: want error")
	}
}

func TestGroupPolicyAssignmentsNotFoundIsError(t *testing.T) {
	s, _ := listServer(t, http.StatusNotFound, `{"error":{"code":"NotFound","message":"gone"}}`)
	if _, err := s.GroupPolicyAssignmentCount(context.Background(), "TeamsMeetingPolicy"); !teamsapi.IsNotFound(err) {
		t.Errorf("got %v, want a 404 error", err)
	}
}
