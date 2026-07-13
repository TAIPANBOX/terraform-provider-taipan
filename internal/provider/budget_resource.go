package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

var (
	_ resource.Resource                = &budgetResource{}
	_ resource.ResourceWithConfigure   = &budgetResource{}
	_ resource.ResourceWithImportState = &budgetResource{}
)

// budgetResource implements taipan_budget: a central spend budget for one
// TokenFuse Cloud run, set via POST /v1/runs/{run}/budget and read back via
// GET /v1/budgets. Both routes are defined in tokenfuse's
// crates/cloud/src/http.rs (set_budget, budgets).
type budgetResource struct {
	client *CloudClient
}

// NewBudgetResource is a resource.Resource constructor for the provider's
// Resources() list.
func NewBudgetResource() resource.Resource {
	return &budgetResource{}
}

// budgetResourceModel maps taipan_budget's schema to/from Terraform state.
type budgetResourceModel struct {
	RunID    types.String  `tfsdk:"run_id"`
	LimitUSD types.Float64 `tfsdk:"limit_usd"`
}

func (r *budgetResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_budget"
}

func (r *budgetResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a central spend budget for one TokenFuse Cloud run (POST /v1/runs/{run_id}/budget), enforced by the gateway on every call for that run. " +
			"TokenFuse Cloud has no server-side budget-delete endpoint: destroying this resource removes it from Terraform state only. The budget itself stays set in TokenFuse Cloud until something else overwrites it (a new taipan_budget apply, a direct API call, or the gateway's own client-supplied budget). " +
			"Requires the provider's cloud_key to be an admin-role TokenFuse Cloud API key; a non-admin key fails with a 403 diagnostic from the Cloud API.",
		Attributes: map[string]schema.Attribute{
			"run_id": schema.StringAttribute{
				Required:    true,
				Description: "The run id this budget applies to. Budgets are keyed by run id, so changing this forces a new resource.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"limit_usd": schema.Float64Attribute{
				Required:    true,
				Description: "The budget limit in US dollars, sent to the Cloud API as budget_usd. The server stores and reports it in microdollars (budget_usd * 1,000,000); this provider converts back to dollars for state.",
			},
		},
	}
}

func (r *budgetResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	clients, ok := req.ProviderData.(*providerClients)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected taipan_budget Configure type",
			fmt.Sprintf("Expected *provider.providerClients, got: %T. Report this issue to the provider maintainers.", req.ProviderData),
		)
		return
	}
	if clients.Cloud == nil {
		resp.Diagnostics.AddError(
			"Missing TokenFuse Cloud configuration",
			"taipan_budget requires cloud_url and cloud_key, set in the provider block or via the TOKENFUSE_CLOUD_URL/TOKENFUSE_CLOUD_KEY environment variables.",
		)
		return
	}
	r.client = clients.Cloud
}

func (r *budgetResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	r.applyBudget(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *budgetResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	r.applyBudget(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

// applyBudget backs both Create and Update. The Cloud API has one endpoint
// that sets-or-overwrites a run's budget (there is no separate PATCH), so
// both operations converge on the same POST-then-store-response logic.
func (r *budgetResource) applyBudget(ctx context.Context, plan tfsdk.Plan, state *tfsdk.State, diags *diag.Diagnostics) {
	var data budgetResourceModel
	diags.Append(plan.Get(ctx, &data)...)
	if diags.HasError() {
		return
	}

	result, err := r.client.SetBudget(ctx, data.RunID.ValueString(), data.LimitUSD.ValueFloat64())
	if err != nil {
		diags.AddError("Error setting taipan_budget", err.Error())
		return
	}

	// State is derived from the server's own response, not just echoed from
	// plan: budget_micros is the authoritative stored value (the server
	// truncates budget_usd * 1e6 to an int64), so round-tripping it back to
	// dollars keeps state and the real Cloud value in agreement.
	data.RunID = types.StringValue(result.Run)
	data.LimitUSD = types.Float64Value(microsToUSD(result.BudgetMicros))

	diags.Append(state.Set(ctx, &data)...)
}

func (r *budgetResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data budgetResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	budgets, err := r.client.ListBudgets(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Error reading taipan_budget", err.Error())
		return
	}

	micros, ok := budgets[data.RunID.ValueString()]
	if !ok {
		// The run no longer has a central budget override (cleared, expired,
		// or never landed server-side): drop it from state so Terraform
		// plans a recreate instead of reporting a false "no changes".
		resp.State.RemoveResource(ctx)
		return
	}

	data.LimitUSD = types.Float64Value(microsToUSD(micros))
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *budgetResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data budgetResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// TokenFuse Cloud exposes no budget-delete endpoint: crates/cloud only
	// routes POST /v1/runs/{run}/budget (set/overwrite) and GET /v1/budgets
	// (read) -- see the router in crates/cloud/src/http.rs. Inventing a
	// DELETE call here would 404 against every real deployment, so this is
	// intentionally a state-only, best-effort delete: the budget stays set
	// in TokenFuse Cloud until something else overwrites it.
	tflog.Warn(ctx, "taipan_budget has no server-side delete; removing from Terraform state only, the run's budget remains set in TokenFuse Cloud", map[string]interface{}{
		"run_id": data.RunID.ValueString(),
	})
}

func (r *budgetResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("run_id"), req, resp)
}

// microsToUSD converts the Cloud API's stored microdollars back to dollars
// for Terraform state: the inverse of the server's (budget_usd * 1e6) as i64.
func microsToUSD(micros int64) float64 {
	return float64(micros) / 1_000_000
}
