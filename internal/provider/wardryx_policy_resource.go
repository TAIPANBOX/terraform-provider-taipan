package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/float64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &wardryxPolicyResource{}
	_ resource.ResourceWithConfigure   = &wardryxPolicyResource{}
	_ resource.ResourceWithImportState = &wardryxPolicyResource{}
)

// emptyStringList is deny_tool/allow_domains's Computed Default, and what
// recordToModel returns for either field when Wardryx's response omits it
// (an empty list, not null) -- these must always agree, or Terraform sees
// a plan/apply mismatch. See Schema's own comment for why an empty/zero
// value needs an explicit Default at all.
var emptyStringList = types.ListValueMust(types.StringType, []attr.Value{})

// wardryxPolicyResource implements taipan_wardryx_policy: one Wardryx
// admin-managed policy document, set via PUT /v1/policies/{id} and read
// back via GET /v1/policies/{id} (wardryx's internal/api). This is layered
// on top of Wardryx's own -policy/WARDRYX_POLICY file-loaded rules, which
// this resource never touches or sees: those stay a permanent floor no API
// write can remove (see wardryx's own README, "Policy-as-code"). Unlike
// taipan_budget, Wardryx's policy API has a real DELETE endpoint, so
// destroying this resource actually removes the rule from Wardryx, not
// just from Terraform state.
type wardryxPolicyResource struct {
	client *WardryxClient
}

// NewWardryxPolicyResource is a resource.Resource constructor for the
// provider's Resources() list.
func NewWardryxPolicyResource() resource.Resource {
	return &wardryxPolicyResource{}
}

// wardryxPolicyResourceModel maps taipan_wardryx_policy's schema to/from
// Terraform state.
type wardryxPolicyResourceModel struct {
	ID                   types.String  `tfsdk:"id"`
	Name                 types.String  `tfsdk:"name"`
	Target               types.String  `tfsdk:"target"`
	DenyTool             types.List    `tfsdk:"deny_tool"`
	AllowDomains         types.List    `tfsdk:"allow_domains"`
	RequireHumanAboveUSD types.Float64 `tfsdk:"require_human_above_usd"`
	DenyAboveUSD         types.Float64 `tfsdk:"deny_above_usd"`
	MaxSteps             types.Int64   `tfsdk:"max_steps"`
	DenyIfUnattested     types.Bool    `tfsdk:"deny_if_unattested"`
	UpdatedAt            types.String  `tfsdk:"updated_at"`
}

func (r *wardryxPolicyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_wardryx_policy"
}

