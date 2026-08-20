package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// The branch a unit test cannot reach and an operator meets for real: somebody
// deletes a policy in Wardryx without going through Terraform.
//
// Read handles the 404 by removing the resource from state, so the next plan
// says "create" rather than "no changes". Getting that wrong is not an error
// anybody sees. Terraform reports a clean plan, the operator believes the rule
// is enforced, and Wardryx is not enforcing it. That is the whole reason the
// branch exists and it was covered by nothing: wardryx_policy's Read sat at
// 52.9%, and this was the missing half.
//
// CLAUDE.md says a green unit run means very little in a Terraform provider,
// and names this class exactly. Only an acceptance step can see it.
func TestAccWardryxPolicyResource_DeletedOutOfBandIsRecreatedNotReportedClean(t *testing.T) {
	id := fmt.Sprintf("taipan-acctest-drift-%d", time.Now().UnixNano())
	var beforeDrift string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheckWardryx(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckWardryxPolicyDestroyed(id),
		Steps: []resource.TestStep{
			{
				Config: testAccWardryxPolicyResourceConfig(
					id, "agent://taipan-acctest-drift/*", []string{"shell_exec"}, 5, 25, 10, true),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("taipan_wardryx_policy.test", "id", id),
					testAccCaptureAttr("taipan_wardryx_policy.test", "updated_at", &beforeDrift),
				),
			},
			{
				// Delete it behind Terraform's back, the way an operator with
				// wardryx admin credentials and a hurry would.
				//
				// The sleep is the same one the lifecycle test needs: Wardryx
				// stamps updated_at with second granularity, so a recreate
				// inside the same second would be indistinguishable from no
				// change at all, and this test turns on that difference.
				PreConfig: func() {
					time.Sleep(1100 * time.Millisecond)
					if err := testAccWardryxClient().DeletePolicy(context.Background(), id); err != nil {
						t.Fatalf("deleting policy %s out of band: %v", id, err)
					}
				},
				Config: testAccWardryxPolicyResourceConfig(
					id, "agent://taipan-acctest-drift/*", []string{"shell_exec"}, 5, 25, 10, true),
				Check: resource.ComposeAggregateTestCheckFunc(
					// The rule is back in Wardryx. If Read had kept the stale
					// state instead of removing it, the plan would have been
					// empty, nothing would have been applied, and Wardryx would
					// still be missing a policy Terraform reports as present.
					testAccCheckWardryxPolicyExists(id),
					// And it is genuinely a new write rather than the old row
					// still being read.
					testAccCheckAttrChanged("taipan_wardryx_policy.test", "updated_at", &beforeDrift),
				),
			},
		},
	})
}

// testAccCheckWardryxPolicyExists is the inverse of the destroy checker: it
// asserts the rule is actually in Wardryx, not merely in Terraform's state.
// The difference between those two is what this whole test is about.
func testAccCheckWardryxPolicyExists(id string) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		if _, err := testAccWardryxClient().GetPolicy(context.Background(), id); err != nil {
			var apiErr *APIError
			if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
				return fmt.Errorf(
					"policy %s is absent from Wardryx while Terraform reports it present: "+
						"a plan that read clean over a deleted rule is the failure this test exists for", id)
			}
			return fmt.Errorf("reading policy %s back from Wardryx: %w", id, err)
		}
		return nil
	}
}
