package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TAIPANBOX/agent-stack-go/passport"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// filesystemObjectType and modelObjectType are the element types of the
// filesystem and models lists, matching the nested-block attribute shapes in
// the resource schema. They let the mustFilesystem/mustModels helpers build a
// types.List the same way the framework would decode the HCL blocks.
var (
	filesystemObjectType = types.ObjectType{AttrTypes: map[string]attr.Type{
		"path": types.StringType,
		"mode": types.StringType,
	}}
	modelObjectType = types.ObjectType{AttrTypes: map[string]attr.Type{
		"provider": types.StringType,
		"model":    types.StringType,
		"endpoint": types.StringType,
	}}
)

// mustFilesystem builds a types.List the way the framework would decode a set
// of filesystem { ... } blocks into passportResourceModel.Filesystem.
func mustFilesystem(t *testing.T, scopes []filesystemScopeModel) types.List {
	t.Helper()
	l, diags := types.ListValueFrom(context.Background(), filesystemObjectType, scopes)
	if diags.HasError() {
		t.Fatalf("build filesystem list: %v", diags)
	}
	return l
}

// mustModels builds a types.List the way the framework would decode a set of
// models { ... } blocks into passportResourceModel.Models.
func mustModels(t *testing.T, decls []modelDeclModel) types.List {
	t.Helper()
	l, diags := types.ListValueFrom(context.Background(), modelObjectType, decls)
	if diags.HasError() {
		t.Fatalf("build models list: %v", diags)
	}
	return l
}

// mustLabels builds a types.Map the same way the framework would decode a
// labels = { ... } HCL block into passportResourceModel.Labels.
func mustLabels(t *testing.T, labels map[string]string) types.Map {
	t.Helper()
	m, diags := types.MapValueFrom(context.Background(), types.StringType, labels)
	if diags.HasError() {
		t.Fatalf("build labels map: %v", diags)
	}
	return m
}

// TestRenderPassport_Full covers every optional field populated, and
// asserts the rendered document round-trips through
// agent-stack-go/passport.Parse (the same validator Idryx uses) and
// matches the shape agent-stack-go's own tests expect.
func TestRenderPassport_Full(t *testing.T) {
	data := &passportResourceModel{
		ID:                types.StringValue("agent://acme-bank.example/support/tier1-bot"),
		Owner:             types.StringValue("team-support@acme-bank.example"),
		DisplayName:       types.StringValue("Tier-1 support bot"),
		Runtime:           types.StringValue("langgraph"),
		Parent:            types.StringValue("agent://acme-bank.example/support/orchestrator"),
		AttestationMethod: types.StringValue("spiffe-svid"),
		AttestationDetail: types.StringValue("spiffe://acme-bank.example/support/tier1-bot"),
		Labels:            mustLabels(t, map[string]string{"env": "prod", "cost_center": "cs-eu"}),
		OutputPath:        types.StringNull(),
	}

	rendered, err := renderPassport(context.Background(), data)
	if err != nil {
		t.Fatalf("renderPassport: %v", err)
	}

	p, err := passport.Parse(rendered)
	if err != nil {
		t.Fatalf("passport.Parse(rendered): %v", err)
	}
	if p.ID != "agent://acme-bank.example/support/tier1-bot" {
		t.Errorf("ID = %q", p.ID)
	}
	if p.Owner != "team-support@acme-bank.example" {
		t.Errorf("Owner = %q", p.Owner)
	}
	if p.DisplayName != "Tier-1 support bot" {
		t.Errorf("DisplayName = %q", p.DisplayName)
	}
	if p.Attestation == nil || p.Attestation.Method != "spiffe-svid" {
		t.Errorf("Attestation = %+v, want Method=spiffe-svid", p.Attestation)
	}
	if p.Attestation != nil && p.Attestation.Detail != "spiffe://acme-bank.example/support/tier1-bot" {
		t.Errorf("Attestation.Detail = %q, want spiffe://acme-bank.example/support/tier1-bot", p.Attestation.Detail)
	}
	if p.Labels["env"] != "prod" || p.Labels["cost_center"] != "cs-eu" {
		t.Errorf("Labels = %+v", p.Labels)
	}

	// Stable key order: labels (a map) must serialize with sorted keys, so
	// cost_center precedes env in the byte stream regardless of Go map
	// iteration order.
	costCenterIdx := strings.Index(string(rendered), `"cost_center"`)
	envIdx := strings.Index(string(rendered), `"env"`)
	if costCenterIdx == -1 || envIdx == -1 || costCenterIdx > envIdx {
		t.Errorf("labels not in sorted key order in rendered json:\n%s", rendered)
	}
}