func (r *wardryxPolicyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages one Wardryx admin policy-as-code document (PUT/GET/DELETE /v1/policies/{id}), layered on top of Wardryx's own -policy/WARDRYX_POLICY file-loaded rules, which this resource never touches -- those stay a permanent floor no API write can remove. " +
			"Requires the provider's wardryx_key to be an admin-role Wardryx bearer key; a non-admin key fails with a 403 diagnostic from the Wardryx API.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Required:    true,
				Description: "The policy id, e.g. ops-guard. Wardryx addresses policies by this id, not by name -- there is no rename, only PUT-by-id and DELETE-by-id, so changing it forces a new resource.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			// The Optional fields below all also set Computed + a matching
			// Default (the zero value each one already means "no
			// constraint" as -- see each field's own doc comment in
			// wardryx's internal/policy/policy.go). This isn't cosmetic:
			// wardryx's own wire encoding uses `omitempty`, so an
			// explicitly-set zero value (empty string/list, 0, false) is
			// indistinguishable on the wire from an omitted one, and
			// Wardryx's response therefore always round-trips a genuinely
			// unset attribute back as its zero value, never as a
			// preserved "was never configured" signal. Without a matching
			// Default, Terraform would plan a null value for an omitted
			// attribute but see a concrete zero value after apply, and
			// fail with "provider produced inconsistent result" --
			// confirmed against a real Wardryx server while building this
			// resource (see the repo's commit history).
			"name": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString(""),
				Description: "Human-readable policy name, used in Wardryx's Reason strings and logs. Defaults to target (server-side, in Wardryx's decision engine) when left empty.",
			},
			"target": schema.StringAttribute{
				Required:    true,
				Description: "The agent:// glob this policy applies to (\"*\" matches any run of characters including \"/\"; \"?\" matches exactly one character).",
			},
			"deny_tool": schema.ListAttribute{
				ElementType: types.StringType,
				Optional:    true,
				Computed:    true,
				Default:     listdefault.StaticValue(emptyStringList),
				Description: "Tool names this policy refuses outright.",
			},
			"allow_domains": schema.ListAttribute{
				ElementType: types.StringType,
				Optional:    true,
				Computed:    true,
				Default:     listdefault.StaticValue(emptyStringList),
				Description: "Network destinations a matched agent's declared tools may reach. Only restricts domains a DecideRequest actually declares; see Wardryx's own README for the exact semantics.",
			},
			"require_human_above_usd": schema.Float64Attribute{
				Optional:    true,
				Computed:    true,
				Default:     float64default.StaticFloat64(0),
				Description: "Estimated-cost threshold above which a human must approve the action (a hold, not a hard deny). Zero/unset means no threshold.",
			},
			"deny_above_usd": schema.Float64Attribute{
				Optional:    true,
				Computed:    true,
				Default:     float64default.StaticFloat64(0),
				Description: "Hard, non-approvable cost ceiling: exceeding it denies outright, and no approval_token -- however validly minted -- can turn that deny into an allow. Zero/unset means no hard ceiling.",
			},
			"max_steps": schema.Int64Attribute{
				Optional:    true,
				Computed:    true,
				Default:     int64default.StaticInt64(0),
				Description: "Caps how many steps a run may take. Zero/unset means no cap.",
			},
			"deny_if_unattested": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
				Description: "Denies any request from an agent with no live attestation (attestation method \"\" or \"none\").",
			},
			"updated_at": schema.StringAttribute{
				Computed:    true,
				Description: "RFC 3339 timestamp of this policy's last write, as reported by Wardryx.",
			},
		},
	}
}

func (r *wardryxPolicyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	clients, ok := req.ProviderData.(*providerClients)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected taipan_wardryx_policy Configure type",
			fmt.Sprintf("Expected *provider.providerClients, got: %T. Report this issue to the provider maintainers.", req.ProviderData),
		)
		return
	}
	if clients.Wardryx == nil {
		resp.Diagnostics.AddError(
			"Missing Wardryx configuration",
			"taipan_wardryx_policy requires wardryx_url and wardryx_key, set in the provider block or via the WARDRYX_URL/WARDRYX_KEY environment variables.",
		)
		return
	}
	r.client = clients.Wardryx
}

func (r *wardryxPolicyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	r.applyPolicy(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *wardryxPolicyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	r.applyPolicy(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

// applyPolicy backs both Create and Update: Wardryx's PUT /v1/policies/{id}
// is upsert (create-or-replace), so both operations converge on the same
// build-document-then-PUT-then-store-response logic, mirroring
// budgetResource.applyBudget.
func (r *wardryxPolicyResource) applyPolicy(ctx context.Context, plan tfsdk.Plan, state *tfsdk.State, diags *diag.Diagnostics) {
	var data wardryxPolicyResourceModel
	diags.Append(plan.Get(ctx, &data)...)
	if diags.HasError() {
		return
	}

	doc, d := modelToDocument(ctx, data)
	diags.Append(d...)
	if diags.HasError() {
		return
	}

	result, err := r.client.PutPolicy(ctx, data.ID.ValueString(), doc)
	if err != nil {
		diags.AddError("Error setting taipan_wardryx_policy", err.Error())
		return
	}

	// State is derived from Wardryx's own response, not just echoed from
	// plan: name defaults to target server-side when left empty, and
	// updated_at is server-assigned, so round-tripping the response keeps
	// state and the real Wardryx value in agreement (mirrors
	// budgetResource.applyBudget's same reasoning for limit_usd).
	newData, d := recordToModel(ctx, *result)
	diags.Append(d...)
	if diags.HasError() {
		return
	}

	diags.Append(state.Set(ctx, &newData)...)
}

func (r *wardryxPolicyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data wardryxPolicyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	result, err := r.client.GetPolicy(ctx, data.ID.ValueString())
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			// The policy no longer exists in Wardryx (deleted out of band,
			// or never landed server-side): drop it from state so
			// Terraform plans a recreate instead of reporting a false "no
			// changes", mirroring budgetResource.Read's same handling of a
			// run missing from ListBudgets.
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading taipan_wardryx_policy", err.Error())
		return
	}

	newData, d := recordToModel(ctx, *result)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &newData)...)
}

