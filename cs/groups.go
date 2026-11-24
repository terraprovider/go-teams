package cs

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/terraprovider/go-teams/teamsapi"
)

// GroupPolicyAssignment returns the policy instance of type policyType assigned
// to the Entra group groupID, and its one-based rank among the group assignments
// of that type. name is the bare instance name (no "Tag:" scope prefix) — what
// Grant-Cs<Type> -Group -PolicyName expects; name == "" means the group has no
// assignment for that type.
//
// It reads GET /Skype.Policy/groupPolicyAssignments/{groupId}/policyTypes/{policyType}
// (the route the module's ConfigAPI uses for Get-CsGroupPolicyAssignment -GroupId
// -PolicyType). The generated GetCsGroupPolicyAssignment binding cannot be used:
// its catalog path is the bare collection, so GroupId and PolicyType are dropped.
// Rows are still matched by group and type (case-insensitively), so the helper is
// correct for a single object, an array, or the whole collection.
//
// A row looks like (live, MicrosoftTeams 7.9.0 / 8.0.0):
//
//	{ "groupId": "<id>", "policyType": "TeamsMeetingPolicy", "policyName": "<name>",
//	  "priority": 1, "createdBy": "...", "createdTime": "..." }
//
// Group grants are asynchronous and Teams keeps ranks dense per policy type
// (inserting at an occupied rank shifts the others down; an out-of-range rank is
// placed last), so callers needing read-your-write consistency should poll until
// both the name and the rank match.
func (s *Service) GroupPolicyAssignment(ctx context.Context, groupID, policyType string) (name string, rank int64, err error) {
	res, err := s.C.Invoke(ctx, teamsapi.Op{
		CmdletName: "Get-CsGroupPolicyAssignment",
		Transport:  teamsapi.ConfigAPI,
		Kind:       teamsapi.AutoRest,
		Method:     http.MethodGet,
		Path:       "/Skype.Policy/groupPolicyAssignments/" + url.PathEscape(groupID) + "/policyTypes/" + url.PathEscape(policyType),
	}, nil)
	if err != nil {
		if teamsapi.IsNotFound(err) {
			return "", 0, nil
		}
		return "", 0, err
	}
	if res == nil {
		return "", 0, nil
	}
	return groupPolicyAssignment(res.Value, groupID, policyType)
}

// groupPolicyAssignment is the pure decoding logic behind GroupPolicyAssignment,
// separated so it can be unit-tested without a live client.
func groupPolicyAssignment(rows []map[string]any, groupID, policyType string) (string, int64, error) {
	var name string
	var rank int64
	for _, row := range rows {
		if !strings.EqualFold(stringField(row, "groupId"), groupID) ||
			!strings.EqualFold(stringField(row, "policyType"), policyType) {
			continue
		}
		if name != "" {
			return "", 0, fmt.Errorf("cs: multiple group policy assignments for %s/%s", groupID, policyType)
		}
		name = stripTag(stringField(row, "policyName"))
		if name == "" {
			return "", 0, fmt.Errorf("cs: group policy assignment %s/%s has no policyName", groupID, policyType)
		}
		var ok bool
		if rank, ok = int64Field(row, "priority"); !ok || rank < 1 {
			return "", 0, fmt.Errorf("cs: group policy assignment %s/%s has no valid priority (one-based rank)", groupID, policyType)
		}
	}
	return name, rank, nil
}

// stripTag removes the "Tag:" scope prefix custom policy instances carry.
func stripTag(s string) string {
	if len(s) >= 4 && strings.EqualFold(s[:4], "Tag:") {
		return s[4:]
	}
	return s
}

// field looks key up exactly, then case-insensitively (the API's casing differs
// between routes and module versions).
func field(row map[string]any, key string) any {
	if v, ok := row[key]; ok {
		return v
	}
	for k, v := range row {
		if strings.EqualFold(k, key) {
			return v
		}
	}
	return nil
}

func stringField(row map[string]any, key string) string {
	s, _ := field(row, key).(string)
	return s
}

// int64Field reads an integer that JSON decoding delivers as float64 (or, from
// some routes, as a string).
func int64Field(row map[string]any, key string) (int64, bool) {
	switch v := field(row, key).(type) {
	case float64:
		return int64(v), v == float64(int64(v))
	case int64:
		return v, true
	case int:
		return int64(v), true
	case string:
		n, err := strconv.ParseInt(v, 10, 64)
		return n, err == nil
	}
	return 0, false
}
