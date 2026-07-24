package cs

import (
	"context"
	"fmt"
	"time"

	"github.com/terraprovider/go-msadmin/consistency"
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
	res, err := s.C.Invoke(ctx, teamsapi.Op{
		CmdletName: "Set-CsTeamsSettingsCustomApp",
		Transport:  teamsapi.ConfigAPI,
		Kind:       teamsapi.AutoRest,
		Method:     "PUT",
		Path:       "/Teams.MiddletierService/tenantWideAppsSettingsGlobal",
	}, body)
	if err != nil {
		return nil, err
	}
	// The MiddletierService write is eventually consistent: a GET immediately after
	// the PUT can still return the old value for ~1-3s. Block until the read reflects
	// what we wrote so callers get read-your-write consistency (Terraform in
	// particular fails with "inconsistent result after apply" on a stale read-back).
	// Best-effort: the write already succeeded, so a lagging read is not fatal.
	if p.IsSideloadedAppsInteractionEnabled != nil {
		want := *p.IsSideloadedAppsInteractionEnabled
		get := func(ctx context.Context) (bool, bool, error) {
			r, e := s.GetCsTeamsSettingsCustomApp(ctx, GetCsTeamsSettingsCustomAppParams{})
			if e != nil {
				return false, false, e
			}
			if len(r.Value) == 0 {
				return false, false, nil
			}
			v, ok := r.Value[0]["isSideloadedAppsInteractionEnabled"].(bool)
			return v, ok, nil
		}
		_, _, _ = consistency.RetryUntil(ctx, consistency.Config{Attempts: 15, Delay: 2 * time.Second}, get,
			func(v bool) bool { return v == want })
	}
	return res, nil
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
