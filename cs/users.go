package cs

import (
	"context"
	"strings"

	"github.com/terraprovider/go-teams/teamsapi"
)

// EffectiveUserPolicy returns the name of the policy instance of type policyType
// that is effectively assigned to a user, as reported by Get-CsOnlineUser.
//
// user is matched, case-insensitively, against the user's objectId or
// userPrincipalName. policyType is the API policy-type discriminator (e.g.
// "TeamsMeetingPolicy"). The returned name is the bare instance name (no "Tag:"
// scope prefix) — exactly what Grant-Cs<Type>Policy -PolicyName expects. It is
// empty when the user has no explicit assignment for that type (i.e. inherits the
// tenant-global default) or when the user is not found.
//
// The assignment is read from the user's effectivePolicyAssignments collection:
//
//	{ "policyType": "<type>",
//	  "policyAssignment": { "assignmentType": "Direct", "displayName": "<name>", ... } }
//
// Per-user grants are asynchronous: after Grant-Cs<Type>Policy the assignment
// typically takes ~30-45s to surface here, so callers needing read-your-write
// consistency should poll (see tf-msadmin/resourcex + go-msadmin/consistency).
func (s *Service) EffectiveUserPolicy(ctx context.Context, user, policyType string) (string, error) {
	res, err := s.GetCsOnlineUser(ctx, GetCsOnlineUserParams{})
	if err != nil {
		return "", err
	}
	return effectiveUserPolicy(usersOf(res), user, policyType), nil
}

// usersOf normalises a Get-CsOnlineUser result into the list of user objects.
// The API returns a single { "users": [ ... ] } envelope; a bare array is also
// tolerated.
func usersOf(res *teamsapi.Result) []map[string]any {
	if res == nil {
		return nil
	}
	if len(res.Value) == 1 {
		if raw, ok := res.Value[0]["users"].([]any); ok {
			out := make([]map[string]any, 0, len(raw))
			for _, e := range raw {
				if m, ok := e.(map[string]any); ok {
					out = append(out, m)
				}
			}
			return out
		}
	}
	return res.Value
}

// effectiveUserPolicy is the pure filtering logic behind EffectiveUserPolicy,
// separated so it can be unit-tested without a live client.
func effectiveUserPolicy(users []map[string]any, user, policyType string) string {
	for _, u := range users {
		if !userMatches(u, user) {
			continue
		}
		epa, _ := u["effectivePolicyAssignments"].([]any)
		for _, e := range epa {
			m, ok := e.(map[string]any)
			if !ok {
				continue
			}
			if pt, _ := m["policyType"].(string); !strings.EqualFold(pt, policyType) {
				continue
			}
			pa, _ := m["policyAssignment"].(map[string]any)
			return firstString(pa, "displayName", "policyName", "name")
		}
		return "" // user found, no explicit assignment for this type
	}
	return "" // user not found
}

// userMatches reports whether the user object identifies the wanted user by
// objectId or userPrincipalName (case-insensitive).
func userMatches(u map[string]any, want string) bool {
	for _, k := range []string{"objectId", "userPrincipalName", "Identity"} {
		if s, ok := u[k].(string); ok && strings.EqualFold(s, want) {
			return true
		}
	}
	return false
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}
