package provider

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// testAccProtoV6ProviderFactories is shared by every TF_ACC-gated
// acceptance test in this package: it runs the real taipanProvider
// in-process over the same protocol v6 wire a real `terraform apply`
// speaks, exercising the tfsdk.Plan/State handling inside each resource's
// Create/Read/Update/Delete that client_test.go and wardryx_client_test.go
// deliberately do not reach (those test the HTTP client layer one level
// down, against an httptest mock, with no real Terraform config or state
// involved at all).
var testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"taipan": providerserver.NewProtocol6WithError(New("acctest")()),
}

// testAccPreCheckCloud fails the test, not skips it, when TOKENFUSE_CLOUD_URL
// or TOKENFUSE_CLOUD_KEY are unset. resource.Test has already skipped for us
// if TF_ACC itself were unset (that is Terraform's own opt-in convention);
// reaching PreCheck at all means the caller explicitly asked for acceptance
// tests to run, so a missing backend here is a misconfiguration to fix, not
// a normal "not applicable" skip.
func testAccPreCheckCloud(t *testing.T) {
	t.Helper()
	if os.Getenv("TOKENFUSE_CLOUD_URL") == "" || os.Getenv("TOKENFUSE_CLOUD_KEY") == "" {
		t.Fatal("TOKENFUSE_CLOUD_URL and TOKENFUSE_CLOUD_KEY must both be set to run this acceptance test against a live TokenFuse Cloud; see README.md's Development section, or scripts/testacc-local.sh for a disposable local instance")
	}
}

// testAccPreCheckWardryx mirrors testAccPreCheckCloud for Wardryx.
func testAccPreCheckWardryx(t *testing.T) {
	t.Helper()
	if os.Getenv("WARDRYX_URL") == "" || os.Getenv("WARDRYX_KEY") == "" {
		t.Fatal("WARDRYX_URL and WARDRYX_KEY must both be set to run this acceptance test against a live Wardryx; see README.md's Development section, or scripts/testacc-local.sh for a disposable local instance")
	}
}

// testAccCloudClient builds a *CloudClient straight from the same
// environment variables the provider itself falls back to (see provider.go's
// Configure), so a CheckDestroy can inspect TokenFuse Cloud's real state
// directly: by the time CheckDestroy runs, the resource is already gone from
// Terraform state, so there is nothing left in *terraform.State to read it
// from.
func testAccCloudClient() *CloudClient {
	return &CloudClient{
		BaseURL:    strings.TrimRight(os.Getenv("TOKENFUSE_CLOUD_URL"), "/"),
		APIKey:     os.Getenv("TOKENFUSE_CLOUD_KEY"),
		HTTPClient: http.DefaultClient,
	}
}

// testAccWardryxClient mirrors testAccCloudClient for Wardryx.
func testAccWardryxClient() *WardryxClient {
	return &WardryxClient{
		BaseURL:    strings.TrimRight(os.Getenv("WARDRYX_URL"), "/"),
		APIKey:     os.Getenv("WARDRYX_KEY"),
		HTTPClient: http.DefaultClient,
	}
}

// testAccCaptureAttr reads resourceName's attr out of the post-apply state
// into *dest, for a later TestStep to compare against -- e.g. proving
// taipan_wardryx_policy's updated_at actually changes on a real Update, not
// just staying whatever Create first observed.
func testAccCaptureAttr(resourceName, attr string, dest *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		v, err := testAccReadAttr(s, resourceName, attr)
		if err != nil {
			return err
		}
		*dest = v
		return nil
	}
}

// testAccCheckAttrChanged asserts resourceName's attr in the current state
// differs from *previous, captured by an earlier testAccCaptureAttr call.
func testAccCheckAttrChanged(resourceName, attr string, previous *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		v, err := testAccReadAttr(s, resourceName, attr)
		if err != nil {
			return err
		}
		if v == *previous {
			return fmt.Errorf("resource %s attribute %q = %q, want it to have changed from the previous step's %q", resourceName, attr, v, *previous)
		}
		return nil
	}
}

func testAccReadAttr(s *terraform.State, resourceName, attr string) (string, error) {
	rs, ok := s.RootModule().Resources[resourceName]
	if !ok {
		return "", fmt.Errorf("resource %s not found in state", resourceName)
	}
	v, ok := rs.Primary.Attributes[attr]
	if !ok {
		return "", fmt.Errorf("resource %s has no attribute %q in state", resourceName, attr)
	}
	return v, nil
}
