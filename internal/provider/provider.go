// Package provider implements the taipan Terraform provider: governance as
// code for the TAIPANBOX agent-governance stack. It lets a FinOps/platform
// team manage AI-agent spend budgets (TokenFuse Cloud) and agent identity
// passports (Idryx/Qryx) the same way they manage the rest of their
// infrastructure: in version control, PR-reviewed, applied by the same
// Terraform pipeline.
//
// This is purely defensive tooling. The provider only configures controls
// the operator already owns (their own TokenFuse Cloud org, their own
// passport documents); it never reaches into, scans, or acts on any
// third-party system.
package provider

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure taipanProvider satisfies the provider.Provider interface.
var _ provider.Provider = &taipanProvider{}

// taipanProvider is the top-level `taipan` Terraform provider.
type taipanProvider struct {
	// version is stamped by the release build (see main.go's -X main.version
	// linker flag); "dev" for local/test builds.
	version string
}

// taipanProviderModel maps the `provider "taipan" {}` configuration block.
type taipanProviderModel struct {
	CloudURL   types.String `tfsdk:"cloud_url"`
	CloudKey   types.String `tfsdk:"cloud_key"`
	WardryxURL types.String `tfsdk:"wardryx_url"`
	WardryxKey types.String `tfsdk:"wardryx_key"`
}

// providerClients bundles every backend client a resource might need.
// resource.ConfigureResponse.ResourceData carries exactly one value for the
// whole provider, so every resource receives this same bundle and extracts
// only the field(s) it actually uses: budgetResource wants Cloud,
// wardryxPolicyResource wants Wardryx, passportResource wants neither (it
// implements no Configure at all -- see its own doc comment). Either field
// is nil when its corresponding cloud_url/cloud_key or
// wardryx_url/wardryx_key pair was not configured; a resource that needs a
// nil field reports its own clear diagnostic rather than the provider
// failing unconditionally for every resource regardless of which one is
// actually in use.
type providerClients struct {
	Cloud   *CloudClient
	Wardryx *WardryxClient
}

// New returns a provider server constructor, the shape providerserver.Serve
// and the acceptance-testing helpers both expect.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &taipanProvider{version: version}
	}
}

func (p *taipanProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "taipan"
	resp.Version = p.version
}

func (p *taipanProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "The taipan provider manages TAIPANBOX agent-governance state as code: TokenFuse Cloud spend budgets (taipan_budget), Idryx/Qryx agent passports (taipan_agent_passport), and Wardryx policy-as-code documents (taipan_wardryx_policy). It is a defensive, operator-side client: it only configures controls the operator already owns, it never scans or acts on third-party systems.",
		Attributes: map[string]schema.Attribute{
			"cloud_url": schema.StringAttribute{
				Optional:    true,
				Description: "Base URL of the TokenFuse Cloud control plane, e.g. https://cloud.tokenfuse.example. Falls back to the TOKENFUSE_CLOUD_URL environment variable; required (from one source or the other) for taipan_budget.",
			},
			"cloud_key": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "TokenFuse Cloud API key, sent as an `Authorization: Bearer` header (bearer format key:org[:role]). taipan_budget mutations require an admin-role key. Falls back to the TOKENFUSE_CLOUD_KEY environment variable; required (from one source or the other) for taipan_budget.",
			},
			"wardryx_url": schema.StringAttribute{
				Optional:    true,
				Description: "Base URL of the operator's Wardryx deployment, e.g. https://wardryx.acme.example. Falls back to the WARDRYX_URL environment variable; required (from one source or the other) for taipan_wardryx_policy.",
			},
			"wardryx_key": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "Wardryx bearer key: just the key segment of one WARDRYX_KEYS entry (key:org:role), e.g. WARDRYX_KEYS=\"prod-tf:acme:admin\" means wardryx_key = \"prod-tf\" -- the org and role live server-side, keyed by this value, and are never sent over the wire. Sent as an `Authorization: Bearer` header; taipan_wardryx_policy requires the key's role to be admin. Falls back to the WARDRYX_KEY environment variable; required (from one source or the other) for taipan_wardryx_policy.",
			},
		},
	}
}

func (p *taipanProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var data taipanProviderModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cloudURL := resolveConfigValue(os.Getenv("TOKENFUSE_CLOUD_URL"), data.CloudURL)
	cloudKey := resolveConfigValue(os.Getenv("TOKENFUSE_CLOUD_KEY"), data.CloudKey)
	wardryxURL := resolveConfigValue(os.Getenv("WARDRYX_URL"), data.WardryxURL)
	wardryxKey := resolveConfigValue(os.Getenv("WARDRYX_KEY"), data.WardryxKey)

	httpClient := &http.Client{Timeout: 30 * time.Second}

	// Each client is built only when its own pair is fully configured, and
	// left nil otherwise -- unlike the old unconditional
	// AddAttributeError-and-abort here, which made every resource (even
	// taipan_agent_passport, which calls no API at all) fail provider
	// Configure whenever cloud_url/cloud_key were unset, regardless of
	// whether the operator's config used taipan_budget at all. A resource
	// that actually needs a nil client reports its own clear diagnostic in
	// its own Configure, scoped to only the resource types that need it.
	clients := &providerClients{}
	if cloudURL != "" && cloudKey != "" {
		clients.Cloud = &CloudClient{
			BaseURL:    strings.TrimRight(cloudURL, "/"),
			APIKey:     cloudKey,
			HTTPClient: httpClient,
		}
	}
	if wardryxURL != "" && wardryxKey != "" {
		clients.Wardryx = &WardryxClient{
			BaseURL:    strings.TrimRight(wardryxURL, "/"),
			APIKey:     wardryxKey,
			HTTPClient: httpClient,
		}
	}

	resp.ResourceData = clients
	resp.DataSourceData = clients
}

// resolveConfigValue applies the provider's standard "config attribute,
// falling back to an environment variable" precedence: envDefault unless
// the Terraform config explicitly set a known, non-empty value, in which
// case the config wins.
func resolveConfigValue(envDefault string, configured types.String) string {
	if !configured.IsNull() && !configured.IsUnknown() && configured.ValueString() != "" {
		return configured.ValueString()
	}
	return envDefault
}

func (p *taipanProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewBudgetResource,
		NewAgentPassportResource,
		NewWardryxPolicyResource,
	}
}

func (p *taipanProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{}
}
