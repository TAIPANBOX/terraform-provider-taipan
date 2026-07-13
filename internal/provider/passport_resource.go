package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/TAIPANBOX/agent-stack-go/passport"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &passportResource{}

// passportResource implements taipan_agent_passport: it renders and
// validates a TAIPANBOX Agent Passport document (schema
// taipanbox.dev/agent-passport/v0.1) and, optionally, writes it to disk.
// Passports are static files consumed by Idryx and Qryx, not a
// server-managed object, so this resource calls no API and needs no
// provider-level Cloud client -- it works whether or not cloud_url/cloud_key
// are set.
type passportResource struct{}

// NewAgentPassportResource is a resource.Resource constructor for the
// provider's Resources() list.
func NewAgentPassportResource() resource.Resource {
	return &passportResource{}
}

// passportResourceModel maps taipan_agent_passport's schema to/from
// Terraform state.
type passportResourceModel struct {
	ID                types.String `tfsdk:"id"`
	Owner             types.String `tfsdk:"owner"`
	DisplayName       types.String `tfsdk:"display_name"`
	Runtime           types.String `tfsdk:"runtime"`
	Parent            types.String `tfsdk:"parent"`
	AttestationMethod types.String `tfsdk:"attestation_method"`
	AttestationDetail types.String `tfsdk:"attestation_detail"`
	Labels            types.Map    `tfsdk:"labels"`
	OutputPath        types.String `tfsdk:"output_path"`
	JSON              types.String `tfsdk:"json"`
}

func (r *passportResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_agent_passport"
}

func (r *passportResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Renders and validates a TAIPANBOX Agent Passport document (schema taipanbox.dev/agent-passport/v0.1). " +
			"This resource calls no API: a passport is a small, static JSON file describing one agent's identity, owner, runtime and attestation posture, consumed by Idryx and Qryx, not a server-managed object. " +
			"Create and Update compute and validate the document, reusing agent-stack-go/passport's Parse verbatim (the same validation Idryx runs on ingest), and, if output_path is set, write it to disk at file mode 0600. Delete removes that file, if any. " +
			"The rendered document is exposed as the computed json attribute for downstream use, e.g. handing it to another resource or provisioner.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Required:    true,
				Description: "The passport's agent:// URI, e.g. agent://acme-bank.example/support/tier1-bot. Validated with agent-stack-go/passport.ValidateAgentURI. Changing it forces a new resource.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"owner": schema.StringAttribute{
				Required:    true,
				Description: "The owning team or identity, e.g. team-support@acme-bank.example.",
			},
			"display_name": schema.StringAttribute{
				Optional:    true,
				Description: "Human-readable name for the agent.",
			},
			"runtime": schema.StringAttribute{
				Optional:    true,
				Description: "The agent's runtime, e.g. langgraph, autogen, custom.",
			},
			"parent": schema.StringAttribute{
				Optional:    true,
				Description: "The agent:// URI of this agent's static provisioning parent, if any.",
			},
			"attestation_method": schema.StringAttribute{
				Optional: true,
				Description: "How the organization binds this passport's id to a workload: one of none, oidc, spiffe-svid, enclave-key, mtls-cert. " +
					"Left unset, the rendered document omits the attestation block entirely.",
			},
			"attestation_detail": schema.StringAttribute{
				Optional: true,
				Description: "A method-specific reference for attestation_method, e.g. a SPIFFE ID for spiffe-svid or an issuer URL for oidc (agent-passport/SPEC.md §4.3; unconstrained string, no format is validated). " +
					"Ignored if attestation_method is unset, since the rendered document then omits the attestation block entirely.",
			},
			"labels": schema.MapAttribute{
				ElementType: types.StringType,
				Optional:    true,
				Description: "Free-form string labels, e.g. env, cost_center.",
			},
			"output_path": schema.StringAttribute{
				Optional:    true,
				Description: "If set, the rendered passport JSON is written to this file path (mode 0600) so Idryx/Qryx can read it from disk. If unset, the resource only computes the json attribute and manages no file.",
			},
			"json": schema.StringAttribute{
				Computed:    true,
				Description: "The rendered, schema-validated passport document (taipanbox.dev/agent-passport/v0.1), with stable key order for a reproducible diff.",
			},
		},
	}
}

