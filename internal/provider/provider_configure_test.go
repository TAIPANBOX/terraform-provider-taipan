package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// configureProvider runs the provider's Configure with the given provider
// block and returns the client bundle every resource then receives.
func configureProvider(t *testing.T, block taipanProviderModel) (*providerClients, *provider.ConfigureResponse) {
	t.Helper()
	p := New("test")()
	var sresp provider.SchemaResponse
	p.Schema(context.Background(), provider.SchemaRequest{}, &sresp)
	st := tfsdk.State{
		Schema: sresp.Schema,
		Raw:    tftypes.NewValue(sresp.Schema.Type().TerraformType(context.Background()), nil),
	}
	if diags := st.Set(context.Background(), &block); diags.HasError() {
		t.Fatalf("encode provider block: %v", diags)
	}
	resp := &provider.ConfigureResponse{}
	p.Configure(context.Background(), provider.ConfigureRequest{
		Config: tfsdk.Config{Schema: sresp.Schema, Raw: st.Raw},
	}, resp)
	if resp.Diagnostics.HasError() {
		return nil, resp
	}
	clients, ok := resp.ResourceData.(*providerClients)
	if !ok {
		t.Fatalf("ResourceData = %T, want *providerClients", resp.ResourceData)
	}
	if resp.DataSourceData != resp.ResourceData {
		t.Error("data sources and resources were handed different client bundles")
	}
	return clients, resp
}

func clearProviderEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"TOKENFUSE_CLOUD_URL", "TOKENFUSE_CLOUD_KEY", "WARDRYX_URL", "WARDRYX_KEY"} {
		t.Setenv(k, "")
	}
}

// TestProviderConfigureBuildsOnlyTheClientsThatAreFullyConfigured: a client
// exists exactly when both its URL and its key resolved, from the block or
// the environment; a half-configured pair yields nil, so only the resources
// that need it fail, each with its own diagnostic.
func TestProviderConfigureBuildsOnlyTheClientsThatAreFullyConfigured(t *testing.T) {
	null := types.StringNull()

	t.Run("nothing configured", func(t *testing.T) {
		clearProviderEnv(t)
		clients, resp := configureProvider(t, taipanProviderModel{CloudURL: null, CloudKey: null, WardryxURL: null, WardryxKey: null})
		if resp.Diagnostics.HasError() {
			t.Fatalf("Configure failed with nothing set; taipan_agent_passport needs no backend: %v", resp.Diagnostics)
		}
		if clients.Cloud != nil || clients.Wardryx != nil {
			t.Errorf("clients = %+v, want both nil", clients)
		}
	})

	t.Run("from the block, trailing slash trimmed", func(t *testing.T) {
		clearProviderEnv(t)
		clients, _ := configureProvider(t, taipanProviderModel{
			CloudURL: types.StringValue("https://cloud.example/"), CloudKey: types.StringValue("k:org:admin"),
			WardryxURL: types.StringValue("https://wardryx.example//"), WardryxKey: types.StringValue("prod-tf"),
		})
		if clients.Cloud == nil || clients.Cloud.BaseURL != "https://cloud.example" || clients.Cloud.APIKey != "k:org:admin" {
			t.Errorf("Cloud = %+v", clients.Cloud)
		}
		if clients.Wardryx == nil || clients.Wardryx.BaseURL != "https://wardryx.example" || clients.Wardryx.APIKey != "prod-tf" {
			t.Errorf("Wardryx = %+v", clients.Wardryx)
		}
		if clients.Cloud.HTTPClient == nil || clients.Cloud.HTTPClient.Timeout == 0 {
			t.Error("the Cloud client has no request timeout")
		}
	})

	t.Run("from the environment", func(t *testing.T) {
		clearProviderEnv(t)
		t.Setenv("TOKENFUSE_CLOUD_URL", "https://env-cloud.example")
		t.Setenv("TOKENFUSE_CLOUD_KEY", "env-key")
		t.Setenv("WARDRYX_URL", "https://env-wardryx.example")
		t.Setenv("WARDRYX_KEY", "env-wkey")
		clients, _ := configureProvider(t, taipanProviderModel{CloudURL: null, CloudKey: null, WardryxURL: null, WardryxKey: null})
		if clients.Cloud == nil || clients.Cloud.BaseURL != "https://env-cloud.example" || clients.Cloud.APIKey != "env-key" {
			t.Errorf("Cloud = %+v", clients.Cloud)
		}
		if clients.Wardryx == nil || clients.Wardryx.BaseURL != "https://env-wardryx.example" || clients.Wardryx.APIKey != "env-wkey" {
			t.Errorf("Wardryx = %+v", clients.Wardryx)
		}
	})

	t.Run("block wins over environment", func(t *testing.T) {
		clearProviderEnv(t)
		t.Setenv("TOKENFUSE_CLOUD_URL", "https://env-cloud.example")
		t.Setenv("TOKENFUSE_CLOUD_KEY", "env-key")
		clients, _ := configureProvider(t, taipanProviderModel{
			CloudURL: types.StringValue("https://block.example"), CloudKey: null, WardryxURL: null, WardryxKey: null,
		})
		if clients.Cloud == nil || clients.Cloud.BaseURL != "https://block.example" || clients.Cloud.APIKey != "env-key" {
			t.Errorf("Cloud = %+v, want the block's URL with the environment's key", clients.Cloud)
		}
	})

	t.Run("half a pair builds nothing", func(t *testing.T) {
		clearProviderEnv(t)
		clients, _ := configureProvider(t, taipanProviderModel{
			CloudURL: types.StringValue("https://cloud.example"), CloudKey: null,
			WardryxURL: null, WardryxKey: types.StringValue("prod-tf"),
		})
		if clients.Cloud != nil {
			t.Errorf("Cloud built without a key: %+v", clients.Cloud)
		}
		if clients.Wardryx != nil {
			t.Errorf("Wardryx built without a URL: %+v", clients.Wardryx)
		}
	})
}

// TestProviderConfigureRefusesAConfigItCannotDecode: a config of the wrong
// shape is an error, and no client bundle is handed out.
func TestProviderConfigureRefusesAConfigItCannotDecode(t *testing.T) {
	p := New("test")()
	s := resourceSchema(t, &budgetResource{})
	st := stateOf(t, s, &budgetResourceModel{RunID: types.StringValue("x"), LimitUSD: types.Float64Value(1)})
	resp := &provider.ConfigureResponse{}
	p.Configure(context.Background(), provider.ConfigureRequest{Config: tfsdk.Config{Schema: s, Raw: st.Raw}}, resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("Configure decoded a config of the wrong shape")
	}
	if resp.ResourceData != nil {
		t.Errorf("ResourceData = %v after a failed Configure", resp.ResourceData)
	}
}
