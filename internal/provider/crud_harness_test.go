package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// The helpers in this file drive a resource's Create/Read/Update/Delete and
// ImportState methods directly, with tfsdk.Plan/State values built from the
// resource's own schema, against an httptest backend. They sit between the
// client-level httptest tests (which never touch tfsdk) and the TF_ACC suite
// (which needs the terraform binary and live backends): they see the
// plan -> request -> response -> state mapping inside each method, which is
// where a silently wrong value would land in someone's state.
//
// What they cannot see, and the acceptance suite still owns: plan modifiers
// (RequiresReplace), defaults applied by the framework during planning, and
// the "provider produced inconsistent result" check Terraform core makes
// between plan and apply.

// resourceSchema returns r's schema as the framework would serve it.
func resourceSchema(t *testing.T, r resource.Resource) schema.Schema {
	t.Helper()
	var resp resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema: %v", resp.Diagnostics)
	}
	return resp.Schema
}

// nullState is the state a resource starts from before Create, or that an
// import fills in: the schema's object type with a null value.
func nullState(s schema.Schema) tfsdk.State {
	return tfsdk.State{
		Schema: s,
		Raw:    tftypes.NewValue(s.Type().TerraformType(context.Background()), nil),
	}
}

// stateOf encodes model (a *xxxResourceModel) as a tfsdk.State of schema s.
func stateOf(t *testing.T, s schema.Schema, model any) tfsdk.State {
	t.Helper()
	st := nullState(s)
	if diags := st.Set(context.Background(), model); diags.HasError() {
		t.Fatalf("encode state: %v", diags)
	}
	return st
}

// planOf encodes model as a tfsdk.Plan of schema s.
func planOf(t *testing.T, s schema.Schema, model any) tfsdk.Plan {
	t.Helper()
	st := stateOf(t, s, model)
	return tfsdk.Plan{Schema: s, Raw: st.Raw}
}

// decodeState reads st back into model, failing the test on any diagnostic.
func decodeState(t *testing.T, st tfsdk.State, model any) {
	t.Helper()
	if diags := st.Get(context.Background(), model); diags.HasError() {
		t.Fatalf("decode state: %v", diags)
	}
}

// recordedRequest is one request a fake backend received.
type recordedRequest struct {
	Method string
	Path   string
	Auth   string
	Body   string
}

// fakeBackend is an httptest server that records every request and answers
// each one with whatever respond returns.
type fakeBackend struct {
	*httptest.Server
	requests []recordedRequest
}

func newFakeBackend(t *testing.T, respond func(r *http.Request) (int, string)) *fakeBackend {
	t.Helper()
	fb := &fakeBackend{}
	fb.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, 0)
		if r.Body != nil {
			buf := make([]byte, 4096)
			for {
				n, err := r.Body.Read(buf)
				body = append(body, buf[:n]...)
				if err != nil {
					break
				}
			}
		}
		fb.requests = append(fb.requests, recordedRequest{
			Method: r.Method,
			Path:   r.URL.Path,
			Auth:   r.Header.Get("Authorization"),
			Body:   string(body),
		})
		status, payload := respond(r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(fb.Close)
	return fb
}

// refuseEveryCall is a respond func for a backend the method under test must
// not call at all.
func refuseEveryCall(t *testing.T) func(r *http.Request) (int, string) {
	return func(r *http.Request) (int, string) {
		t.Errorf("unexpected backend call: %s %s", r.Method, r.URL.Path)
		return http.StatusTeapot, `{"error":"no call expected"}`
	}
}