func (r *wardryxPolicyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data wardryxPolicyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Unlike taipan_budget (no server-side delete, state-only removal),
	// Wardryx's policy API has a real DELETE -- call it. A 404 (already
	// gone, e.g. deleted out of band) is not an error here: the end state
	// Delete is meant to reach ("this id is not enforced by Wardryx") is
	// already true.
	if err := r.client.DeletePolicy(ctx, data.ID.ValueString()); err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			return
		}
		resp.Diagnostics.AddError("Error deleting taipan_wardryx_policy", err.Error())
	}
}

func (r *wardryxPolicyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// modelToDocument converts Terraform state/plan into the wire document
// PutPolicy sends. Mirrors passport_resource.go's Labels handling for the
// same reason: a Terraform List must be read out via ElementsAs, not cast
// directly to []string.
func modelToDocument(ctx context.Context, data wardryxPolicyResourceModel) (WardryxPolicyDocument, diag.Diagnostics) {
	var diags diag.Diagnostics
	doc := WardryxPolicyDocument{
		Name:                 data.Name.ValueString(),
		Target:               data.Target.ValueString(),
		RequireHumanAboveUSD: data.RequireHumanAboveUSD.ValueFloat64(),
		DenyAboveUSD:         data.DenyAboveUSD.ValueFloat64(),
		MaxSteps:             data.MaxSteps.ValueInt64(),
		DenyIfUnattested:     data.DenyIfUnattested.ValueBool(),
	}

	if !data.DenyTool.IsNull() && !data.DenyTool.IsUnknown() {
		d := data.DenyTool.ElementsAs(ctx, &doc.DenyTool, false)
		diags.Append(d...)
	}
	if !data.AllowDomains.IsNull() && !data.AllowDomains.IsUnknown() {
		d := data.AllowDomains.ElementsAs(ctx, &doc.AllowDomains, false)
		diags.Append(d...)
	}
	return doc, diags
}

// recordToModel converts a WardryxPolicyRecord (PutPolicy/GetPolicy's
// response) into Terraform state. Every optional field resolves to a
// concrete value here -- never null -- matching each one's Computed
// Default in Schema: an omitted-on-the-wire field (Wardryx's own
// `omitempty`) decodes as its Go zero value, which is exactly each
// schema Default too, so state always agrees with what planning already
// resolved a genuinely-unset attribute to.
func recordToModel(ctx context.Context, rec WardryxPolicyRecord) (wardryxPolicyResourceModel, diag.Diagnostics) {
	var diags diag.Diagnostics
	data := wardryxPolicyResourceModel{
		ID:                   types.StringValue(rec.ID),
		Name:                 types.StringValue(rec.Name),
		Target:               types.StringValue(rec.Target),
		RequireHumanAboveUSD: types.Float64Value(rec.RequireHumanAboveUSD),
		DenyAboveUSD:         types.Float64Value(rec.DenyAboveUSD),
		MaxSteps:             types.Int64Value(rec.MaxSteps),
		DenyIfUnattested:     types.BoolValue(rec.DenyIfUnattested),
		UpdatedAt:            types.StringValue(rec.UpdatedAt),
	}

	denyTool, d := stringListOrEmpty(ctx, rec.DenyTool)
	diags.Append(d...)
	data.DenyTool = denyTool

	allowDomains, d := stringListOrEmpty(ctx, rec.AllowDomains)
	diags.Append(d...)
	data.AllowDomains = allowDomains

	return data, diags
}

// stringListOrEmpty mirrors recordToModel's "always concrete" rule for the
// two List attributes: emptyStringList, not a null List, so it matches
// deny_tool/allow_domains's own Computed Default.
func stringListOrEmpty(ctx context.Context, ss []string) (types.List, diag.Diagnostics) {
	if len(ss) == 0 {
		return emptyStringList, nil
	}
	return types.ListValueFrom(ctx, types.StringType, ss)
}
