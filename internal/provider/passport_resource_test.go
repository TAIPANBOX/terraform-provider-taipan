package provider

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TAIPANBOX/agent-stack-go/passport"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

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
	for _, field := range []string{"display_name", "runtime", "parent", "attestation", "labels", "created_at"} {
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
