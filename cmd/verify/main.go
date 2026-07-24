// Command verify is a live smoke test for the teamsapi transport: it connects
// app-only, performs the serviceDiscovery handshake, and reads a few in-scope
// policy types — proving the wire fingerprint is accepted by the real API and the
// generated bindings decode it. Read-only.
//
// Credentials are resolved from the ARM_*/AZURE_* environment via go-msadmin/authx
// (the same surface the provider uses), so it works locally with a client secret
// or certificate and in CI with GitHub OIDC federation — no stored secret:
//
//	# local (secret)
//	ARM_TENANT_ID=… ARM_CLIENT_ID=… ARM_CLIENT_SECRET=… go run ./cmd/verify
//	# CI (GitHub OIDC federated credential)
//	ARM_TENANT_ID=… ARM_CLIENT_ID=… ARM_USE_OIDC=true go run ./cmd/verify
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/terraprovider/go-msadmin/authx"
	"github.com/terraprovider/go-teams/cs"
	"github.com/terraprovider/go-teams/teamsapi"
)

func main() {
	tenant := os.Getenv("ARM_TENANT_ID")

	// authx resolves whatever credential the environment carries (secret,
	// certificate, OIDC/workload-identity, Azure CLI) into a token provider.
	tp, err := authx.FromEnv().Build()
	if err != nil {
		fmt.Fprintln(os.Stderr, "auth:", err)
		os.Exit(2)
	}
	c, err := teamsapi.New(teamsapi.Options{
		Environment: teamsapi.Public,
		TenantID:    tenant,
		Tokens:      tp,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "new:", err)
		os.Exit(1)
	}

	ctx := context.Background()
	svc := cs.New(c)
	// Drive the GENERATED cs bindings end-to-end (read-only Get-Cs*).
	probes := []struct {
		name string
		call func(context.Context) (*teamsapi.Result, error)
	}{
		{"Get-CsTeamsMeetingPolicy", func(ctx context.Context) (*teamsapi.Result, error) {
			return svc.GetCsTeamsMeetingPolicy(ctx, cs.GetCsTeamsMeetingPolicyParams{})
		}},
		{"Get-CsTeamsMessagingPolicy", func(ctx context.Context) (*teamsapi.Result, error) {
			return svc.GetCsTeamsMessagingPolicy(ctx, cs.GetCsTeamsMessagingPolicyParams{})
		}},
		{"Get-CsTeamsChannelsPolicy", func(ctx context.Context) (*teamsapi.Result, error) {
			return svc.GetCsTeamsChannelsPolicy(ctx, cs.GetCsTeamsChannelsPolicyParams{})
		}},
		{"Get-CsTenantFederationConfiguration", func(ctx context.Context) (*teamsapi.Result, error) {
			return svc.GetCsTenantFederationConfiguration(ctx, cs.GetCsTenantFederationConfigurationParams{})
		}},
		{"Get-CsTeamsSettingsCustomApp (autorest)", func(ctx context.Context) (*teamsapi.Result, error) {
			return svc.GetCsTeamsSettingsCustomApp(ctx, cs.GetCsTeamsSettingsCustomAppParams{})
		}},
		{"Get-CsOnlineVoiceRoute (captured→policy)", func(ctx context.Context) (*teamsapi.Result, error) {
			return svc.GetCsOnlineVoiceRoute(ctx, cs.GetCsOnlineVoiceRouteParams{})
		}},
		{"Get-CsCallQueue (captured→autorest)", func(ctx context.Context) (*teamsapi.Result, error) {
			return svc.GetCsCallQueue(ctx, cs.GetCsCallQueueParams{})
		}},
	}
	fmt.Printf("connecting to %s (tenant %s)\n", teamsapi.Public.Endpoint, tenant)
	var failed int
	for _, p := range probes {
		res, err := p.call(ctx)
		if err != nil {
			fmt.Printf("  ✗ %-38s %v\n", p.name, err)
			failed++
			continue
		}
		ids := make([]string, 0, len(res.Value))
		for _, o := range res.Value {
			if s, _ := o["Identity"].(string); s != "" {
				ids = append(ids, s)
			}
		}
		fmt.Printf("  ✓ %-38s %d instance(s): %s\n", p.name, len(res.Value), strings.Join(ids, ", "))
	}
	if failed > 0 {
		os.Exit(1)
	}
	fmt.Println("live transport OK")
}
