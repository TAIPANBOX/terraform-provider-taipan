package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// TestAccWardryxPolicyResource exercises taipan_wardryx_policy's full
// Create -> Update -> Import -> Destroy lifecycle against a live Wardryx,
// over the real Terraform protocol v6 wire. Requires TF_ACC=1 plus a live
// backend; see testAccPreCheckWardryx.
//
// Unlike taipan_budget, Wardryx's policy API has a real DELETE
// (wardryx_policy_resource.go's Delete), so CheckDestroy here asserts the
// usual convention: the policy is actually gone (a 404) after
// resource.Test's final implicit destroy.
func TestAccWardryxPolicyResource(t *testing.T) {
	id := fmt.Sprintf("taipan-acctest-policy-%d", time.Now().UnixNano())
	var firstUpdatedAt string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheckWardryx(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckWardryxPolicyDestroyed(id),
		Steps: []resource.TestStep{
			{
				// deny_if_unattested: true and a non-zero require_human_above_usd
				// / deny_above_usd / max_steps deliberately exercise every
				// Optional+Computed field away from its Default, so this step
				// also proves the Default doesn't leak into a value the config
				// actually set (see the schema's own comment on why a mismatched
				// Default previously broke against a real server).
				Config: testAccWardryxPolicyResourceConfig(id, "agent://taipan-acctest/*", []string{"shell_exec"}, 5, 25, 10, true),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("taipan_wardryx_policy.test", "id", id),
					resource.TestCheckResourceAttr("taipan_wardryx_policy.test", "target", "agent://taipan-acctest/*"),
					resource.TestCheckResourceAttr("taipan_wardryx_policy.test", "deny_tool.#", "1"),
					resource.TestCheckResourceAttr("taipan_wardryx_policy.test", "deny_tool.0", "shell_exec"),
					resource.TestCheckResourceAttr("taipan_wardryx_policy.test", "allow_domains.#", "0"),
					resource.TestCheckResourceAttr("taipan_wardryx_policy.test", "require_human_above_usd", "5"),
					resource.TestCheckResourceAttr("taipan_wardryx_policy.test", "deny_above_usd", "25"),
					resource.TestCheckResourceAttr("taipan_wardryx_policy.test", "max_steps", "10"),
					resource.TestCheckResourceAttr("taipan_wardryx_policy.test", "deny_if_unattested", "true"),
					resource.TestCheckResourceAttrSet("taipan_wardryx_policy.test", "updated_at"),
					testAccCaptureAttr("taipan_wardryx_policy.test", "updated_at", &firstUpdatedAt),
				),
			},
			{
				// Wardryx stamps updated_at with time.RFC3339 (second
				// granularity, no fractional seconds -- internal/api/api.go),
				// and this step's PUT can otherwise land within the same
				// wall-clock second as the previous step's, which would make
				// testAccCheckAttrChanged below fail on a real, working
				// server for no reason. Crossing a full second first makes
				// the assertion actually mean something.
				PreConfig: func() { time.Sleep(1100 * time.Millisecond) },
				// Every field that has a Default now goes back to it (deny_tool
				// grows instead, require_human_above_usd/deny_if_unattested drop
				// to their zero values) -- proving Update round-trips a field
				// back to its Default correctly, not just away from it.
				Config: testAccWardryxPolicyResourceConfig(id, "agent://taipan-acctest/*", []string{"shell_exec", "http_fetch"}, 0, 50, 20, false),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("taipan_wardryx_policy.test", "deny_tool.#", "2"),
					resource.TestCheckResourceAttr("taipan_wardryx_policy.test", "deny_tool.1", "http_fetch"),
					resource.TestCheckResourceAttr("taipan_wardryx_policy.test", "require_human_above_usd", "0"),
					resource.TestCheckResourceAttr("taipan_wardryx_policy.test", "deny_above_usd", "50"),
					resource.TestCheckResourceAttr("taipan_wardryx_policy.test", "max_steps", "20"),
					resource.TestCheckResourceAttr("taipan_wardryx_policy.test", "deny_if_unattested", "false"),
					// Proves the PUT actually reached Wardryx and Wardryx actually
					// re-stamped updated_at, not that the provider just echoed the
					// plan's values back into state unchanged.
					testAccCheckAttrChanged("taipan_wardryx_policy.test", "updated_at", &firstUpdatedAt),
				),
			},
			{
				ResourceName: "taipan_wardryx_policy.test",
				// Explicit for the same reason as taipan_budget's own
				// acceptance test: matches exactly what
				// wardryx_policy_resource.go's ImportState passes through
				// (path.Root("id")), rather than relying on the test
				// framework's default "id" attribute lookup.
				ImportStateId:     id,
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

func testAccWardryxPolicyResourceConfig(id, target string, denyTools []string, requireHumanAboveUSD, denyAboveUSD float64, maxSteps int64, denyIfUnattested bool) string {
	quoted := make([]string, len(denyTools))
	for i, tool := range denyTools {
		quoted[i] = strconv.Quote(tool)
	}
	return fmt.Sprintf(`
resource "taipan_wardryx_policy" "test" {
  id                       = %q
  target                   = %q
  deny_tool                = [%s]
  require_human_above_usd  = %v
  deny_above_usd           = %v
  max_steps                = %d
  deny_if_unattested       = %v
}
`, id, target, strings.Join(quoted, ", "), requireHumanAboveUSD, denyAboveUSD, maxSteps, denyIfUnattested)
}

// testAccCheckWardryxPolicyDestroyed asserts taipan_wardryx_policy's real
// DELETE actually took effect. See the doc comment on
// TestAccWardryxPolicyResource.
func testAccCheckWardryxPolicyDestroyed(id string) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		_, err := testAccWardryxClient().GetPolicy(context.Background(), id)
		if err == nil {
			return fmt.Errorf("policy %s still exists in Wardryx after destroy", id)
		}
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotFound {
			return fmt.Errorf("expected a 404 confirming policy %s was deleted, got: %w", id, err)
		}
		return nil
	}
}
