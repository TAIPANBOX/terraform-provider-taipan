package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/TAIPANBOX/agent-stack-go/v2/passport"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// canonicalAttestationMethods returns the closed attestation.method enum of
// the agent-passport schema this provider renders (v0.1, passport.RequiredSchema),
// read out of the copy the PINNED agent-stack-go module carries at
// passport/testdata/schema/agent-passport.schema.json. agent-stack-go's
// schemas-in-sync.sh holds that copy byte-identical to agent-passport's own
// schemas/, so this is the canonical list at the version go.mod names.
//
// Reading it from the pin instead of writing the names out here is the point.
// The attestation_method description drifted by being a copy: agent-passport
// added dpop-key on 2026-08-26 and the description went on listing five. A
// copy in this test would drift the same way. Read from the pin, the tests
// below go red in the very pull request that bumps agent-stack-go past a
// change to the enum.
func canonicalAttestationMethods(t *testing.T) []string {
	t.Helper()
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/TAIPANBOX/agent-stack-go/v2").CombinedOutput()
	if err != nil {
		t.Fatalf("locating the pinned agent-stack-go module: %v\n%s", err, out)
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" {
		t.Fatal("go list named no directory for agent-stack-go, so this measured nothing")
	}

	path := filepath.Join(dir, "passport", "testdata", "schema", "agent-passport.schema.json")
	raw, err := os.ReadFile(path) // #nosec G304 -- a fixed path inside the module cache
	if err != nil {
		t.Fatalf("reading the agent-passport schema the pinned agent-stack-go carries: %v", err)
	}
	var schema struct {
		Properties struct {
			Attestation struct {
				Properties struct {
					Method struct {
						Enum []string `json:"enum"`
					} `json:"method"`
				} `json:"properties"`
			} `json:"attestation"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	methods := schema.Properties.Attestation.Properties.Method.Enum
	if len(methods) == 0 {
		t.Fatalf("%s carries no attestation.method enum, so this measured nothing", path)
	}
	return methods
}

// TestAttestationMethodDescriptionListsExactlyTheCanonicalMethods holds the
// attestation_method description, which is what a Registry reader sees in
// docs/resources/agent_passport.md, to the canonical enum: the same methods,
// in the same order, none missing and none invented.
func TestAttestationMethodDescriptionListsExactlyTheCanonicalMethods(t *testing.T) {
	want := canonicalAttestationMethods(t)

	var resp fwresource.SchemaResponse
	NewAgentPassportResource().Schema(context.Background(), fwresource.SchemaRequest{}, &resp)
	attr, ok := resp.Schema.Attributes["attestation_method"]
	if !ok {
		t.Fatal("taipan_agent_passport has no attestation_method attribute, so this measured nothing")
	}
	desc := attr.GetDescription()

	// The list runs from "one of " to the first "." or "(", neither of which
	// can appear inside a method name.
	_, rest, found := strings.Cut(desc, "one of ")
	if !found {
		t.Fatalf("the description no longer carries a \"one of ...\" list, so this measured nothing: %q", desc)
	}
	if end := strings.IndexAny(rest, ".("); end >= 0 {
		rest = rest[:end]
	}
	got := strings.Split(strings.TrimSpace(rest), ", ")

	if !slices.Equal(got, want) {
		t.Errorf("attestation_method description lists %q, the agent-passport schema the pinned agent-stack-go carries lists %q:\n%s", got, want, desc)
	}
}

// TestRenderPassport_EveryCanonicalAttestationMethodRoundTrips renders a
// passport under each canonical method and reads it back through
// passport.Parse, the function Idryx runs on ingest. Nothing in this provider
// or in Parse checks the method against a list today; this pins that every
// method the spec names, dpop-key included, still renders and parses if
// either one ever starts to.
func TestRenderPassport_EveryCanonicalAttestationMethodRoundTrips(t *testing.T) {
	for _, method := range canonicalAttestationMethods(t) {
		t.Run(method, func(t *testing.T) {
			detail := "detail-for-" + method
			data := &passportResourceModel{
				ID:                types.StringValue("agent://acme.example/payments/settler"),
				Owner:             types.StringValue("team-payments@acme.example"),
				AttestationMethod: types.StringValue(method),
				AttestationDetail: types.StringValue(detail),
			}

			rendered, err := renderPassport(context.Background(), data)
			if err != nil {
				t.Fatalf("renderPassport with attestation_method %q: %v", method, err)
			}
			p, err := passport.Parse(rendered)
			if err != nil {
				t.Fatalf("passport.Parse(rendered) with attestation_method %q: %v", method, err)
			}
			if p.Attestation == nil || p.Attestation.Method != method || p.Attestation.Detail != detail {
				t.Errorf("Attestation = %+v, want Method=%q Detail=%q:\n%s", p.Attestation, method, detail, rendered)
			}
		})
	}
}

// testAccAgentPassportDpopKeyConfig is a passport attested by dpop-key
// (agent-passport SPEC 4.3: the workload proves possession of a key on every
// request, RFC 9449, and detail carries the key's RFC 7638 thumbprint). The
// detail here is a readable placeholder, not a real thumbprint, since the
// provider does not interpret it.
func testAccAgentPassportDpopKeyConfig(outputPath string) string {
	return fmt.Sprintf(`
resource "taipan_agent_passport" "acc" {
  id                 = "agent://acme.example/payments/settler"
  owner              = "team-payments@acme.example"
  attestation_method = "dpop-key"
  attestation_detail = "rfc7638-thumbprint-placeholder"
  output_path        = %q
}
`, outputPath)
}

// TestAccAgentPassportResource_DpopKeyAttestationAppliesAndImports takes
// attestation_method = "dpop-key" through a real terraform apply, the
// empty re-plan terraform-plugin-testing runs after every apply step, and an
// import from the file on disk, which reads it back through passport.Parse.
// Runs solely under TF_ACC, no PreCheck: taipan_agent_passport calls no API,
// so this needs a terraform/tofu binary on PATH and no live backend.
func TestAccAgentPassportResource_DpopKeyAttestationAppliesAndImports(t *testing.T) {
	outputPath := filepath.Join(t.TempDir(), "settler.json")

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccAgentPassportDpopKeyConfig(outputPath),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("taipan_agent_passport.acc", "attestation_method", "dpop-key"),
					resource.TestCheckResourceAttrWith("taipan_agent_passport.acc", "json", func(rendered string) error {
						p, err := passport.Parse([]byte(rendered))
						if err != nil {
							return fmt.Errorf("computed json does not parse as a passport: %w", err)
						}
						if p.Attestation == nil || p.Attestation.Method != "dpop-key" || p.Attestation.Detail != "rfc7638-thumbprint-placeholder" {
							return fmt.Errorf("attestation = %+v, want method dpop-key with its detail: %s", p.Attestation, rendered)
						}
						return nil
					}),
				),
			},
			{
				ResourceName:      "taipan_agent_passport.acc",
				ImportStateId:     outputPath,
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}