func (r *passportResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data passportResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	rendered, err := renderPassport(ctx, &data)
	if err != nil {
		resp.Diagnostics.AddError("Invalid taipan_agent_passport", err.Error())
		return
	}

	if outputPath := data.OutputPath.ValueString(); !data.OutputPath.IsNull() && outputPath != "" {
		if err := writePassportFile(outputPath, rendered); err != nil {
			resp.Diagnostics.AddError("Unable to write taipan_agent_passport output_path", err.Error())
			return
		}
	}

	data.JSON = types.StringValue(string(rendered))
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *passportResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data passportResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// No external system to read from: taipan_agent_passport re-derives its
	// computed json attribute from the other, already-known state fields.
	// This is deliberately side-effect-free (no filesystem access), so Read
	// never touches output_path.
	rendered, err := renderPassport(ctx, &data)
	if err != nil {
		resp.Diagnostics.AddError("Invalid taipan_agent_passport", err.Error())
		return
	}

	data.JSON = types.StringValue(string(rendered))
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *passportResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan passportResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var state passportResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	rendered, err := renderPassport(ctx, &plan)
	if err != nil {
		resp.Diagnostics.AddError("Invalid taipan_agent_passport", err.Error())
		return
	}

	oldPath := state.OutputPath.ValueString()
	newPath := plan.OutputPath.ValueString()
	if oldPath != "" && oldPath != newPath {
		if err := os.Remove(oldPath); err != nil && !os.IsNotExist(err) {
			resp.Diagnostics.AddError("Unable to remove previous taipan_agent_passport output_path", err.Error())
			return
		}
	}
	if !plan.OutputPath.IsNull() && newPath != "" {
		if err := writePassportFile(newPath, rendered); err != nil {
			resp.Diagnostics.AddError("Unable to write taipan_agent_passport output_path", err.Error())
			return
		}
	}

	plan.JSON = types.StringValue(string(rendered))
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *passportResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data passportResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if data.OutputPath.IsNull() {
		return
	}
	outputPath := data.OutputPath.ValueString()
	if outputPath == "" {
		return
	}
	if err := os.Remove(outputPath); err != nil && !os.IsNotExist(err) {
		resp.Diagnostics.AddError("Unable to remove taipan_agent_passport output_path", err.Error())
	}
}

// renderPassport builds a passport.Passport from the resource's current
// Terraform data, marshals it to deterministic, indented JSON, and
// validates it by round-tripping through agent-stack-go/passport.Parse,
// the exact function Idryx uses to ingest a passport document. A
// taipan_agent_passport that applies cleanly is therefore guaranteed to
// parse downstream too.
func renderPassport(ctx context.Context, data *passportResourceModel) ([]byte, error) {
	p := passport.Passport{
		Schema:      passport.RequiredSchema,
		ID:          data.ID.ValueString(),
		Owner:       data.Owner.ValueString(),
		DisplayName: data.DisplayName.ValueString(),
		Runtime:     data.Runtime.ValueString(),
		Parent:      data.Parent.ValueString(),
	}

	if method := data.AttestationMethod.ValueString(); !data.AttestationMethod.IsNull() && method != "" {
		p.Attestation = &passport.Attestation{Method: method, Detail: data.AttestationDetail.ValueString()}
	}

	if !data.Labels.IsNull() && !data.Labels.IsUnknown() {
		var labels map[string]string
		diags := data.Labels.ElementsAs(ctx, &labels, false)
		if diags.HasError() {
			return nil, fmt.Errorf("read labels: %s", diags[0].Summary())
		}
		if len(labels) > 0 {
			p.Labels = labels
		}
	}

	// encoding/json marshals struct fields in declaration order and sorts
	// map[string]string keys alphabetically (both documented, stable
	// behaviors), so this is byte-for-byte reproducible for the same input.
	rendered, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("render passport: %w", err)
	}

	if _, err := passport.Parse(rendered); err != nil {
		return nil, fmt.Errorf("render produced an invalid passport: %w", err)
	}

	return rendered, nil
}

// writePassportFile writes the rendered passport to path with a trailing
// newline, at file mode 0600: passport documents can carry organizational
// structure (owner, runtime, labels), so they are written readable only by
// the invoking user.
func writePassportFile(path string, rendered []byte) error {
	out := make([]byte, 0, len(rendered)+1)
	out = append(out, rendered...)
	out = append(out, '\n')
	// #nosec G304 -- path is an operator-supplied Terraform attribute
	// (output_path), not untrusted input; mirrors Idryx's convention for
	// operator-supplied file paths (internal/ingest/passport/passport.go).
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return err
	}
	return nil
}
