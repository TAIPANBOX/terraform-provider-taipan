package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// The conversion layer between Terraform state and the wire, which had no
// coverage at all until 2026-08-20 while carrying invariant 2 ("State reflects
// the backend, it never invents") at its cheapest point to break.
//
// READ THIS BEFORE TRUSTING ANY OF IT. CLAUDE.md: "A green unit-test run means
// very little in a Terraform provider. Unit tests here check schema shape and
// mapping; they cannot see a perpetual diff, a missing RequiresReplace, or an
// import that does not round-trip. Only the acceptance tests can."
//
// Every test below is a mapping test. None of them says a resource works.

func stringList(t *testing.T, ss ...string) types.List {
	t.Helper()
	l, diags := types.ListValueFrom(context.Background(), types.StringType, ss)
	if diags.HasError() {
		t.Fatalf("build list fixture: %v", diags)
	}
	return l
}

// A fully-populated policy: every Optional attribute set away from its
// Default, so a field that silently failed to carry cannot hide behind a zero
// value that happens to match.
func fullPolicyModel(t *testing.T) wardryxPolicyResourceModel {
	t.Helper()
	return wardryxPolicyResourceModel{
		ID:                   types.StringValue("acct-policy"),
		Name:                 types.StringValue("no shell for finance agents"),
		Target:               types.StringValue("agent://finance.local/*"),
		DenyTool:             stringList(t, "shell_exec", "file_write"),
		AllowDomains:         stringList(t, "api.example.com"),
		RequireHumanAboveUSD: types.Float64Value(5),
		DenyAboveUSD:         types.Float64Value(25),
		MaxSteps:             types.Int64Value(10),
		DenyIfUnattested:     types.BoolValue(true),
	}
}

func TestModelToDocumentCarriesEveryFieldTheOperatorWrote(t *testing.T) {
	t.Parallel()

	doc, diags := modelToDocument(context.Background(), fullPolicyModel(t))
	if diags.HasError() {
		t.Fatalf("modelToDocument: %v", diags)
	}

	// A dropped field here is not an error, it is a weaker policy: a missing
	// deny_tool is a tool that is now allowed, and nothing says so.
	if doc.Name != "no shell for finance agents" {
		t.Errorf("name: got %q", doc.Name)
	}
	if doc.Target != "agent://finance.local/*" {
		t.Errorf("target: got %q", doc.Target)
	}
	if len(doc.DenyTool) != 2 || doc.DenyTool[0] != "shell_exec" || doc.DenyTool[1] != "file_write" {
		t.Errorf("deny_tool: got %v, order matters because the operator wrote it", doc.DenyTool)
	}
	if len(doc.AllowDomains) != 1 || doc.AllowDomains[0] != "api.example.com" {
		t.Errorf("allow_domains: got %v", doc.AllowDomains)
	}
	if doc.RequireHumanAboveUSD != 5 {
		t.Errorf("require_human_above_usd: got %v", doc.RequireHumanAboveUSD)
	}
	if doc.DenyAboveUSD != 25 {
		t.Errorf("deny_above_usd: got %v", doc.DenyAboveUSD)
	}
	if doc.MaxSteps != 10 {
		t.Errorf("max_steps: got %v", doc.MaxSteps)
	}
	if !doc.DenyIfUnattested {
		t.Error("deny_if_unattested: got false, the operator wrote true")
	}
}

func TestModelToDocumentDoesNotInventListsTheOperatorLeftOut(t *testing.T) {
	t.Parallel()

	// Null and unknown are different states in Terraform and both mean "not
	// something the operator asked for". Reading either as a value is how
	// invariant 2's "never invents" breaks at its cheapest point.
	for _, tc := range []struct {
		name string
		list types.List
	}{
		{"null", types.ListNull(types.StringType)},
		{"unknown", types.ListUnknown(types.StringType)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := fullPolicyModel(t)
			m.DenyTool = tc.list
			m.AllowDomains = tc.list

			doc, diags := modelToDocument(context.Background(), m)
			if diags.HasError() {
				t.Fatalf("modelToDocument: %v", diags)
			}
			if len(doc.DenyTool) != 0 {
				t.Errorf("deny_tool from a %s list: got %v, want nothing", tc.name, doc.DenyTool)
			}
			if len(doc.AllowDomains) != 0 {
				t.Errorf("allow_domains from a %s list: got %v, want nothing", tc.name, doc.AllowDomains)
			}
		})
	}
}

