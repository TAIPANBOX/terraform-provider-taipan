package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	pschema "github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	rschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
)

// Holds invariant 3 of CLAUDE.md: no credential ever reaches state, a log line,
// or an error message.
//
// Terraform state is frequently stored unencrypted and shared, so an attribute
// that carries a secret and is not marked Sensitive publishes it to everyone
// with read access to the backend. Nothing about that failure is loud: the plan
// works, the apply works, and the value sits in a JSON file somebody later
// commits or copies into a bug report.
//
// The check walks the REAL schema objects rather than the source text, so it
// sees the schema the framework will actually use, including nested attributes,
// and cannot be fooled by a differently formatted declaration.
//
// It is name-driven on purpose. It cannot know which attributes carry secrets,
// so it applies the rule a reviewer would: anything whose name says key, secret,
// token, password or credential must be Sensitive. That catches the regression
// mode, which is a new attribute added without the marker. An attribute holding
// a secret under a name that says nothing is beyond any automatic check and
// stays a matter for review.
var secretShaped = []string{"key", "secret", "token", "password", "credential", "passwd"}

// Names that contain a secret-shaped word but are not secrets. Each needs a
// reason, so that adding to this list is a decision rather than a shortcut.
var notActuallySecret = map[string]string{
	"api_key_id": "an identifier for a key, not the key",
}

func looksSecret(name string) bool {
	if _, ok := notActuallySecret[name]; ok {
		return false
	}
	lower := strings.ToLower(name)
	for _, w := range secretShaped {
		if strings.Contains(lower, w) {
			return true
		}
	}
	return false
}

func checkResourceAttrs(t *testing.T, where string, attrs map[string]rschema.Attribute) {
	t.Helper()
	for name, attr := range attrs {
		path := where + "." + name
		if looksSecret(name) && !attr.IsSensitive() {
			t.Errorf(
				"%s is named like a credential and is not Sensitive. Terraform "+
					"state is often stored unencrypted and shared, so an unmarked "+
					"secret is published to everyone who can read the backend. "+
					"Add Sensitive: true, or, if it genuinely holds no secret, add "+
					"it to notActuallySecret with a reason.",
				path,
			)
		}
		if nested, ok := attr.(rschema.NestedAttribute); ok {
			obj := nested.GetNestedObject()
			if obj != nil {
				inner := map[string]rschema.Attribute{}
				for k, v := range obj.GetAttributes() {
					if a, ok := v.(rschema.Attribute); ok {
						inner[k] = a
					}
				}
				checkResourceAttrs(t, path, inner)
			}
		}
	}
}

func TestEverySecretShapedResourceAttributeIsSensitive(t *testing.T) {
	p := New("test")().(*taipanProvider)

	resources := p.Resources(context.Background())
	if len(resources) == 0 {
		t.Fatal("the provider exposes no resources, so this test measured nothing")
	}

	checked := 0
	for _, mk := range resources {
		r := mk()
		var meta resource.MetadataResponse
		r.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "taipan"}, &meta)

		var resp resource.SchemaResponse
		r.Schema(context.Background(), resource.SchemaRequest{}, &resp)
		if len(resp.Schema.Attributes) == 0 {
			t.Errorf("%s declares no attributes, which is suspicious", meta.TypeName)
			continue
		}
		checked += len(resp.Schema.Attributes)
		checkResourceAttrs(t, meta.TypeName, resp.Schema.Attributes)
	}

	if checked == 0 {
		t.Fatal("no attributes were inspected, so this test measured nothing")
	}
	t.Logf("%d top-level resource attributes inspected across %d resources", checked, len(resources))
}

func TestEverySecretShapedProviderAttributeIsSensitive(t *testing.T) {
	p := New("test")()

	var resp provider.SchemaResponse
	p.Schema(context.Background(), provider.SchemaRequest{}, &resp)

	if len(resp.Schema.Attributes) == 0 {
		t.Fatal("the provider schema declares no attributes, so this test measured nothing")
	}

	found := 0
	for name, attr := range resp.Schema.Attributes {
		if looksSecret(name) {
			found++
			if !attr.IsSensitive() {
				t.Errorf(
					"provider attribute %q is named like a credential and is not "+
						"Sensitive. It goes into state for every configuration that "+
						"sets it.",
					name,
				)
			}
		}
		_ = pschema.Schema{} // keep the provider schema import meaningful
	}

	// The provider carries cloud_key and wardryx_key. If neither is present any
	// more, the test is watching something that no longer exists and should be
	// re-pointed rather than left green.
	if found == 0 {
		t.Fatal(
			"no secret-shaped provider attribute was found at all. The provider " +
				"used to carry cloud_key and wardryx_key; if they were renamed, " +
				"re-point this test rather than letting it pass on an empty set.",
		)
	}
}

func TestDataSourcesDeclareNothingUnmarked(t *testing.T) {
	p := New("test")().(*taipanProvider)

	dss := p.DataSources(context.Background())
	for _, mk := range dss {
		d := mk()
		var meta datasource.MetadataResponse
		d.Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "taipan"}, &meta)

		var resp datasource.SchemaResponse
		d.Schema(context.Background(), datasource.SchemaRequest{}, &resp)
		for name, attr := range resp.Schema.Attributes {
			if looksSecret(name) && !attr.IsSensitive() {
				t.Errorf("data source %s.%s is named like a credential and is not Sensitive", meta.TypeName, name)
			}
			_ = dschema.Schema{}
		}
	}
	// Zero data sources today. This test exists so the first one added does not
	// arrive unexamined.
	t.Logf("%d data sources inspected", len(dss))
}
