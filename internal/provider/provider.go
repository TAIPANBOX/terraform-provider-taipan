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
	"github.com/hashicorp/terraform-plugin-framework/path"
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
	CloudURL types.String `tfsdk:"cloud_url"`
	CloudKey types.String `tfsdk:"cloud_key"`
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
		Description: "The taipan provider manages TAIPANBOX agent-governance state as code: TokenFuse Cloud spend budgets (taipan_budget) and Idryx/Qryx agent passports (taipan_agent_passport). It is a defensive, operator-side client: it only configures controls the operator already owns, it never scans or acts on third-party systems.",
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
		},
	}
}

func (p *taipanProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var data taipanProviderModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cloudURL := os.Getenv("TOKENFUSE_CLOUD_URL")
	if !data.CloudURL.IsNull() && !data.CloudURL.IsUnknown() && data.CloudURL.ValueString() != "" {
		cloudURL = data.CloudURL.ValueString()
	}

	cloudKey := os.Getenv("TOKENFUSE_CLOUD_KEY")
	if !data.CloudKey.IsNull() && !data.CloudKey.IsUnknown() && data.CloudKey.ValueString() != "" {
		cloudKey = data.CloudKey.ValueString()
	}

	// Required unconditionally, even though taipan_agent_passport itself
	// calls no API: failing fast here, once, gives a single clear
	// diagnostic instead of a confusing nil-client error the first time a
	// taipan_budget resource is planned.
	if cloudURL == "" {
		resp.Diagnostics.AddAttributeError(
			path.Root("cloud_url"),
			"Missing TokenFuse Cloud URL",
			"Set cloud_url in the provider block or the TOKENFUSE_CLOUD_URL environment variable.",
		)
	}
	if cloudKey == "" {
		resp.Diagnostics.AddAttributeError(
			path.Root("cloud_key"),
			"Missing TokenFuse Cloud key",
			"Set cloud_key in the provider block or the TOKENFUSE_CLOUD_KEY environment variable.",
		)
	}
	if resp.Diagnostics.HasError() {
		return
	}

	client := &CloudClient{
		BaseURL: strings.TrimRight(cloudURL, "/"),
		APIKey:  cloudKey,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}

	resp.ResourceData = client
	resp.DataSourceData = client
}

func (p *taipanProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewBudgetResource,
		NewAgentPassportResource,
	}
}

func (p *taipanProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{}
}