func TestRecordToModelResolvesEveryOptionalToAConcreteValue(t *testing.T) {
	t.Parallel()

	// Wardryx's own omitempty means an unset field never reaches the wire, so
	// it decodes as its Go zero value. Every one of these attributes is
	// Computed with a Default equal to that zero value, so planning has
	// already resolved them. State holding null instead would disagree with
	// the resolved plan, and that disagreement is a perpetual diff.
	//
	// This test does not see a perpetual diff; nothing at this level can. It
	// holds the one precondition that makes it impossible from this side.
	rec := WardryxPolicyRecord{ID: "policy-with-nothing-set"}

	data, diags := recordToModel(context.Background(), rec)
	if diags.HasError() {
		t.Fatalf("recordToModel: %v", diags)
	}

	for name, v := range map[string]attr.Value{
		"id":                      data.ID,
		"name":                    data.Name,
		"target":                  data.Target,
		"require_human_above_usd": data.RequireHumanAboveUSD,
		"deny_above_usd":          data.DenyAboveUSD,
		"max_steps":               data.MaxSteps,
		"deny_if_unattested":      data.DenyIfUnattested,
		"updated_at":              data.UpdatedAt,
		"deny_tool":               data.DenyTool,
		"allow_domains":           data.AllowDomains,
	} {
		if v.IsNull() {
			t.Errorf("%s came back null; every attribute here is Computed with a Default, so null disagrees with the resolved plan", name)
		}
		if v.IsUnknown() {
			t.Errorf("%s came back unknown, which state may never hold", name)
		}
	}

	if data.ID.ValueString() != "policy-with-nothing-set" {
		t.Errorf("id: got %q", data.ID.ValueString())
	}
}

func TestStringListOrEmptyReturnsAnEmptyListNeverNull(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		in   []string
	}{
		{"nil", nil},
		{"empty", []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, diags := stringListOrEmpty(context.Background(), tc.in)
			if diags.HasError() {
				t.Fatalf("stringListOrEmpty: %v", diags)
			}
			if got.IsNull() {
				t.Fatal("a null list would disagree with the attribute's Computed Default")
			}
			if n := len(got.Elements()); n != 0 {
				t.Fatalf("got %d elements, want an empty list", n)
			}
		})
	}

	got, diags := stringListOrEmpty(context.Background(), []string{"a", "b"})
	if diags.HasError() {
		t.Fatalf("stringListOrEmpty: %v", diags)
	}
	if n := len(got.Elements()); n != 2 {
		t.Fatalf("got %d elements, want 2", n)
	}
}

func TestStringOrNullKeepsAnAbsentServerFieldOutOfState(t *testing.T) {
	t.Parallel()

	// The opposite rule to stringListOrEmpty, and deliberately so: this
	// attribute is Optional WITHOUT a Default, so an empty string in state is
	// a value nobody set, and the next plan after an import shows a diff for a
	// field nobody changed.
	if got := stringOrNull(""); !got.IsNull() {
		t.Errorf("an empty server field must not become an empty string in state, got %#v", got)
	}
	if got := stringOrNull("agent://a/b"); got.IsNull() || got.ValueString() != "agent://a/b" {
		t.Errorf("a present value must survive, got %#v", got)
	}
}

