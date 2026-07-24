package teamsapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/terraprovider/go-msadmin/httpx"
)

func debugEnabled() bool { return os.Getenv("TEAMSAPI_DEBUG") != "" }

// serviceDiscovery is the /Teams.Tenant/serviceDiscovery response envelope.
type serviceDiscovery struct {
	Endpoints struct {
		ConfigApiEndpoint    string `json:"ConfigApiEndpoint"`
		AdminServiceEndpoint string `json:"AdminServiceEndpoint"`
	} `json:"Endpoints"`
	Headers struct {
		XMSForest string `json:"X-MS-Forest"`
	} `json:"Headers"`
}

// ensureDiscovery performs the one-time connect handshake: GET
// /Teams.Tenant/serviceDiscovery learns the regional admin backend, which every
// subsequent call carries as X-MS-Target-Uri (Teams' routing mechanism — it
// replaces Exchange's 302 + affinity cookie). Retries on failure (not pinned).
func (c *Client) ensureDiscovery(ctx context.Context) error {
	c.disc.Lock()
	defer c.disc.Unlock()
	if c.discovered {
		return nil
	}
	token, err := c.opt.Tokens.Token(ctx, c.opt.Environment.Resource)
	if err != nil {
		return fmt.Errorf("teamsapi: token: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL()+"/Teams.Tenant/serviceDiscovery", nil)
	if err != nil {
		return err
	}
	// serviceDiscovery bootstrap uses the module's minimal header set (no
	// X-MS-Target-Uri yet, since that's what we're learning).
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Connection", "Keep-Alive")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	raw, err := httpx.DecodeBody(resp)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return parseAPIError(resp.StatusCode, raw)
	}
	var sd serviceDiscovery
	if err := json.Unmarshal(raw, &sd); err != nil {
		return fmt.Errorf("teamsapi: decode serviceDiscovery: %w", err)
	}
	if sd.Endpoints.AdminServiceEndpoint == "" {
		return fmt.Errorf("teamsapi: serviceDiscovery returned no AdminServiceEndpoint")
	}
	c.targetURI = "https://" + sd.Endpoints.AdminServiceEndpoint + "/"
	c.forest = sd.Headers.XMSForest
	c.discovered = true
	if debugEnabled() {
		fmt.Fprintf(os.Stderr, "[teamsapi] discovery: target=%s forest=%s\n", c.targetURI, c.forest)
	}
	return nil
}

// setHeaders applies the exact per-request header fingerprint the module sends for
// the given transport, so the request is wire-indistinguishable. The two stacks
// have distinct signatures (see docs 05/06):
//   - ConfigAPI/AutoRest: UA Microsoft.Teams.ConfigAPI.Cmdlets/{ver}/{op}/…, X-MS-Target-Uri
//   - MPA/PolicyRp:        UA Microsoft.Teams.Policy.Administration/{ver}/{op}/…, MPACmdlet: true
func (c *Client) setHeaders(req *http.Request, op Op, token string, hasBody bool) {
	h := req.Header
	h.Set("Authorization", "Bearer "+token)
	corr := httpx.NewCorrelationID()
	// We set Accept-Encoding explicitly on both stacks so net/http does not inject
	// its own "gzip" (a fidelity tell); httpx.DecodeBody decodes br/gzip/deflate.
	h.Set("Accept-Encoding", "br, gzip, deflate")
	switch op.Transport {
	case MPA:
		h.Set("User-Agent", fmt.Sprintf("Microsoft.Teams.Policy.Administration/%s/%s/MicrosoftTeams/%s",
			c.opt.MPAVersion, op.CmdletName, c.opt.ModuleVersion))
		h.Set("X-MS-CmdletName", op.CmdletName)
		h.Set("X-MS-Correlation-Id", corr)
		h.Set("MPACmdlet", "true")
		if c.forest != "" {
			h.Set("X-MS-Forest", c.forest)
		}
	default: // ConfigAPI
		if c.targetURI != "" {
			h.Set("X-MS-Target-Uri", c.targetURI)
		}
		h.Set("X-MS-Correlation-Id", corr)
		h.Set("User-Agent", fmt.Sprintf("Microsoft.Teams.ConfigAPI.Cmdlets/%s/%s/MicrosoftTeams/%s",
			c.opt.ConfigAPIVersion, op.CmdletName, c.opt.ModuleVersion))
		h.Set("X-MS-CmdletName", op.CmdletName)
	}
	if hasBody {
		h.Set("Content-Type", "application/json")
	}
}

// send issues one request for op against the resolved path, after the discovery
// handshake. Retry/throttling is delegated to the *http.Client (wrap it with
// go-msadmin/retry).
func (c *Client) send(ctx context.Context, path string, op Op, body []byte) (*http.Response, error) {
	if err := c.ensureDiscovery(ctx); err != nil {
		return nil, err
	}
	token, err := c.opt.Tokens.Token(ctx, c.opt.Environment.Resource)
	if err != nil {
		return nil, fmt.Errorf("teamsapi: token: %w", err)
	}
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, op.Method, c.baseURL()+path, rdr)
	if err != nil {
		return nil, err
	}
	c.setHeaders(req, op, token, body != nil)
	if debugEnabled() {
		fmt.Fprintf(os.Stderr, "[teamsapi] %s %s%s (%s) target=%q cmdlet=%q toklen=%d\n", op.Method, c.baseURL(), path, op.Transport, c.targetURI, op.CmdletName, len(token))
		if body != nil {
			fmt.Fprintf(os.Stderr, "[teamsapi] >>> body: %s\n", string(body))
		}
	}
	return c.http.Do(req)
}
