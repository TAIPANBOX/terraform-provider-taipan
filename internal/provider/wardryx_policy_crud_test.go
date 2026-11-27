package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// policyServerReply is what Wardryx answers to PUT/GET: the stored policy
// with the server's own name default (target, when name was empty) and
// updated_at.
const policyServerReply = `{
  "id": "acct-policy",
  "name": "no shell for finance agents",
  "target": "agent://finance.local/*",
  "deny_tool": ["shell_exec", "file_write"],
  "allow_domains": ["api.example.com"],
  "require_human_above_usd": 5,
  "deny_above_usd": 25,
  "max_steps": 10,
  "deny_if_unattested": true,
  "updated_at": "2026-11-26T10:00:00Z"
}`

// TestWardryxPolicyApplySendsThePlanAndStoresTheServerRecord: Create and
// Update both PUT the planned document under the planned id, and state is
// rebuilt from Wardryx's response, which is the only place updated_at comes
// from.
func TestWardryxPolicyApplySendsThePlanAndStoresTheServerRecord(t *testing.T) {
	for _, op := range []string{"Create", "Update"} {
		t.Run(op, func(t *testing.T) {
			fb := newFakeBackend(t, func(r *http.Request) (int, string) {
				return http.StatusOK, policyServerReply
			})
			r := &wardryxPolicyResource{client: newTestWardryxClient(fb.URL)}
			s := resourceSchema(t, r)
			planned := fullPolicyModel(t)
			planned.UpdatedAt = types.StringUnknown()
			plan := planOf(t, s, &planned)

			var st = nullState(s)
			ctx := context.Background()
			if op == "Create" {
				resp := resource.CreateResponse{State: st}
				r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
				if resp.Diagnostics.HasError() {
					t.Fatalf("Create: %v", resp.Diagnostics)
				}
				st = resp.State
			} else {
				prior := fullPolicyModel(t)
				prior.UpdatedAt = types.StringValue("2026-01-01T00:00:00Z")
				resp := resource.UpdateResponse{State: stateOf(t, s, &prior)}
				r.Update(ctx, resource.UpdateRequest{Plan: plan, State: resp.State}, &resp)
				if resp.Diagnostics.HasError() {
					t.Fatalf("Update: %v", resp.Diagnostics)
				}
				st = resp.State
			}

			if len(fb.requests) != 1 {
				t.Fatalf("backend saw %d requests, want 1", len(fb.requests))
			}
			req := fb.requests[0]
			if req.Method != http.MethodPut || req.Path != "/v1/policies/acct-policy" {
				t.Errorf("request = %s %s, want PUT /v1/policies/acct-policy", req.Method, req.Path)
			}
			if req.Auth != "Bearer testorg:admin:key" {
				t.Errorf("Authorization = %q", req.Auth)
			}
			var sent WardryxPolicyDocument
			if err := json.Unmarshal([]byte(req.Body), &sent); err != nil {
				t.Fatalf("request body: %v", err)
			}
			if sent.Target != "agent://finance.local/*" || sent.MaxSteps != 10 || !sent.DenyIfUnattested ||
				len(sent.DenyTool) != 2 || sent.DenyAboveUSD != 25 {
				t.Errorf("sent document = %+v, want the planned policy", sent)
			}

			var got wardryxPolicyResourceModel
			decodeState(t, st, &got)
			if got.UpdatedAt.ValueString() != "2026-11-26T10:00:00Z" {
				t.Errorf("updated_at = %q, want the server's timestamp", got.UpdatedAt.ValueString())
			}
			if got.ID.ValueString() != "acct-policy" || got.Target.ValueString() != "agent://finance.local/*" {
				t.Errorf("state = %+v", got)
			}
		})
	}
}

