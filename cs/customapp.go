package cs

import (
	"context"
	"fmt"

	"github.com/terraprovider/go-teams/teamsapi"
)

// SetCsTeamsSettingsCustomAppParams are the bound parameters for
// Set-CsTeamsSettingsCustomApp. Only isSideloadedAppsInteractionEnabled is
// operator-controlled; the rest of the tenant-wide app-settings object is
// preserved from the current value (see SetCsTeamsSettingsCustomApp).
type SetCsTeamsSettingsCustomAppParams struct {
	IsSideloadedAppsInteractionEnabled *bool    `ps:"isSideloadedAppsInteractionEnabled"`
	HttpPipelinePrepend                []string `ps:"HttpPipelinePrepend"`
}

// SetCsTeamsSettingsCustomApp updates the tenant-wide custom-app settings.
//
// This is a hand-written override of the autorest binding (gen-go skips it — see
// customCmdlets there) that mirrors the module's custom Set-CsTeamsSettingsCustomApp
// wrapper: the MiddletierService PUT to /tenantWideAppsSettingsGlobal replaces the
// WHOLE object and rejects a partial body with
//
//	400 {"errorCode":"BadRequest","message":"body of the tenantWideAppsSettings cannot be null"}
//
// so a single-field write is not possible. Instead it reads the current settings,
// overlays isSideloadedAppsInteractionEnabled, and PUTs the full object. The other
// fields are carried over verbatim; appSettingsList is sent empty (the app list is
// managed by separate cmdlets) — exactly what the module's wrapper does.
//
// The write itself is authoritative, but the setting's READ is replica-inconsistent:
// a GET after the PUT load-balances across backend replicas that converge at
// different rates, so successive reads flap between the old and new value for a long
// time (observed still flapping >60s later). There is therefore no reliable
// read-your-write; callers must treat the written value as the source of truth rather
// than reading it back (the Terraform provider marks the attribute WriteOnly).
func (s *Service) SetCsTeamsSettingsCustomApp(ctx context.Context, p SetCsTeamsSettingsCustomAppParams) (*teamsapi.Result, error) {
	cur, err := s.GetCsTeamsSettingsCustomApp(ctx, GetCsTeamsSettingsCustomAppParams{})
	if err != nil {
		return nil, fmt.Errorf("Set-CsTeamsSettingsCustomApp: read current settings: %w", err)
	}
	var current map[string]any
	if cur != nil && len(cur.Value) > 0 {
		current = cur.Value[0]
	}
	body := customAppUpdateBody(current, p.IsSideloadedAppsInteractionEnabled)
	return s.C.Invoke(ctx, teamsapi.Op{
		CmdletName: "Set-CsTeamsSettingsCustomApp",
		Transport:  teamsapi.ConfigAPI,
		Kind:       teamsapi.AutoRest,
		Method:     "PUT",
		Path:       "/Teams.MiddletierService/tenantWideAppsSettingsGlobal",
	}, body)
}

// customAppUpdateBody builds the full PUT body for the tenant-wide app settings
// from the current (GET) object, overlaying isSideloadedAppsInteractionEnabled.
//
// It reproduces the module wrapper's field set and the autorest write model's
// exact JSON keys. Note two casing quirks the API imposes: the GET response uses
// camelCase lob* keys (lobTextColor, …) but the PUT body expects PascalCase
// (LobTextColor, …); appSettingsList is always sent empty. Fields are copied only
// when present, matching the write model's add-if-non-null serialization.
func customAppUpdateBody(current map[string]any, isSideloaded *bool) map[string]any {
	body := map[string]any{
		// appSettingsList is intentionally emptied on write (managed elsewhere).
		"appSettingsList": []any{},
	}
	// Carry over the current values (GET key -> PUT key).
	carry := []struct{ from, to string }{
		{"isAppsEnabled", "isAppsEnabled"},
		{"isAppsPurchaseEnabled", "isAppsPurchaseEnabled"},
		{"isExternalAppsEnabledByDefault", "isExternalAppsEnabledByDefault"},
		{"isLicenseBasedPinnedAppsEnabled", "isLicenseBasedPinnedAppsEnabled"},
		{"isTenantWideAutoInstallEnabled", "isTenantWideAutoInstallEnabled"},
		{"lobTextColor", "LobTextColor"},
		{"lobBackground", "LobBackground"},
		{"lobLogo", "LobLogo"},
		{"lobLogomark", "LobLogomark"},
		{"appAccessRequestConfig", "appAccessRequestConfig"},
	}
	for _, c := range carry {
		if v, ok := current[c.from]; ok && v != nil {
			body[c.to] = v
		}
	}
	// Overlay the operator-controlled field; fall back to the current value so a
	// nil param (nothing to change) still produces a valid full-object write.
	switch {
	case isSideloaded != nil:
		body["isSideloadedAppsInteractionEnabled"] = *isSideloaded
	default:
		if v, ok := current["isSideloadedAppsInteractionEnabled"]; ok && v != nil {
			body["isSideloadedAppsInteractionEnabled"] = v
		}
	}
	return body
}