func TestResolveConfigValuePrefersConfigOverEnvironment(t *testing.T) {
	t.Parallel()

	// Getting this backwards points a whole configuration at the wrong
	// backend, and it does it quietly, because both values are legitimate.
	for _, tc := range []struct {
		name       string
		env        string
		configured types.String
		want       string
	}{
		{"config set wins", "http://from-env", types.StringValue("http://from-config"), "http://from-config"},
		{"config null falls back", "http://from-env", types.StringNull(), "http://from-env"},
		{"config unknown falls back", "http://from-env", types.StringUnknown(), "http://from-env"},
		{"config empty falls back", "http://from-env", types.StringValue(""), "http://from-env"},
		{"neither set", "", types.StringNull(), ""},
		{"config set with no env", "", types.StringValue("http://only-config"), "http://only-config"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := resolveConfigValue(tc.env, tc.configured); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAPIErrorNamesTheStatusAndTheServerMessage(t *testing.T) {
	t.Parallel()

	err := &APIError{StatusCode: 403, Body: `{"error":"budget not found"}`}
	got := err.Error()

	if !strings.Contains(got, "403") {
		t.Errorf("the status code is what an operator matches on first, got %q", got)
	}
	if !strings.Contains(got, "budget not found") {
		t.Errorf("the server's own message must survive rather than be swallowed, got %q", got)
	}

	// WHAT THIS DOES NOT HOLD, and it is worth stating where somebody will
	// read it. Invariant 3 says no credential reaches "state, a log line, or
	// an error message", and CLAUDE.md records that its error-text half is
	// unchecked. This error passes the server's response body through
	// verbatim, by design, so a backend that ever echoes a token in an error
	// body puts it into a Terraform diagnostic. Nothing here catches that, and
	// this test asserting the body survives is not an argument that it should.
}

// Configure is the plumbing between the provider block and each resource, and
// it had no coverage while carrying three distinct behaviours, one of them a
// trap.
//
// THE TRAP: ProviderData is nil during validation, before the provider has
// been configured at all, and the framework calls Configure anyway. Returning
// silently is required. A resource that raised a diagnostic there would fail
// every plan, including `terraform validate` with no credentials present,
// which is exactly when a newcomer first runs it.
//
// The other two are operator-facing: a wrong ProviderData type is a provider
// bug and says so, and a missing client names the precise environment
// variables to set. A typo in those names sends somebody chasing a variable
// that does not exist, and no test had ever read them.
func TestResourceConfigureHandlesEveryProviderDataItCanBeGiven(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		resource       string
		new            func() resource.Resource
		clientsWithout *providerClients
		clientsWith    *providerClients
		wantsEnvVars   []string
	}{
		{
			resource:       "taipan_budget",
			new:            NewBudgetResource,
			clientsWithout: &providerClients{},
			clientsWith:    &providerClients{Cloud: &CloudClient{}},
			wantsEnvVars:   []string{"TOKENFUSE_CLOUD_URL", "TOKENFUSE_CLOUD_KEY"},
		},
		{
			resource:       "taipan_unit_budget",
			new:            NewUnitBudgetResource,
			clientsWithout: &providerClients{},
			clientsWith:    &providerClients{Cloud: &CloudClient{}},
			wantsEnvVars:   []string{"TOKENFUSE_CLOUD_URL", "TOKENFUSE_CLOUD_KEY"},
		},
		{
			resource:       "taipan_wardryx_policy",
			new:            NewWardryxPolicyResource,
			clientsWithout: &providerClients{},
			clientsWith:    &providerClients{Wardryx: &WardryxClient{}},
			wantsEnvVars:   []string{"WARDRYX_URL", "WARDRYX_KEY"},
		},
	} {
		t.Run(tc.resource, func(t *testing.T) {
			t.Parallel()

			configure := func(data any) diag.Diagnostics {
				r, ok := tc.new().(resource.ResourceWithConfigure)
				if !ok {
					t.Fatalf("%s does not implement ResourceWithConfigure", tc.resource)
				}
				resp := &resource.ConfigureResponse{}
				r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: data}, resp)
				return resp.Diagnostics
			}

			if d := configure(nil); d.HasError() {
				t.Errorf("nil ProviderData must return silently, not diagnose: %v", d)
			}

			d := configure("not the clients struct")
			if !d.HasError() {
				t.Error("a wrong ProviderData type is a provider bug and must say so")
			}

			d = configure(tc.clientsWithout)
			if !d.HasError() {
				t.Fatal("a missing client must be diagnosed before apply, not at apply")
			}
			text := d.Errors()[0].Summary() + " " + d.Errors()[0].Detail()
			for _, env := range tc.wantsEnvVars {
				if !strings.Contains(text, env) {
					t.Errorf("the diagnostic must name %s so an operator knows what to set, got: %s", env, text)
				}
			}

			if d := configure(tc.clientsWith); d.HasError() {
				t.Errorf("a correctly configured provider must not diagnose: %v", d)
			}
		})
	}
}

func TestProviderMetadataNamesTheProviderTheWayUsersWriteIt(t *testing.T) {
	t.Parallel()

	// "taipan", not "terraform-provider-taipan". Invariant 6 records that the
	// docs generator gets this wrong by default and rewrote all three page
	// titles once. The type name is the same claim from the other side: it is
	// what an operator types in a resource block.
	var resp provider.MetadataResponse
	New("1.2.3")().Metadata(context.Background(), provider.MetadataRequest{}, &resp)

	if resp.TypeName != "taipan" {
		t.Errorf("type name: got %q, want taipan, which is what a resource block says", resp.TypeName)
	}
	if resp.Version != "1.2.3" {
		t.Errorf("version: got %q, want the one the build passed in", resp.Version)
	}
}
