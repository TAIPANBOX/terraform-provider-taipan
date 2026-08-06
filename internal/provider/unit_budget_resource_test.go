package provider

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// TestAccUnitBudgetResource exercises taipan_unit_budget's Create -> Update
// -> Import lifecycle against a live TokenFuse Cloud, over the real
// Terraform protocol v6 wire, mirroring TestAccBudgetResource exactly: same
// backend, same shape of test, since taipan_unit_budget follows
// unitBudgetResource's own doc comment ("mirrors budgetResource.applyBudget
// exactly") field for field. Requires TF_ACC=1 plus a live backend; see
// testAccPreCheckCloud.
//
// taipan_unit_budget's Delete is documented as state-only, mirroring
// taipan_budget: TokenFuse Cloud has no unit-budget-delete endpoint (see
// unit_budget_resource.go's Delete). This test's CheckDestroy asserts the
// unit's last-applied budget still exists server-side after resource.Test's
// final implicit destroy, the same non-standard assertion
// testAccCheckBudgetSurvivesDestroy makes for the per-run resource.
func TestAccUnitBudgetResource(t *testing.T) {
	unitID := fmt.Sprintf("taipan-acctest-unit-%d", time.Now().UnixNano())
	const (
		initialLimitUSD = 2000.0
		updatedLimitUSD = 3500.5
	)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheckCloud(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckUnitBudgetSurvivesDestroy(unitID, updatedLimitUSD),
		Steps: []resource.TestStep{
			{
				Config: testAccUnitBudgetResourceConfig(unitID, initialLimitUSD),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("taipan_unit_budget.test", "unit_id", unitID),
					resource.TestCheckResourceAttr("taipan_unit_budget.test", "limit_usd", "2000"),
				),
			},
			{
				Config: testAccUnitBudgetResourceConfig(unitID, updatedLimitUSD),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("taipan_unit_budget.test", "unit_id", unitID),
					resource.TestCheckResourceAttr("taipan_unit_budget.test", "limit_usd", "3500.5"),
				),
			},
			{
				ResourceName: "taipan_unit_budget.test",
				// taipan_unit_budget has no "id" attribute (only unit_id and
				// limit_usd), mirroring taipan_budget: both ImportStateId
				// and ImportStateVerifyIdentifierAttribute must be set
				// explicitly, matching exactly what
				// unit_budget_resource.go's own ImportState passes through
				// (path.Root("unit_id")). Without ImportStateId the import
				// fails outright ("Cannot import non-existent remote
				// object"); without ImportStateVerifyIdentifierAttribute the
				// import succeeds but ImportStateVerify cannot tell which
				// resource instance to diff against -- both confirmed
				// against a real TokenFuse Cloud while writing
				// TestAccBudgetResource, and unchanged here since this
				// resource shares that one's exact identifier shape.
				ImportStateId:                        unitID,
				ImportState:                          true,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "unit_id",
			},
		},
	})
}

func testAccUnitBudgetResourceConfig(unitID string, limitUSD float64) string {
	return fmt.Sprintf(`
resource "taipan_unit_budget" "test" {
  unit_id   = %q
  limit_usd = %v
}
`, unitID, limitUSD)
}

// testAccCheckUnitBudgetSurvivesDestroy asserts taipan_unit_budget's
// documented state-only Delete against a real server, mirroring
// testAccCheckBudgetSurvivesDestroy for the per-run resource.
func testAccCheckUnitBudgetSurvivesDestroy(unitID string, wantLimitUSD float64) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		budgets, err := testAccCloudClient().ListUnitBudgets(context.Background())
		if err != nil {
			return fmt.Errorf("list unit budgets after destroy: %w", err)
		}
		micros, ok := budgets[unitID]
		if !ok {
			return fmt.Errorf("unit %s has no budget after destroy, but taipan_unit_budget's Delete is state-only: TokenFuse Cloud should still report its last-applied value", unitID)
		}
		if got := microsToUSD(micros); got != wantLimitUSD {
			return fmt.Errorf("unit %s budget after destroy = $%v, want the last-applied $%v unchanged", unitID, got, wantLimitUSD)
		}
		return nil
	}
}
