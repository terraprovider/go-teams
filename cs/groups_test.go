package cs

import (
	"encoding/json"
	"testing"
)

// rowsOf decodes a JSON array the way teamsapi does (numbers arrive as float64).
func rowsOf(t *testing.T, raw string) []map[string]any {
	t.Helper()
	var rows []map[string]any
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		t.Fatalf("bad fixture: %v", err)
	}
	return rows
}

// collection mirrors the live GET …/groupPolicyAssignments/ response: a bare
// array over every group and policy type, camelCase fields, one-based priority,
// policyName without the "Tag:" prefix.
const collection = `[
  {"groupId":"11111111-1111-1111-1111-111111111111","policyType":"TeamsMeetingPolicy","policyName":"RetailManagers","priority":1,"createdBy":"x","createdTime":"2026-11-19T10:00:00Z"},
  {"groupId":"22222222-2222-2222-2222-222222222222","policyType":"TeamsMeetingPolicy","policyName":"Tag:Students","priority":2,"createdBy":"x","createdTime":"2026-11-19T10:00:00Z"},
  {"groupId":"11111111-1111-1111-1111-111111111111","policyType":"TeamsMessagingPolicy","policyName":"Chat","priority":1,"createdBy":"x","createdTime":"2026-11-19T10:00:00Z"}
]`

func TestGroupPolicyAssignment(t *testing.T) {
	rows := rowsOf(t, collection)
	cases := []struct {
		name, group, ptype, wantName string
		wantRank                     int64
	}{
		{"match", "11111111-1111-1111-1111-111111111111", "TeamsMeetingPolicy", "RetailManagers", 1},
		{"case-insensitive", "11111111-1111-1111-1111-111111111111", "teamsmessagingpolicy", "Chat", 1},
		{"Tag: stripped", "22222222-2222-2222-2222-222222222222", "TeamsMeetingPolicy", "Students", 2},
		{"no assignment for type", "22222222-2222-2222-2222-222222222222", "TeamsMessagingPolicy", "", 0},
		{"unknown group", "33333333-3333-3333-3333-333333333333", "TeamsMeetingPolicy", "", 0},
	}
	for _, tc := range cases {
		name, rank, err := groupPolicyAssignment(rows, tc.group, tc.ptype)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if name != tc.wantName || rank != tc.wantRank {
			t.Errorf("%s: got (%q, %d), want (%q, %d)", tc.name, name, rank, tc.wantName, tc.wantRank)
		}
	}
}

func TestGroupPolicyAssignmentSingleRow(t *testing.T) {
	// The targeted route may return one object; teamsapi normalises it to one row.
	rows := []map[string]any{{"GroupId": "g1", "PolicyType": "TeamsMeetingPolicy", "PolicyName": "Foo", "Priority": float64(3)}}
	name, rank, err := groupPolicyAssignment(rows, "g1", "TeamsMeetingPolicy")
	if err != nil || name != "Foo" || rank != 3 {
		t.Fatalf("got (%q, %d, %v), want (\"Foo\", 3, nil)", name, rank, err)
	}
}

func TestGroupPolicyAssignmentErrors(t *testing.T) {
	cases := map[string]string{
		"duplicate":        `[{"groupId":"g1","policyType":"T","policyName":"A","priority":1},{"groupId":"g1","policyType":"T","policyName":"B","priority":2}]`,
		"missing name":     `[{"groupId":"g1","policyType":"T","priority":1}]`,
		"missing priority": `[{"groupId":"g1","policyType":"T","policyName":"A"}]`,
		"zero priority":    `[{"groupId":"g1","policyType":"T","policyName":"A","priority":0}]`,
		"fractional":       `[{"groupId":"g1","policyType":"T","policyName":"A","priority":1.5}]`,
	}
	for name, raw := range cases {
		if _, _, err := groupPolicyAssignment(rowsOf(t, raw), "g1", "T"); err == nil {
			t.Errorf("%s: want error, got nil", name)
		}
	}
}