// TestRenderPassport_Minimal covers only the two required fields, mirroring
// agent-stack-go's TestParseMinimal: every optional field must be omitted
// from the document, not emitted as an empty string/object.
func TestRenderPassport_Minimal(t *testing.T) {
	data := &passportResourceModel{
		ID:    types.StringValue("agent://acme.example/bot"),
		Owner: types.StringValue("team@acme.example"),
	}

	rendered, err := renderPassport(context.Background(), data)
	if err != nil {
		t.Fatalf("renderPassport: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(rendered, &raw); err != nil {
		t.Fatalf("rendered json invalid: %v", err)
	}
	for _, field := range []string{"display_name", "runtime", "parent", "attestation", "labels", "filesystem", "models", "created_at"} {
		if _, present := raw[field]; present {
			t.Errorf("field %q present in minimal render, want omitted: %s", field, rendered)
		}
	}

	if _, err := passport.Parse(rendered); err != nil {
		t.Fatalf("passport.Parse(minimal rendered): %v", err)
	}
}

// TestRenderPassport_Deterministic asserts two renders of the same input
// are byte-for-byte identical, the property the resource's json attribute
// (and Terraform's plan diffing) depends on.
func TestRenderPassport_Deterministic(t *testing.T) {
	build := func() *passportResourceModel {
		return &passportResourceModel{
			ID:      types.StringValue("agent://acme.example/eng/ci-fixer"),
			Owner:   types.StringValue("team-eng@acme.example"),
			Runtime: types.StringValue("autogen"),
			Labels:  mustLabels(t, map[string]string{"z-last": "1", "a-first": "2", "m-mid": "3"}),
		}
	}

	first, err := renderPassport(context.Background(), build())
	if err != nil {
		t.Fatalf("renderPassport (1): %v", err)
	}
	second, err := renderPassport(context.Background(), build())
	if err != nil {
		t.Fatalf("renderPassport (2): %v", err)
	}

	if string(first) != string(second) {
		t.Errorf("renderPassport not deterministic:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}

// TestRenderPassport_InvalidID asserts a malformed agent:// URI is rejected
// by ValidateAgentURI (via passport.Parse) rather than silently accepted,
// even though the id attribute is also validated at plan time by the
// RequiresReplace path -- renderPassport is the last line of defense.
func TestRenderPassport_InvalidID(t *testing.T) {
	data := &passportResourceModel{
		ID:    types.StringValue("not-a-valid-uri"),
		Owner: types.StringValue("team@acme.example"),
	}

	_, err := renderPassport(context.Background(), data)
	if err == nil {
		t.Fatal("renderPassport: expected an error for an invalid id, got nil")
	}
	if !errors.Is(err, passport.ErrInvalidURI) {
		t.Errorf("error = %v, want wrapping passport.ErrInvalidURI", err)
	}
}

// TestRenderPassport_EmptyOwner asserts an empty owner is rejected. The
// resource schema also marks owner Required (Terraform itself blocks a
// null), but an empty string "" passes schema validation and must still be
// caught here.
func TestRenderPassport_EmptyOwner(t *testing.T) {
	data := &passportResourceModel{
		ID:    types.StringValue("agent://acme.example/bot"),
		Owner: types.StringValue(""),
	}

	_, err := renderPassport(context.Background(), data)
	if err == nil {
		t.Fatal("renderPassport: expected an error for an empty owner, got nil")
	}
	if !errors.Is(err, passport.ErrMissingOwner) {
		t.Errorf("error = %v, want wrapping passport.ErrMissingOwner", err)
	}
}

// TestRenderPassport_NoAttestationWhenMethodUnset asserts leaving
// attestation_method null omits the attestation block entirely, rather than
// emitting {"method":""}.
func TestRenderPassport_NoAttestationWhenMethodUnset(t *testing.T) {
	data := &passportResourceModel{
		ID:                types.StringValue("agent://acme.example/bot"),
		Owner:             types.StringValue("team@acme.example"),
		AttestationMethod: types.StringNull(),
	}

	rendered, err := renderPassport(context.Background(), data)
	if err != nil {
		t.Fatalf("renderPassport: %v", err)
	}
	if strings.Contains(string(rendered), "attestation") {
		t.Errorf("attestation present with attestation_method unset: %s", rendered)
	}
}

// TestRenderPassport_NoDetailWhenUnset asserts that setting
// attestation_method without attestation_detail omits the detail key
// entirely (json:"detail,omitempty"), rather than emitting "detail":"".
func TestRenderPassport_NoDetailWhenUnset(t *testing.T) {
	data := &passportResourceModel{
		ID:                types.StringValue("agent://acme.example/bot"),
		Owner:             types.StringValue("team@acme.example"),
		AttestationMethod: types.StringValue("none"),
	}

	rendered, err := renderPassport(context.Background(), data)
	if err != nil {
		t.Fatalf("renderPassport: %v", err)
	}
	if strings.Contains(string(rendered), "detail") {
		t.Errorf("detail present with attestation_detail unset: %s", rendered)
	}

	p, err := passport.Parse(rendered)
	if err != nil {
		t.Fatalf("passport.Parse(rendered): %v", err)
	}
	if p.Attestation == nil || p.Attestation.Detail != "" {
		t.Errorf("Attestation = %+v, want non-nil with empty Detail", p.Attestation)
	}
}

// TestRenderPassport_FilesystemAndModels covers the filesystem and models
// blocks the Genaryx onboard wizard and the catalog Terraform emit: they must
// render as the document's root-level filesystem/models arrays (agent-passport
// SPEC.md §4.4-4.5), in declared order, and still round-trip through
// passport.Parse (which tolerates the fields the Go mirror type does not yet
// carry).
func TestRenderPassport_FilesystemAndModels(t *testing.T) {
	data := &passportResourceModel{
		ID:    types.StringValue("agent://acme.example/data/etl-bot"),
		Owner: types.StringValue("team-data@acme.example"),
		Filesystem: mustFilesystem(t, []filesystemScopeModel{
			{Path: types.StringValue("/data/reports"), Mode: types.StringValue("read")},
			{Path: types.StringValue("/data/out"), Mode: types.StringValue("write")},
		}),
		Models: mustModels(t, []modelDeclModel{
			{Provider: types.StringValue("anthropic"), Model: types.StringValue("claude-sonnet-4-5"), Endpoint: types.StringValue("api.anthropic.com")},
			// provider-only: model and endpoint left null, so both keys must
			// be omitted from this entry.
			{Provider: types.StringValue("openai"), Model: types.StringNull(), Endpoint: types.StringNull()},
		}),
	}

	rendered, err := renderPassport(context.Background(), data)
	if err != nil {
		t.Fatalf("renderPassport: %v", err)
	}

	// Same validator Idryx runs on ingest: the extra arrays must not break it.
	if _, err := passport.Parse(rendered); err != nil {
		t.Fatalf("passport.Parse(rendered): %v", err)
	}

	var doc struct {
		ID         string           `json:"id"`
		Owner      string           `json:"owner"`
		Filesystem []map[string]any `json:"filesystem"`
		Models     []map[string]any `json:"models"`
	}
	if err := json.Unmarshal(rendered, &doc); err != nil {
		t.Fatalf("rendered json invalid: %v\n%s", err, rendered)
	}

	if doc.ID != "agent://acme.example/data/etl-bot" || doc.Owner != "team-data@acme.example" {
		t.Errorf("core fields not preserved: id=%q owner=%q", doc.ID, doc.Owner)
	}

	// filesystem: two entries, in declared order, each with exactly path+mode.
	if len(doc.Filesystem) != 2 {
		t.Fatalf("filesystem len = %d, want 2: %s", len(doc.Filesystem), rendered)
	}
	if doc.Filesystem[0]["path"] != "/data/reports" || doc.Filesystem[0]["mode"] != "read" {
		t.Errorf("filesystem[0] = %v, want path=/data/reports mode=read", doc.Filesystem[0])
	}
	if doc.Filesystem[1]["path"] != "/data/out" || doc.Filesystem[1]["mode"] != "write" {
		t.Errorf("filesystem[1] = %v, want path=/data/out mode=write", doc.Filesystem[1])
	}

	// models: entry 0 carries all three keys; entry 1 (provider-only) must
	// carry provider ALONE, with model/endpoint omitted rather than "".
	if len(doc.Models) != 2 {
		t.Fatalf("models len = %d, want 2: %s", len(doc.Models), rendered)
	}
	if doc.Models[0]["provider"] != "anthropic" || doc.Models[0]["model"] != "claude-sonnet-4-5" || doc.Models[0]["endpoint"] != "api.anthropic.com" {
		t.Errorf("models[0] = %v, want provider/model/endpoint all set", doc.Models[0])
	}
	if doc.Models[1]["provider"] != "openai" {
		t.Errorf("models[1] provider = %v, want openai", doc.Models[1]["provider"])
	}
	if _, present := doc.Models[1]["model"]; present {
		t.Errorf("models[1] has a model key, want it omitted: %v", doc.Models[1])
	}
	if _, present := doc.Models[1]["endpoint"]; present {
		t.Errorf("models[1] has an endpoint key, want it omitted: %v", doc.Models[1])
	}

	// Deterministic with the blocks populated too: same input, identical bytes.
	again, err := renderPassport(context.Background(), data)
	if err != nil {
		t.Fatalf("renderPassport (again): %v", err)
	}
	if string(again) != string(rendered) {
		t.Errorf("render with filesystem/models not deterministic:\n--- a ---\n%s\n--- b ---\n%s", rendered, again)
	}
}

// TestRenderPassport_EmptyBlocksOmitted asserts that filesystem/models present
// but empty (a null or zero-length list, e.g. an old plan or a resource with
// no blocks) render byte-for-byte identically to a passport that never had the
// fields -- the backward-compatibility guarantee omitempty is there to keep.
func TestRenderPassport_EmptyBlocksOmitted(t *testing.T) {
	withNull := &passportResourceModel{
		ID:         types.StringValue("agent://acme.example/bot"),
		Owner:      types.StringValue("team@acme.example"),
		Filesystem: types.ListNull(filesystemObjectType),
		Models:     types.ListNull(modelObjectType),
	}
	withEmpty := &passportResourceModel{
		ID:         types.StringValue("agent://acme.example/bot"),
		Owner:      types.StringValue("team@acme.example"),
		Filesystem: mustFilesystem(t, []filesystemScopeModel{}),
		Models:     mustModels(t, []modelDeclModel{}),
	}
	bare := &passportResourceModel{
		ID:    types.StringValue("agent://acme.example/bot"),
		Owner: types.StringValue("team@acme.example"),
	}

	rNull, err := renderPassport(context.Background(), withNull)
	if err != nil {
		t.Fatalf("renderPassport(null lists): %v", err)
	}
	rEmpty, err := renderPassport(context.Background(), withEmpty)
	if err != nil {
		t.Fatalf("renderPassport(empty lists): %v", err)
	}
	rBare, err := renderPassport(context.Background(), bare)
	if err != nil {
		t.Fatalf("renderPassport(bare): %v", err)
	}

	if string(rNull) != string(rBare) || string(rEmpty) != string(rBare) {
		t.Errorf("empty/null filesystem+models did not render identically to a bare passport:\n--- bare ---\n%s\n--- null ---\n%s\n--- empty ---\n%s", rBare, rNull, rEmpty)
	}
	if strings.Contains(string(rBare), "filesystem") || strings.Contains(string(rBare), "models") {
		t.Errorf("filesystem/models key present when unset: %s", rBare)
	}
}

func TestWritePassportFile(t *testing.T) {
	// writePassportFile only writes the file; like os.WriteFile, it does
	// not create intermediate directories, so it is exercised against an
	// existing directory (t.TempDir()) only.
	dir := t.TempDir()
	path := filepath.Join(dir, "tier1-bot.json")

	rendered := []byte(`{"schema":"taipanbox.dev/agent-passport/v0.1"}`)
	if err := writePassportFile(path, rendered); err != nil {
		t.Fatalf("writePassportFile: %v", err)
	}

	got, err := os.ReadFile(path) // #nosec G304 -- test-owned temp file path
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	want := string(rendered) + "\n"
	if string(got) != want {
		t.Errorf("file content = %q, want %q", got, want)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file mode = %o, want 0600", perm)
	}
}

// testAccAgentPassportFilesystemModelsConfig is the exact filesystem {} /
// models {} block syntax the Genaryx onboard wizard emits
// (genaryx/crates/api/src/onboard/commands.rs) and the catalog's
// agent-with-guardrail.tf documents the provider could not yet accept. It is
// the regression fixture for this resource actually accepting that HCL.
const testAccAgentPassportFilesystemModelsConfig = `
resource "taipan_agent_passport" "acc" {
  id      = "agent://acme.example/data/etl-bot"
  owner   = "team-data@acme.example"
  runtime = "langgraph"

  filesystem {
    path = "/data/reports"
    mode = "read"
  }
  filesystem {
    path = "/data/out"
    mode = "write"
  }

  models {
    provider = "anthropic"
    model    = "claude-sonnet-4-5"
    endpoint = "api.anthropic.com"
  }
  models {
    provider = "openai"
  }
}
`

// TestAccAgentPassportFilesystemModels drives a real terraform apply through
// the in-process provider (no backend: taipan_agent_passport calls no API) to
// prove the wizard-generated filesystem {} and models {} blocks parse, apply,
// and land in both state and the computed json document. This is the end-to-end
// counterpart to TestRenderPassport_FilesystemAndModels, which exercises only
// the render function. Runs solely under TF_ACC (resource.Test skips
// otherwise); it needs a terraform/tofu binary on PATH but no env-var backend,
// so it takes no PreCheck.
func TestAccAgentPassportFilesystemModels(t *testing.T) {
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccAgentPassportFilesystemModelsConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("taipan_agent_passport.acc", "filesystem.#", "2"),
					resource.TestCheckResourceAttr("taipan_agent_passport.acc", "filesystem.0.path", "/data/reports"),
					resource.TestCheckResourceAttr("taipan_agent_passport.acc", "filesystem.0.mode", "read"),
					resource.TestCheckResourceAttr("taipan_agent_passport.acc", "filesystem.1.path", "/data/out"),
					resource.TestCheckResourceAttr("taipan_agent_passport.acc", "filesystem.1.mode", "write"),
					resource.TestCheckResourceAttr("taipan_agent_passport.acc", "models.#", "2"),
					resource.TestCheckResourceAttr("taipan_agent_passport.acc", "models.0.provider", "anthropic"),
					resource.TestCheckResourceAttr("taipan_agent_passport.acc", "models.0.model", "claude-sonnet-4-5"),
					resource.TestCheckResourceAttr("taipan_agent_passport.acc", "models.0.endpoint", "api.anthropic.com"),
					resource.TestCheckResourceAttr("taipan_agent_passport.acc", "models.1.provider", "openai"),
					// The rendered document carries the blocks as root-level
					// arrays, and passport.Parse accepts it.
					resource.TestCheckResourceAttrWith("taipan_agent_passport.acc", "json", checkRenderedPassportBlocks),
				),
			},
		},
	})
}

