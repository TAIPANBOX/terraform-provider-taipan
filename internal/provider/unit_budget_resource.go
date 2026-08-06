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
	_ resource.Resource                = &unitBudgetResource{}
	_ resource.ResourceWithConfigure   = &unitBudgetResource{}
	_ resource.ResourceWithImportState = &unitBudgetResource{}
)

// unitBudgetResource implements taipan_unit_budget: a central monthly
// spend budget for one TokenFuse Cloud business unit, set via
// POST /v1/units/{id}/budget and read back via GET /v1/unit-budgets. Both
// routes are defined in tokenfuse's crates/cloud/src/http.rs
// (set_unit_budget, unit_budgets); see docs/20-identity-map.md section 4
// for the unit/key/prefix binding this budget is enforced against.
//
// This is a distinct governance control from taipan_budget, not a variant
// of it: taipan_budget caps one run, taipan_unit_budget caps every run a
// business unit (e.g. "treasury") is attributed to over a UTC calendar
// month, resolved from the identity map, not from run_id. The wire shape
// is structurally identical (mirrors budgetResource one-for-one -- same
// request/response fields, same admin-only write, same "no delete
// endpoint" honesty), which is exactly why the resource follows the same
// pattern rather than inventing a new one.
type unitBudgetResource struct {
	client *CloudClient
}

// NewUnitBudgetResource is a resource.Resource constructor for the
// provider's Resources() list.
func NewUnitBudgetResource() resource.Resource {
	return &unitBudgetResource{}
}

// unitBudgetResourceModel maps taipan_unit_budget's schema to/from
// Terraform state.
type unitBudgetResourceModel struct {
	UnitID   types.String  `tfsdk:"unit_id"`
	LimitUSD types.Float64 `tfsdk:"limit_usd"`
}

func (r *unitBudgetResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_unit_budget"
}

func (r *unitBudgetResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a central monthly spend budget for one TokenFuse Cloud business unit (POST /v1/units/{id}/budget), enforced by the gateway against every run the identity map attributes to that unit over a UTC calendar month (docs/20-identity-map.md section 4). " +
			"A distinct governance control from taipan_budget: that resource caps one run by run_id, this one caps a business unit (e.g. treasury, ops) across every run attributed to it. " +
			"TokenFuse Cloud has no server-side unit-budget-delete endpoint, mirroring taipan_budget: destroying this resource removes it from Terraform state only. The budget itself stays set in TokenFuse Cloud until something else overwrites it (a new taipan_unit_budget apply, or a direct API call). " +
			"Requires the provider's cloud_key to be an admin-role TokenFuse Cloud API key; a non-admin key fails with a 403 diagnostic from the Cloud API.",
		Attributes: map[string]schema.Attribute{
			"unit_id": schema.StringAttribute{
				Required:    true,
				Description: "The business unit id this budget applies to, e.g. treasury. Unit budgets are keyed by unit id, so changing this forces a new resource.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"limit_usd": schema.Float64Attribute{
				Required:    true,
				Description: "The monthly budget limit in US dollars, sent to the Cloud API as budget_usd. The server stores and reports it in microdollars (budget_usd * 1,000,000); this provider converts back to dollars for state.",
			},
		},
	}
}

func (r *unitBudgetResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	clients, ok := req.ProviderData.(*providerClients)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected taipan_unit_budget Configure type",
			fmt.Sprintf("Expected *provider.providerClients, got: %T. Report this issue to the provider maintainers.", req.ProviderData),
		)
		return
	}
	if clients.Cloud == nil {
		resp.Diagnostics.AddError(
			"Missing TokenFuse Cloud configuration",
			"taipan_unit_budget requires cloud_url and cloud_key, set in the provider block or via the TOKENFUSE_CLOUD_URL/TOKENFUSE_CLOUD_KEY environment variables.",
		)
		return
	}
	r.client = clients.Cloud
}

func (r *unitBudgetResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	r.applyUnitBudget(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *unitBudgetResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	r.applyUnitBudget(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

// applyUnitBudget backs both Create and Update. The Cloud API has one
// endpoint that sets-or-overwrites a unit's budget (there is no separate
// PATCH), so both operations converge on the same POST-then-store-response
// logic, mirroring budgetResource.applyBudget exactly.
func (r *unitBudgetResource) applyUnitBudget(ctx context.Context, plan tfsdk.Plan, state *tfsdk.State, diags *diag.Diagnostics) {
	var data unitBudgetResourceModel
	diags.Append(plan.Get(ctx, &data)...)
	if diags.HasError() {
		return
	}

	result, err := r.client.SetUnitBudget(ctx, data.UnitID.ValueString(), data.LimitUSD.ValueFloat64())
	if err != nil {
		diags.AddError("Error setting taipan_unit_budget", err.Error())
		return
	}

	// State is derived from the server's own response, not just echoed from
	// plan: budget_micros is the authoritative stored value (the server
	// truncates budget_usd * 1e6 to an int64), so round-tripping it back to
	// dollars keeps state and the real Cloud value in agreement.
	data.UnitID = types.StringValue(result.Unit)
	data.LimitUSD = types.Float64Value(microsToUSD(result.BudgetMicros))

	diags.Append(state.Set(ctx, &data)...)
}

func (r *unitBudgetResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data unitBudgetResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	budgets, err := r.client.ListUnitBudgets(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Error reading taipan_unit_budget", err.Error())
		return
	}

	micros, ok := budgets[data.UnitID.ValueString()]
	if !ok {
		// The unit no longer has a central budget override (cleared,
		// expired, or never landed server-side): drop it from state so
		// Terraform plans a recreate instead of reporting a false "no
		// changes".
		resp.State.RemoveResource(ctx)
		return
	}

	data.LimitUSD = types.Float64Value(microsToUSD(micros))
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *unitBudgetResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data unitBudgetResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// TokenFuse Cloud exposes no unit-budget-delete endpoint: crates/cloud
	// only routes POST /v1/units/{id}/budget (set/overwrite) and
	// GET /v1/unit-budgets (read) -- see the router in
	// crates/cloud/src/http.rs. Inventing a DELETE call here would 404
	// against every real deployment, so this is intentionally a
	// state-only, best-effort delete, mirroring taipan_budget's Delete: the
	// unit's budget stays set in TokenFuse Cloud until something else
	// overwrites it.
	tflog.Warn(ctx, "taipan_unit_budget has no server-side delete; removing from Terraform state only, the unit's budget remains set in TokenFuse Cloud", map[string]interface{}{
		"unit_id": data.UnitID.ValueString(),
	})
}

func (r *unitBudgetResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("unit_id"), req, resp)
}
