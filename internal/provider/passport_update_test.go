package provider

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// passportResource.Update was at 0%. It is the only place that removes the
// PREVIOUS file when an operator moves a passport, and forgetting to leaves two
// passports on disk for one agent, one of them stale.
//
// A stale passport is not a broken one. It parses, it validates, and whatever
// reads it gets an attestation that used to be true. That is the failure worth
// a test, and no unit test can see it: it is about a file surviving across two
// applies.

func TestAccAgentPassportResource_MovingTheOutputPathLeavesNoStaleCopyBehind(t *testing.T) {
	dir := t.TempDir()
	// Both directories are created here on purpose. output_path does NOT
	// create its parent, and the test below pins that.
	firstDir := filepath.Join(dir, "first")
	secondDir := filepath.Join(dir, "second")
	for _, d := range []string{firstDir, secondDir} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatalf("preparing %s: %v", d, err)
		}
	}
	first := filepath.Join(firstDir, "passport.json")
	second := filepath.Join(secondDir, "passport.json")

	// No PreCheck, like the other two passport acceptance tests:
	// taipan_agent_passport calls no API, so this needs a terraform or tofu
	// binary on PATH and no live backend at all.
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccAgentPassportImportConfig(first),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckPassportFileExists(first),
				),
			},
			{
				// The operator moves it. Terraform plans an update rather than
				// a replace, so Update is what has to clean up after itself.
				Config: testAccAgentPassportImportConfig(second),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckPassportFileExists(second),
					testAccCheckPassportFileAbsent(first),
				),
			},
		},
	})
}

func testAccCheckPassportFileExists(path string) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		b, err := os.ReadFile(path) // #nosec G304 -- a path this test itself created
		if err != nil {
			return fmt.Errorf("passport was not written to %s: %w", path, err)
		}
		if len(b) == 0 {
			return fmt.Errorf("passport at %s is empty", path)
		}
		return nil
	}
}

func testAccCheckPassportFileAbsent(path string) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf(
				"the passport is still at %s after being moved: two passports now exist "+
					"for one agent and the older one still parses and validates, so whatever "+
					"reads it gets an attestation that used to be true", path)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("checking %s: %w", path, err)
		}
		return nil
	}
}

// The provider does not create output_path's parent directory, and that is a
// decision rather than an omission: an apply that silently makes directories
// on an operator's filesystem is a side effect they did not write down. What
// has to hold is that the refusal is LOUD. A silent mkdir and a silent skip
// look identical from the plan, and both end with an operator believing a
// passport is on disk somewhere it is not.
//
// This also covers Create's write-failure diagnostic, which no unit test
// reaches.
func TestAccAgentPassportResource_MissingParentDirectoryFailsLoudlyRatherThanCreatingIt(t *testing.T) {
	dir := t.TempDir()
	absent := filepath.Join(dir, "not-created-by-anyone", "passport.json")

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      testAccAgentPassportImportConfig(absent),
				ExpectError: regexp.MustCompile(`Unable to write taipan_agent_passport output_path`),
			},
		},
	})

	if _, err := os.Stat(filepath.Dir(absent)); !os.IsNotExist(err) {
		t.Fatalf("the provider created %s: apply is making directories nobody asked for", filepath.Dir(absent))
	}
}
