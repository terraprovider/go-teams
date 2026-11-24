package teamsapi

import (
	"errors"
	"fmt"
	"testing"
)

// teamsBody is the Teams config API's error envelope for a 409.
func teamsBody(code string) string {
	return `{"code":"ClientError","message":"Identity has a create conflict","action":"Please check your request parameters.","errorCode":` + code + `,"innerError":{"ErrorCode":"Status: Conflict (409)"}}`
}

func TestParseAPIError(t *testing.T) {
	for name, c := range map[string]struct {
		status        int
		body          string
		code, message string
	}{
		"teams errorCode number":  {409, teamsBody("40904"), "40904", "Identity has a create conflict"},
		"teams errorCode string":  {409, teamsBody(`"40902"`), "40902", "Identity has a create conflict"},
		"teams errorCode null":    {409, teamsBody("null"), "ClientError", "Identity has a create conflict"},
		"teams without errorCode": {400, `{"code":"ClientError","message":"bad"}`, "ClientError", "bad"},
		"nested error":            {404, `{"error":{"code":"NotFound","message":"gone"}}`, "NotFound", "gone"},
		"message only":            {500, `{"Message":"boom"}`, "", "boom"},
		"not json":                {502, `<html>`, "", "<html>"},
		"empty":                   {500, ``, "", "(no response body)"},
	} {
		err := parseAPIError(c.status, []byte(c.body))
		var ae *APIError
		if !errors.As(err, &ae) {
			t.Fatalf("%s: not an APIError: %v", name, err)
		}
		if ae.Status != c.status || ae.Code != c.code || ae.Message != c.message || ae.Body != c.body {
			t.Errorf("%s: got status %d, code %q, message %q, body %q", name, ae.Status, ae.Code, ae.Message, ae.Body)
		}
	}
}

func TestIsWriteConflict(t *testing.T) {
	for name, c := range map[string]struct {
		err  error
		want bool
	}{
		"create conflict":  {parseAPIError(409, []byte(teamsBody("40904"))), true},
		"update conflict":  {parseAPIError(409, []byte(teamsBody("40902"))), true},
		"wrapped":          {fmt.Errorf("x: %w", parseAPIError(409, []byte(teamsBody("40902")))), true},
		"already exists":   {parseAPIError(409, []byte(teamsBody("40901"))), false},
		"longer code":      {parseAPIError(409, []byte(teamsBody("409041"))), false},
		"other status":     {parseAPIError(400, []byte(teamsBody("40904"))), false},
		"409 without code": {parseAPIError(409, []byte(`<html>`)), false},
		"not an APIError":  {errors.New("x"), false},
		"nil":              {nil, false},
	} {
		if got := IsWriteConflict(c.err); got != c.want {
			t.Errorf("%s: got %v, want %v", name, got, c.want)
		}
	}
}
