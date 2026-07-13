package provider

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// TestAccBudgetResource exercises taipan_budget's Create -> Update -> Import
// lifecycle against a live TokenFuse Cloud, over the real Terraform
// protocol v6 wire (not the in-process Go calls client_test.go exercises
// one level down against an httptest mock). Requires TF_ACC=1 plus a live
// backend; see testAccPreCheckCloud.
//
// taipan_budget's Delete is documented as state-only: TokenFuse Cloud has
// no budget-delete endpoint (see budget_resource.go's Delete). This test's
// CheckDestroy deliberately asserts the opposite of the usual convention:
// that the run's last-applied budget still exists server-side after
// resource.Test's final implicit destroy, not that it is gone. A
// CheckDestroy asserting absence here would be asserting behavior this
// resource does not have.
func TestAccBudgetResource(t *testing.T) {
	runID := fmt.Sprintf("taipan-acctest-budget-%d", time.Now().UnixNano())
	const (
		initialLimitUSD = 10.5
		updatedLimitUSD = 20.25
	)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheckCloud(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBudgetSurvivesDestroy(runID, updatedLimitUSD),
		Steps: []resource.TestStep{
			{
				Config: testAccBudgetResourceConfig(runID, initialLimitUSD),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("taipan_budget.test", "run_id", runID),
					resource.TestCheckResourceAttr("taipan_budget.test", "limit_usd", "10.5"),
				),
			},
			{
				Config: testAccBudgetResourceConfig(runID, updatedLimitUSD),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("taipan_budget.test", "run_id", runID),
					resource.TestCheckResourceAttr("taipan_budget.test", "limit_usd", "20.25"),
				),
			},
			{
				ResourceName: "taipan_budget.test",
				// taipan_budget has no "id" attribute (only run_id and
				// limit_usd); both ImportStateId and
				// ImportStateVerifyIdentifierAttribute must be set
				// explicitly, matching exactly what budget_resource.go's own
				// ImportState passes through (path.Root("run_id")). The test
				// framework otherwise falls back to its SDKv2-era default of
				// an "id" attribute that does not exist here: without
				// ImportStateId it fails the import outright ("Cannot import
				// non-existent remote object"), and without
				// ImportStateVerifyIdentifierAttribute the import itself
				// succeeds but ImportStateVerify can't tell which resource
				// instance to diff against and fails with "New resource
				// missing identifier attribute \"id\"" -- confirmed both
				// ways against a real TokenFuse Cloud while writing this
				// test.
				ImportStateId:                        runID,
				ImportState:                          true,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "run_id",
			},
		},
	})
}

func testAccBudgetResourceConfig(runID string, limitUSD float64) string {
	return fmt.Sprintf(`
resource "taipan_budget" "test" {
  run_id    = %q
  limit_usd = %v
}
`, runID, limitUSD)
}

// testAccCheckBudgetSurvivesDestroy asserts taipan_budget's documented
// state-only Delete against a real server. See the doc comment on
// TestAccBudgetResource.
func testAccCheckBudgetSurvivesDestroy(runID string, wantLimitUSD float64) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		budgets, err := testAccCloudClient().ListBudgets(context.Background())
		if err != nil {
			return fmt.Errorf("list budgets after destroy: %w", err)
		}
		micros, ok := budgets[runID]
		if !ok {
			return fmt.Errorf("run %s has no budget after destroy, but taipan_budget's Delete is state-only: TokenFuse Cloud should still report its last-applied value", runID)
		}
		if got := microsToUSD(micros); got != wantLimitUSD {
			return fmt.Errorf("run %s budget after destroy = $%v, want the last-applied $%v unchanged", runID, got, wantLimitUSD)
		}
		return nil
	}
}
