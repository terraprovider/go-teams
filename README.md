# go-teams

A Go client for the **Microsoft Teams admin configuration** REST API — the
service behind the `MicrosoftTeams` PowerShell module's `-Cs*` cmdlets
(`Connect-MicrosoftTeams`). Typed bindings are **generated** from a derived
catalog so the surface tracks the module; the transport reproduces the module's
exact wire behaviour so traffic is indistinguishable from the PowerShell client.

> Not affiliated with or endorsed by Microsoft. It calls the same documented admin
> API the PowerShell module does, over the same REST transport. Built on
> [`go-msadmin`](https://github.com/terraprovider/go-msadmin); the Terraform provider
> builds on [`tf-msadmin`](https://github.com/terraprovider/tf-msadmin).

```bash
go get github.com/terraprovider/go-teams
```

## Status

- **`teamsapi`** (transport core) — ✅ implemented, unit-tested, live-validated.
- **`spec`** (embedded, derived cmdlet catalog + CRUD-quad detection) — ✅.
- **`cmd/gen-go`** → **`cs`** (typed generated bindings) — ✅ for the in-scope
  org-wide policy/config surface (`New/Get/Set/Remove-Cs*`); driven end-to-end
  against the real API in `cmd/verify`. Expanding coverage + `models` next.

```go
svc := cs.New(client)
res, _ := svc.GetCsTeamsMeetingPolicy(ctx, cs.GetCsTeamsMeetingPolicyParams{})
```

The catalog is fed by the private `teams-powershell-api-re` factory
(`just publish ../go-teams`); `cmd/gen-go` regenerates `cs/zz_generated_cs.go`.

## `teamsapi` — the transport

```go
c, _ := teamsapi.New(teamsapi.Options{
    Environment: teamsapi.Public,          // or GCCH / DoD / Gallatin / Bleu / Delos
    TenantID:    tenantID,
    Tokens:      tokenProvider,            // any TokenProvider (see Auth)
})

// Read all instances of a policy type:
res, _ := c.Invoke(ctx, teamsapi.Op{
    CmdletName: "Get-CsConfiguration_Get", Transport: teamsapi.ConfigAPI,
    Kind: teamsapi.PolicyList, PolicyName: "TeamsMeetingPolicy",
}, nil)
// res.Value is []map[string]any; res.Decode(&[]models.TeamsMeetingPolicy{}) when models land.
```

It reproduces the module faithfully:

- **Connect handshake** — `GET /Teams.Tenant/serviceDiscovery` learns the regional
  admin backend, carried on every later call as **`X-MS-Target-Uri`** (Teams'
  routing mechanism; there is no 302/affinity cookie like Exchange).
- **Two wire fingerprints** — the module uses two HTTP stacks with distinct
  signatures, and `teamsapi` matches each per call via `Op.Transport`:
  - `ConfigAPI` — `User-Agent: Microsoft.Teams.ConfigAPI.Cmdlets/<ver>/<op>/MicrosoftTeams/<ver>`
  - `MPA` — `User-Agent: Microsoft.Teams.Policy.Administration/<ver>/<op>/MicrosoftTeams/<ver>`, `MPACmdlet: true`
- **Policy CRUD** over `/Skype.Policy/configurations/{PolicyName}` (`PolicyList` /
  `PolicyGet` / `PolicyNew` / `PolicySet` / `PolicyRemove`).
- `br`/`gzip`/`deflate` response decoding; `X-MS-Correlation-Id` per request;
  warnings surfaced from `X-Ms-ConfigApi-PowerShell-WarningMessage`;
  `teamsapi.IsNotFound(err)` for 404s. Throttling/`Retry-After`/5xx retries are the
  default via `go-msadmin/retry` (override `Options.HTTPClient`).

## Auth

`teamsapi.TokenProvider` is `Token(ctx, resource) (string, error)`. Anything
satisfying it works — an `go-exoscc/msalauth` `Confidential`/`Delegated`, the result
of `authx.Config.Build()`, or your own. App-only needs the app's service principal
to hold a **Teams admin directory role** (a 403 is a role gap, not a token problem).
`cmd/verify` shows a minimal client-credentials provider.

## Generation (planned)

Following the go-exoscc pipeline: the private `teams-powershell-api-re` factory
derives `spec/*.json` (cmdlet/op → method/path/fields, per-cloud environments);
`spec` exposes it typed + CRUD-quad detection; `cmd/gen-go` emits the `cs` bindings
(`New-CsTeamsMeetingPolicy` → `cs.NewCsTeamsMeetingPolicyParams` + `svc.NewCsTeamsMeetingPolicy`);
`terraform-provider-teams/cmd/gen-tf` feeds `tf-msadmin/genframework`. Only the
derived factual catalog is committed here — never decompiled Microsoft sources.

## License

[MIT](LICENSE) © glueckanja AG
