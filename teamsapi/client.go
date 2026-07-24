// Package teamsapi is a lean Go client for the Microsoft Teams admin
// configuration REST API — the service behind the MicrosoftTeams PowerShell
// module (Connect-MicrosoftTeams / the -Cs* cmdlets).
//
// It reproduces the module's wire behaviour so traffic is indistinguishable from
// the PowerShell client: the connect handshake (GET /Teams.Tenant/serviceDiscovery
// → X-MS-Target-Uri routing) and the module's two distinct HTTP fingerprints — the
// ConfigAPI/AutoRest client and the MPA/PolicyRp client (see docs 05/06 of
// teams-powershell-api-re). Policy/config CRUD runs over
// /Skype.Policy/configurations/{PolicyName}.
//
// The core is intentionally thin: transport pooling and retry are delegated to the
// *http.Client you pass in (wrap it with go-msadmin/retry). Auth is abstracted
// behind TokenProvider — an MSAL-backed implementation lives in go-exoscc/msalauth,
// wired by the caller (or the Terraform provider via tf-msadmin/authschema), so
// this package has no dependency on the sibling libraries beyond go-msadmin.
package teamsapi

import (
	"fmt"
	"net/http"
	"sync"

	"github.com/terraprovider/go-msadmin/auth"
	"github.com/terraprovider/go-msadmin/retry"
)

// Environment selects the service endpoint and token audience for a cloud.
// Values are the derived facts from teams-powershell-api-re/spec/environments.json;
// the token resource is identical across all production clouds — only the host
// differs.
type Environment struct {
	Name     string // "Public", "GCCH", "DoD", "Gallatin", "Bleu", "Delos"
	Endpoint string // config API host, e.g. api.interfaces.records.teams.microsoft.com
	Resource string // token audience GUID (request {Resource}/.default)
}

const teamsAdminResource = "48ac35b8-9aa8-4d74-927d-1f4a14a0b239"

var (
	// Public is the commercial cloud.
	Public = Environment{Name: "Public", Endpoint: "api.interfaces.records.teams.microsoft.com", Resource: teamsAdminResource}
	// GCCH is US Government Community Cloud High.
	GCCH = Environment{Name: "GCCH", Endpoint: "api.interfaces.records.gov.teams.microsoft.us", Resource: teamsAdminResource}
	// DoD is US Department of Defense.
	DoD = Environment{Name: "DoD", Endpoint: "api.interfaces.records.dod.teams.microsoft.us", Resource: teamsAdminResource}
	// Gallatin is the 21Vianet (China) cloud.
	Gallatin = Environment{Name: "Gallatin", Endpoint: "api.interfaces.records.teams.microsoftonline.cn", Resource: teamsAdminResource}
	// Bleu is the France sovereign cloud.
	Bleu = Environment{Name: "Bleu", Endpoint: "api.interfaces.records.communications.svc.sovcloud.fr", Resource: teamsAdminResource}
	// Delos is the Germany sovereign cloud.
	Delos = Environment{Name: "Delos", Endpoint: "api.interfaces.records.communications.svc.sovcloud.de", Resource: teamsAdminResource}
)

// TokenProvider is the shared token abstraction (go-msadmin/auth): it returns a
// bearer token whose audience matches Environment.Resource. An
// msalauth.Confidential/Delegated (go-exoscc/msalauth) or the result of
// authx.Config.Build() satisfies it.
type TokenProvider = auth.TokenProvider

// StaticTokenProvider serves a fixed pre-acquired JWT (handy for tests/scripts).
type StaticTokenProvider = auth.StaticToken

// Default header-fidelity versions, pinned to MicrosoftTeams 7.9.0. These are the
// exact strings the module emits in the User-Agent (see docs 05/06). Refresh them
// alongside the module version.
const (
	DefaultModuleVersion    = "7.9.0"
	DefaultConfigAPIVersion = "9.714.2" // Microsoft.Teams.ConfigAPI.Cmdlets assembly
	DefaultMPAVersion       = "0.0.11"  // Microsoft.Teams.Policy.Administration
)

// Options configures a Client.
type Options struct {
	Environment Environment   // defaults to Public
	TenantID    string        // tenant GUID (from the token 'tid' claim); required
	Tokens      TokenProvider // required
	HTTPClient  *http.Client  // optional; wrap with go-msadmin/retry. A plain client is used if nil.

	// Header-fidelity knobs (defaults mimic MicrosoftTeams 7.9.0).
	ModuleVersion    string
	ConfigAPIVersion string
	MPAVersion       string
}

// Client talks to one cloud in one tenant. Safe for concurrent use.
type Client struct {
	opt  Options
	http *http.Client

	// discovery: learned from /Teams.Tenant/serviceDiscovery and pinned. Retries
	// until it succeeds (a transient failure does not poison the client).
	disc       sync.Mutex
	discovered bool
	targetURI  string // https://{AdminServiceEndpoint}/  -> X-MS-Target-Uri
	forest     string // X-MS-Forest
}

// New builds a Client. TenantID and Tokens are required.
func New(opt Options) (*Client, error) {
	if opt.Tokens == nil {
		return nil, fmt.Errorf("teamsapi: Options.Tokens is required")
	}
	if opt.TenantID == "" {
		return nil, fmt.Errorf("teamsapi: Options.TenantID is required")
	}
	if opt.Environment.Endpoint == "" {
		opt.Environment = Public
	}
	if opt.HTTPClient == nil {
		// These APIs throttle with 429/Retry-After and return transient 5xx under
		// load; default to the go-msadmin retry transport. Callers can override.
		opt.HTTPClient = &http.Client{Transport: retry.NewTransport(nil, retry.Config{})}
	}
	if opt.ModuleVersion == "" {
		opt.ModuleVersion = DefaultModuleVersion
	}
	if opt.ConfigAPIVersion == "" {
		opt.ConfigAPIVersion = DefaultConfigAPIVersion
	}
	if opt.MPAVersion == "" {
		opt.MPAVersion = DefaultMPAVersion
	}
	return &Client{opt: opt, http: opt.HTTPClient}, nil
}

func (c *Client) baseURL() string { return "https://" + c.opt.Environment.Endpoint }