// TestWardryxPolicyApplyTakesTheServerNameDefault: left empty in the plan,
// name comes back as whatever Wardryx stored; state carries the server's
// value, not the planned empty string.
func TestWardryxPolicyApplyTakesTheServerNameDefault(t *testing.T) {
	fb := newFakeBackend(t, func(r *http.Request) (int, string) {
		return http.StatusOK, `{"id":"p1","name":"agent://x/*","target":"agent://x/*","updated_at":"t1"}`
	})
	r := &wardryxPolicyResource{client: newTestWardryxClient(fb.URL)}
	s := resourceSchema(t, r)
	planned := wardryxPolicyResourceModel{
		ID: types.StringValue("p1"), Name: types.StringValue(""), Target: types.StringValue("agent://x/*"),
		DenyTool: emptyStringList, AllowDomains: emptyStringList,
		RequireHumanAboveUSD: types.Float64Value(0), DenyAboveUSD: types.Float64Value(0),
		MaxSteps: types.Int64Value(0), DenyIfUnattested: types.BoolValue(false), UpdatedAt: types.StringUnknown(),
	}
	resp := resource.CreateResponse{State: nullState(s)}
	r.Create(context.Background(), resource.CreateRequest{Plan: planOf(t, s, &planned)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create: %v", resp.Diagnostics)
	}
	var got wardryxPolicyResourceModel
	decodeState(t, resp.State, &got)
	if got.Name.ValueString() != "agent://x/*" {
		t.Errorf("name = %q, want the server's default agent://x/*", got.Name.ValueString())
	}
	if got.DenyTool.IsNull() || len(got.DenyTool.Elements()) != 0 {
		t.Errorf("deny_tool = %v, want an empty list (never null)", got.DenyTool)
	}
	if strings.Contains(fb.requests[0].Body, "deny_tool") {
		t.Errorf("an empty deny_tool reached the wire: %s", fb.requests[0].Body)
	}
}

// TestWardryxPolicyApplyFailureLeavesNoState: a refused PUT is an error
// diagnostic with the server's message, and no state is written.
func TestWardryxPolicyApplyFailureLeavesNoState(t *testing.T) {
	fb := newFakeBackend(t, func(r *http.Request) (int, string) {
		return http.StatusForbidden, `{"error":"admin role required"}`
	})
	r := &wardryxPolicyResource{client: newTestWardryxClient(fb.URL)}
	s := resourceSchema(t, r)
	planned := fullPolicyModel(t)
	planned.UpdatedAt = types.StringUnknown()

	resp := resource.CreateResponse{State: nullState(s)}
	r.Create(context.Background(), resource.CreateRequest{Plan: planOf(t, s, &planned)}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("Create against a 403 reported no error")
	}
	if got := resp.Diagnostics[0].Summary(); got != "Error setting taipan_wardryx_policy" {
		t.Errorf("summary = %q", got)
	}
	if !strings.Contains(resp.Diagnostics[0].Detail(), "admin role required") {
		t.Errorf("detail = %q, want the server's message", resp.Diagnostics[0].Detail())
	}
	if !resp.State.Raw.IsNull() {
		t.Error("state was written after a failed PUT")
	}
}

// TestWardryxPolicyReadFollowsTheServer: present (state takes the server's
// record, including a change made out of band), 404 (removed from state so
// Terraform plans a recreate), any other failure (an error, state kept).
func TestWardryxPolicyReadFollowsTheServer(t *testing.T) {
	prior := fullPolicyModel(t)
	prior.MaxSteps = types.Int64Value(3)
	prior.UpdatedAt = types.StringValue("old")

	cases := []struct {
		name     string
		status   int
		body     string
		wantErr  bool
		wantGone bool
	}{
		{"present", http.StatusOK, policyServerReply, false, false},
		{"deleted out of band", http.StatusNotFound, `{"error":"not found"}`, false, true},
		{"server error", http.StatusInternalServerError, `{"error":"db down"}`, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fb := newFakeBackend(t, func(r *http.Request) (int, string) { return tc.status, tc.body })
			r := &wardryxPolicyResource{client: newTestWardryxClient(fb.URL)}
			s := resourceSchema(t, r)
			st := stateOf(t, s, &prior)

			resp := resource.ReadResponse{State: st}
			r.Read(context.Background(), resource.ReadRequest{State: st}, &resp)

			if fb.requests[0].Method != http.MethodGet || fb.requests[0].Path != "/v1/policies/acct-policy" {
				t.Errorf("request = %s %s", fb.requests[0].Method, fb.requests[0].Path)
			}
			if got := resp.Diagnostics.HasError(); got != tc.wantErr {
				t.Fatalf("error = %v, want %v (%v)", got, tc.wantErr, resp.Diagnostics)
			}
			if got := resp.State.Raw.IsNull(); got != tc.wantGone {
				t.Fatalf("removed from state = %v, want %v", got, tc.wantGone)
			}
			if tc.wantErr {
				if got := resp.Diagnostics[0].Summary(); got != "Error reading taipan_wardryx_policy" {
					t.Errorf("summary = %q", got)
				}
				return
			}
			if tc.wantGone {
				return
			}
			var got wardryxPolicyResourceModel
			decodeState(t, resp.State, &got)
			if got.MaxSteps.ValueInt64() != 10 || got.UpdatedAt.ValueString() != "2026-11-26T10:00:00Z" {
				t.Errorf("state = max_steps %d, updated_at %q; want the server's 10 and timestamp",
					got.MaxSteps.ValueInt64(), got.UpdatedAt.ValueString())
			}
		})
	}
}

