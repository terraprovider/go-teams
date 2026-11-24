package cs

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
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

// GroupAssignment is one group's assignment of a policy type: the bare instance
// name (no "Tag:" prefix) and its one-based rank among that type's group
// assignments.
type GroupAssignment struct {
	GroupID    string
	PolicyName string
	Rank       int64
}

// GroupPolicyAssignments lists the group assignments of policyType, sorted by
// rank (then group ID).
//
// It reads GET /Skype.Policy/groupPolicyAssignments?policyType={policyType}: a
// bare array of the rows GroupPolicyAssignment describes, unordered and without
// paging (live, 2026-11). The server ignores an unknown query parameter and then
// returns every type, so rows are filtered by policyType here as well
// (case-insensitively). An empty list is [], not 404, so a 404 is an error.
//
// Like GroupPolicyAssignment, the list follows grants asynchronously.
func (s *Service) GroupPolicyAssignments(ctx context.Context, policyType string) ([]GroupAssignment, error) {
	if policyType == "" {
		return nil, fmt.Errorf("cs: GroupPolicyAssignments: policyType is required")
	}
	res, err := s.C.Invoke(ctx, teamsapi.Op{
		CmdletName: "Get-CsGroupPolicyAssignment",
		Transport:  teamsapi.ConfigAPI,
		Kind:       teamsapi.AutoRest,
		Method:     http.MethodGet,
		Path:       "/Skype.Policy/groupPolicyAssignments?policyType=" + url.QueryEscape(policyType),
	}, nil)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, nil
	}
	return groupPolicyAssignments(res.Value, policyType)
}

// GroupPolicyAssignmentCount returns the number of group assignments of
// policyType: the highest rank a grant can reach without being placed last
// (one more for a group that has no assignment of the type yet).
func (s *Service) GroupPolicyAssignmentCount(ctx context.Context, policyType string) (int64, error) {
	list, err := s.GroupPolicyAssignments(ctx, policyType)
	return int64(len(list)), err
}

// groupPolicyAssignments is the decoding logic behind GroupPolicyAssignments.
func groupPolicyAssignments(rows []map[string]any, policyType string) ([]GroupAssignment, error) {
	var out []GroupAssignment
	seen := map[string]bool{}
	for _, row := range rows {
		if !strings.EqualFold(stringField(row, "policyType"), policyType) {
			continue
		}
		g := stringField(row, "groupId")
		if g == "" {
			return nil, fmt.Errorf("cs: group policy assignment of %s has no groupId", policyType)
		}
		if seen[strings.ToLower(g)] {
			return nil, fmt.Errorf("cs: multiple group policy assignments for %s/%s", g, policyType)
		}
		seen[strings.ToLower(g)] = true
		name := stripTag(stringField(row, "policyName"))
		if name == "" {
			return nil, fmt.Errorf("cs: group policy assignment %s/%s has no policyName", g, policyType)
		}
		rank, ok := int64Field(row, "priority")
		if !ok || rank < 1 {
			return nil, fmt.Errorf("cs: group policy assignment %s/%s has no valid priority (one-based rank)", g, policyType)
		}
		out = append(out, GroupAssignment{GroupID: g, PolicyName: name, Rank: rank})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Rank != out[j].Rank {
			return out[i].Rank < out[j].Rank
		}
		return strings.ToLower(out[i].GroupID) < strings.ToLower(out[j].GroupID)
	})
	return out, nil
}
