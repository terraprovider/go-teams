package cs

import (
	"reflect"
	"testing"
)

func TestCustomAppUpdateBody(t *testing.T) {
	// A representative GET response: camelCase keys, empty lob* strings, a populated
	// appSettingsList (which must be dropped), no appAccessRequestConfig.
	current := map[string]any{
		"isAppsEnabled":                      true,
		"isAppsPurchaseEnabled":              true,
		"isExternalAppsEnabledByDefault":     true,
		"isLicenseBasedPinnedAppsEnabled":    false,
		"isTenantWideAutoInstallEnabled":     false,
		"isSideloadedAppsInteractionEnabled": true,
		"lobTextColor":                       "",
		"lobBackground":                      "#fff",
		"lobLogo":                            "",
		"lobLogomark":                        "",
		"appSettingsList":                    []any{map[string]any{"id": "x"}},
	}

	set := false
	got := customAppUpdateBody(current, &set)

	want := map[string]any{
		"isAppsEnabled":                      true,
		"isAppsPurchaseEnabled":              true,
		"isExternalAppsEnabledByDefault":     true,
		"isLicenseBasedPinnedAppsEnabled":    false,
		"isTenantWideAutoInstallEnabled":     false,
		"isSideloadedAppsInteractionEnabled": false, // overlaid
		"LobTextColor":                       "",    // camelCase GET -> PascalCase PUT
		"LobBackground":                      "#fff",
		"LobLogo":                            "",
		"LobLogomark":                        "",
		"appSettingsList":                    []any{}, // emptied on write
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("body mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

func TestCustomAppUpdateBody_OmitsAbsentAndKeepsCurrentWhenNil(t *testing.T) {
	// Missing optional fields must not appear; a nil override keeps the current value.
	current := map[string]any{
		"isAppsEnabled":                      true,
		"isSideloadedAppsInteractionEnabled": true,
		// no lob*, no appAccessRequestConfig, no other bools
	}
	got := customAppUpdateBody(current, nil)

	if _, ok := got["appAccessRequestConfig"]; ok {
		t.Errorf("appAccessRequestConfig should be omitted when absent from current: %#v", got)
	}
	if _, ok := got["LobTextColor"]; ok {
		t.Errorf("LobTextColor should be omitted when absent from current: %#v", got)
	}
	if got["isSideloadedAppsInteractionEnabled"] != true {
		t.Errorf("nil override should keep current value, got %#v", got["isSideloadedAppsInteractionEnabled"])
	}
	if !reflect.DeepEqual(got["appSettingsList"], []any{}) {
		t.Errorf("appSettingsList should always be an empty array, got %#v", got["appSettingsList"])
	}
}

func TestCustomAppUpdateBody_CarriesAppAccessRequestConfig(t *testing.T) {
	cfg := map[string]any{"isEnabled": true}
	got := customAppUpdateBody(map[string]any{"appAccessRequestConfig": cfg}, nil)
	if !reflect.DeepEqual(got["appAccessRequestConfig"], cfg) {
		t.Errorf("appAccessRequestConfig should be carried over, got %#v", got["appAccessRequestConfig"])
	}
}