// TestWardryxPolicyDeleteCallsTheRealDelete: unlike the budgets, Wardryx has
// a DELETE route and Delete must call it. A 404 means the end state already
// holds and is not an error; any other failure is.
func TestWardryxPolicyDeleteCallsTheRealDelete(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		wantErr bool
	}{
		{"deleted", http.StatusNoContent, false},
		{"already gone", http.StatusNotFound, false},
		{"refused", http.StatusForbidden, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fb := newFakeBackend(t, func(r *http.Request) (int, string) { return tc.status, `{"error":"x"}` })
			r := &wardryxPolicyResource{client: newTestWardryxClient(fb.URL)}
			s := resourceSchema(t, r)
			prior := fullPolicyModel(t)
			prior.UpdatedAt = types.StringValue("t")
			st := stateOf(t, s, &prior)

			resp := resource.DeleteResponse{State: st}
			r.Delete(context.Background(), resource.DeleteRequest{State: st}, &resp)

			if len(fb.requests) != 1 || fb.requests[0].Method != http.MethodDelete || fb.requests[0].Path != "/v1/policies/acct-policy" {
				t.Fatalf("requests = %+v, want one DELETE /v1/policies/acct-policy", fb.requests)
			}
			if got := resp.Diagnostics.HasError(); got != tc.wantErr {
				t.Fatalf("error = %v, want %v (%v)", got, tc.wantErr, resp.Diagnostics)
			}
			if tc.wantErr && resp.Diagnostics[0].Summary() != "Error deleting taipan_wardryx_policy" {
				t.Errorf("summary = %q", resp.Diagnostics[0].Summary())
			}
		})
	}
}

// TestWardryxPolicyImportSetsTheID: the import id is the policy id Read
// then fetches.
func TestWardryxPolicyImportSetsTheID(t *testing.T) {
	r := &wardryxPolicyResource{}
	s := resourceSchema(t, r)
	resp := resource.ImportStateResponse{State: nullState(s)}
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: "ops-guard"}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("ImportState: %v", resp.Diagnostics)
	}
	var got types.String
	resp.State.GetAttribute(context.Background(), path.Root("id"), &got)
	if got.ValueString() != "ops-guard" {
		t.Errorf("id = %q, want ops-guard", got.ValueString())
	}
}

// TestWardryxPolicyRejectsUndecodablePlanAndState: a plan or state of the
// wrong shape stops at the decode with an error and never reaches Wardryx.
func TestWardryxPolicyRejectsUndecodablePlanAndState(t *testing.T) {
	fb := newFakeBackend(t, refuseEveryCall(t))
	r := &wardryxPolicyResource{client: newTestWardryxClient(fb.URL)}
	own := resourceSchema(t, r)
	otherSchema := resourceSchema(t, &budgetResource{})
	other := &budgetResourceModel{RunID: types.StringValue("x"), LimitUSD: types.Float64Value(1)}
	wrongPlan := planOf(t, otherSchema, other)
	wrongState := stateOf(t, otherSchema, other)
	ctx := context.Background()

	c := resource.CreateResponse{State: nullState(own)}
	r.Create(ctx, resource.CreateRequest{Plan: wrongPlan}, &c)
	rd := resource.ReadResponse{State: wrongState}
	r.Read(ctx, resource.ReadRequest{State: wrongState}, &rd)
	d := resource.DeleteResponse{State: wrongState}
	r.Delete(ctx, resource.DeleteRequest{State: wrongState}, &d)

	for name, failed := range map[string]bool{
		"Create": c.Diagnostics.HasError(), "Read": rd.Diagnostics.HasError(), "Delete": d.Diagnostics.HasError(),
	} {
		if !failed {
			t.Errorf("%s decoded a value of the wrong shape without error", name)
		}
	}
	if len(fb.requests) != 0 {
		t.Errorf("Wardryx was called %d times on undecodable input", len(fb.requests))
	}
}