// checkRenderedPassportBlocks validates the computed json attribute end to end:
// it parses through the same passport.Parse Idryx runs on ingest, then asserts
// the filesystem/models arrays rendered as the schema expects, including the
// provider-only entry omitting its model/endpoint keys.
func checkRenderedPassportBlocks(rendered string) error {
	if _, err := passport.Parse([]byte(rendered)); err != nil {
		return fmt.Errorf("computed json does not parse as a passport: %w", err)
	}
	var doc struct {
		Filesystem []map[string]any `json:"filesystem"`
		Models     []map[string]any `json:"models"`
	}
	if err := json.Unmarshal([]byte(rendered), &doc); err != nil {
		return fmt.Errorf("computed json invalid: %w", err)
	}
	if len(doc.Filesystem) != 2 {
		return fmt.Errorf("filesystem len = %d, want 2: %s", len(doc.Filesystem), rendered)
	}
	if doc.Filesystem[0]["path"] != "/data/reports" || doc.Filesystem[0]["mode"] != "read" {
		return fmt.Errorf("filesystem[0] = %v, want path=/data/reports mode=read", doc.Filesystem[0])
	}
	if len(doc.Models) != 2 {
		return fmt.Errorf("models len = %d, want 2: %s", len(doc.Models), rendered)
	}
	if doc.Models[0]["endpoint"] != "api.anthropic.com" {
		return fmt.Errorf("models[0] endpoint = %v, want api.anthropic.com", doc.Models[0]["endpoint"])
	}
	if _, present := doc.Models[1]["model"]; present {
		return fmt.Errorf("provider-only models[1] carries a model key, want it omitted: %v", doc.Models[1])
	}
	if _, present := doc.Models[1]["endpoint"]; present {
		return fmt.Errorf("provider-only models[1] carries an endpoint key, want it omitted: %v", doc.Models[1])
	}
	return nil
}
