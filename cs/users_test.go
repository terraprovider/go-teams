package cs

import (
	"encoding/json"
	"testing"

	"github.com/terraprovider/go-teams/teamsapi"
)

// sampleUsersResult mirrors the live Get-CsOnlineUser response shape validated
// against the dev tenant: a single { "users": [ ... ] } envelope, each user with
// objectId / userPrincipalName and an effectivePolicyAssignments array whose
// entries nest the instance name at policyAssignment.displayName (bare name, no
// "Tag:" prefix). A user with no explicit assignment has an empty array.
func sampleUsersResult(t *testing.T) *teamsapi.Result {
	t.Helper()
	const raw = `{
	  "users": [
	    {
	      "objectId": "0c34477f-6990-4c83-b505-e9695548b962",
	      "userPrincipalName": "cloudadmin@example.onmicrosoft.com",
	      "effectivePolicyAssignments": [
	        {
	          "policyType": "TeamsMeetingPolicy",
	          "policyAssignment": {
	            "assignmentType": "Direct",
	            "authority": "Tenant",
	            "displayName": "tfprobe-123",
	            "policyId": "Not Yet Supported."
	          }
	        },
	        {
	          "policyType": "TeamsMessagingPolicy",
	          "policyAssignment": { "assignmentType": "Direct", "displayName": "RestrictedMsg" }
	        }
	      ]
	    },
	    {
	      "objectId": "11111111-1111-1111-1111-111111111111",
	      "userPrincipalName": "noassign@example.onmicrosoft.com",
	      "effectivePolicyAssignments": []
	    }
	  ]
	}`
	var o map[string]any
	if err := json.Unmarshal([]byte(raw), &o); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	return &teamsapi.Result{Value: []map[string]any{o}}
}

func TestEffectiveUserPolicyFiltering(t *testing.T) {
	users := usersOf(sampleUsersResult(t))
	if len(users) != 2 {
		t.Fatalf("usersOf: want 2 users, got %d", len(users))
	}

	cases := []struct {
		name       string
		user       string
		policyType string
		want       string
	}{
		{"by objectId", "0c34477f-6990-4c83-b505-e9695548b962", "TeamsMeetingPolicy", "tfprobe-123"},
		{"by upn", "cloudadmin@example.onmicrosoft.com", "TeamsMeetingPolicy", "tfprobe-123"},
		{"upn case-insensitive", "CloudAdmin@EXAMPLE.onmicrosoft.com", "TeamsMeetingPolicy", "tfprobe-123"},
		{"policyType case-insensitive", "cloudadmin@example.onmicrosoft.com", "teamsmeetingpolicy", "tfprobe-123"},
		{"other policy type", "0c34477f-6990-4c83-b505-e9695548b962", "TeamsMessagingPolicy", "RestrictedMsg"},
		{"type not assigned -> empty", "0c34477f-6990-4c83-b505-e9695548b962", "TeamsCallingPolicy", ""},
		{"user with no assignments -> empty", "noassign@example.onmicrosoft.com", "TeamsMeetingPolicy", ""},
		{"unknown user -> empty", "does-not-exist", "TeamsMeetingPolicy", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := effectiveUserPolicy(users, tc.user, tc.policyType); got != tc.want {
				t.Errorf("effectiveUserPolicy(%q, %q) = %q, want %q", tc.user, tc.policyType, got, tc.want)
			}
		})
	}
}

// TestUsersOfBareArray covers the fallback where the API returns the user list as
// a bare array rather than the { "users": [...] } envelope.
func TestUsersOfBareArray(t *testing.T) {
	res := &teamsapi.Result{Value: []map[string]any{
		{
			"objectId":                   "abc",
			"effectivePolicyAssignments": []any{map[string]any{"policyType": "TeamsMeetingPolicy", "policyAssignment": map[string]any{"displayName": "P1"}}},
		},
	}}
	if got := effectiveUserPolicy(usersOf(res), "abc", "TeamsMeetingPolicy"); got != "P1" {
		t.Errorf("bare-array fallback: got %q, want %q", got, "P1")
	}
}
