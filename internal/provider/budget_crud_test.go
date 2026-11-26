package provider

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// budgetLike lets the run budget and the unit budget share one table of CRUD
// tests: the two resources have the same wire shape and the same lifecycle,
// and differ only in the key attribute, the routes, and the response field
// that echoes the key back. Each case below runs against both.
type budgetLike struct {
	name      string
	newRes    func(*CloudClient) resource.Resource
	keyAttr   string
	setPath   func(key string) string
	listPath  string
	setReply  func(key string, micros string) string
	model     func(key string, limit float64) any
	decode    func(t *testing.T, st tfsdk.State) (string, float64)
	errPrefix string
}

var budgetResources = []budgetLike{
	{
		name:     "taipan_budget",
		newRes:   func(c *CloudClient) resource.Resource { return &budgetResource{client: c} },
		keyAttr:  "run_id",
		setPath:  func(k string) string { return "/v1/runs/" + k + "/budget" },
		listPath: "/v1/budgets",
		setReply: func(k, m string) string { return `{"run":"` + k + `","budget_micros":` + m + `}` },
		model: func(k string, l float64) any {
			return &budgetResourceModel{RunID: types.StringValue(k), LimitUSD: types.Float64Value(l)}
		},
		decode: func(t *testing.T, st tfsdk.State) (string, float64) {
			var m budgetResourceModel
			decodeState(t, st, &m)
			return m.RunID.ValueString(), m.LimitUSD.ValueFloat64()
		},
		errPrefix: "taipan_budget",
	},
	{
		name:     "taipan_unit_budget",
		newRes:   func(c *CloudClient) resource.Resource { return &unitBudgetResource{client: c} },
		keyAttr:  "unit_id",
		setPath:  func(k string) string { return "/v1/units/" + k + "/budget" },
		listPath: "/v1/unit-budgets",
		setReply: func(k, m string) string { return `{"unit":"` + k + `","budget_micros":` + m + `}` },
		model: func(k string, l float64) any {
			return &unitBudgetResourceModel{UnitID: types.StringValue(k), LimitUSD: types.Float64Value(l)}
		},
		decode: func(t *testing.T, st tfsdk.State) (string, float64) {
			var m unitBudgetResourceModel
			decodeState(t, st, &m)
			return m.UnitID.ValueString(), m.LimitUSD.ValueFloat64()
		},
		errPrefix: "taipan_unit_budget",
	},
}

// applyFn is Create or Update, which both resources route through the same
// apply helper; the tests run both so neither entry point can drift.
type applyFn func(r resource.Resource, plan tfsdk.Plan, st tfsdk.State) (tfsdk.State, diag.Diagnostics)

var budgetApplies = map[string]applyFn{
	"Create": func(r resource.Resource, plan tfsdk.Plan, st tfsdk.State) (tfsdk.State, diag.Diagnostics) {
		resp := resource.CreateResponse{State: st}
		r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &resp)
		return resp.State, resp.Diagnostics
	},
	"Update": func(r resource.Resource, plan tfsdk.Plan, st tfsdk.State) (tfsdk.State, diag.Diagnostics) {
		resp := resource.UpdateResponse{State: st}
		r.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: st}, &resp)
		return resp.State, resp.Diagnostics
	},
}

// TestBudgetApplyStoresWhatTheServerStoredNotWhatWasPlanned: the Cloud API
// truncates budget_usd * 1e6 to whole microdollars, so a plan of
// 12.3456789 is stored as 12345678 micros. State must carry the server's
// 12.345678, converted back from micros, and the server's echoed key: an
// echo of the plan would drift from the real Cloud value on the next read.
func TestBudgetApplyStoresWhatTheServerStoredNotWhatWasPlanned(t *testing.T) {
	for _, b := range budgetResources {
		for opName, apply := range budgetApplies {
			t.Run(b.name+"/"+opName, func(t *testing.T) {
				fb := newFakeBackend(t, func(r *http.Request) (int, string) {
					return http.StatusOK, b.setReply("treasury-7", "12345678")
				})
				r := b.newRes(newTestClient(fb.URL))
				s := resourceSchema(t, r)

				st, diags := apply(r, planOf(t, s, b.model("treasury-7", 12.3456789)), nullState(s))
				if diags.HasError() {
					t.Fatalf("%s: %v", opName, diags)
				}

				key, limit := b.decode(t, st)
				if key != "treasury-7" {
					t.Errorf("%s = %q, want treasury-7", b.keyAttr, key)
				}
				if limit != 12.345678 {
					t.Errorf("limit_usd = %v, want 12.345678 (server's truncated micros, not the plan's 12.3456789)", limit)
				}

				if len(fb.requests) != 1 {
					t.Fatalf("backend saw %d requests, want 1", len(fb.requests))
				}
				got := fb.requests[0]
				if got.Method != http.MethodPost || got.Path != b.setPath("treasury-7") {
					t.Errorf("request = %s %s, want POST %s", got.Method, got.Path, b.setPath("treasury-7"))
				}
				if got.Body != `{"budget_usd":12.3456789}` {
					t.Errorf("request body = %s, want the planned dollars as budget_usd", got.Body)
				}
			})
		}
	}
}

// TestBudgetApplyEchoesTheKeyTheServerReturned: if the server answers with a
// different key than the one sent, state follows the server (invariant 2,
// state reflects the backend), rather than keeping the planned one.
func TestBudgetApplyEchoesTheKeyTheServerReturned(t *testing.T) {
	for _, b := range budgetResources {
		t.Run(b.name, func(t *testing.T) {
			fb := newFakeBackend(t, func(r *http.Request) (int, string) {
				return http.StatusOK, b.setReply("canonical-id", "1000000")
			})
			r := b.newRes(newTestClient(fb.URL))
			s := resourceSchema(t, r)

			st, diags := budgetApplies["Create"](r, planOf(t, s, b.model("alias-id", 1)), nullState(s))
			if diags.HasError() {
				t.Fatalf("Create: %v", diags)
			}
			if key, _ := b.decode(t, st); key != "canonical-id" {
				t.Errorf("%s = %q, want the server's canonical-id", b.keyAttr, key)
			}
		})
	}
}

// TestBudgetApplyFailureLeavesNoStateAndSaysWhy: a refused write (403 for a
// non-admin key is the documented case) is an error diagnostic naming the
// resource and carrying the server's own message, and nothing reaches state.
func TestBudgetApplyFailureLeavesNoStateAndSaysWhy(t *testing.T) {
	for _, b := range budgetResources {
		for opName, apply := range budgetApplies {
			t.Run(b.name+"/"+opName, func(t *testing.T) {
				fb := newFakeBackend(t, func(r *http.Request) (int, string) {
					return http.StatusForbidden, `{"error":"admin role required"}`
				})
				r := b.newRes(newTestClient(fb.URL))
				s := resourceSchema(t, r)

				st, diags := apply(r, planOf(t, s, b.model("run-1", 5)), nullState(s))
				if !diags.HasError() {
					t.Fatalf("%s against a 403 reported no error", opName)
				}
				summary, detail := diags[0].Summary(), diags[0].Detail()
				if summary != "Error setting "+b.errPrefix {
					t.Errorf("summary = %q, want %q", summary, "Error setting "+b.errPrefix)
				}
				if !strings.Contains(detail, "403") || !strings.Contains(detail, "admin role required") {
					t.Errorf("detail = %q, want the status and the server's message", detail)
				}
				if !st.Raw.IsNull() {
					t.Errorf("state was written after a failed %s: %v", opName, st.Raw)
				}
			})
		}
	}
}

// TestBudgetReadFollowsTheServer covers the three outcomes of Read: the
// budget is there (state takes the server's value, even when it moved out
// of band), the budget is gone (state is removed so Terraform plans a
// recreate), and the read fails (an error, state untouched).
func TestBudgetReadFollowsTheServer(t *testing.T) {
	for _, b := range budgetResources {
		t.Run(b.name+"/changed out of band", func(t *testing.T) {
			fb := newFakeBackend(t, func(r *http.Request) (int, string) {
				return http.StatusOK, `{"other":1,"run-1":7500000}`
			})
			r := b.newRes(newTestClient(fb.URL))
			s := resourceSchema(t, r)
			prior := stateOf(t, s, b.model("run-1", 5))

			resp := resource.ReadResponse{State: prior}
			r.Read(context.Background(), resource.ReadRequest{State: prior}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("Read: %v", resp.Diagnostics)
			}
			key, limit := b.decode(t, resp.State)
			if key != "run-1" || limit != 7.5 {
				t.Errorf("state = (%q, %v), want (run-1, 7.5) from the server", key, limit)
			}
			if fb.requests[0].Method != http.MethodGet || fb.requests[0].Path != b.listPath {
				t.Errorf("request = %s %s, want GET %s", fb.requests[0].Method, fb.requests[0].Path, b.listPath)
			}
		})

		t.Run(b.name+"/gone server-side", func(t *testing.T) {
			fb := newFakeBackend(t, func(r *http.Request) (int, string) {
				return http.StatusOK, `{"someone-else":1000000}`
			})
			r := b.newRes(newTestClient(fb.URL))
			s := resourceSchema(t, r)
			prior := stateOf(t, s, b.model("run-1", 5))

			resp := resource.ReadResponse{State: prior}
			r.Read(context.Background(), resource.ReadRequest{State: prior}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("Read: %v", resp.Diagnostics)
			}
			if !resp.State.Raw.IsNull() {
				t.Errorf("a budget missing server-side stayed in state: %v", resp.State.Raw)
			}
		})

		t.Run(b.name+"/read fails", func(t *testing.T) {
			fb := newFakeBackend(t, func(r *http.Request) (int, string) {
				return http.StatusInternalServerError, `{"error":"db down"}`
			})
			r := b.newRes(newTestClient(fb.URL))
			s := resourceSchema(t, r)
			prior := stateOf(t, s, b.model("run-1", 5))

			resp := resource.ReadResponse{State: prior}
			r.Read(context.Background(), resource.ReadRequest{State: prior}, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatal("Read against a 500 reported no error")
			}
			if got := resp.Diagnostics[0].Summary(); got != "Error reading "+b.errPrefix {
				t.Errorf("summary = %q", got)
			}
			if resp.State.Raw.IsNull() {
				t.Error("a failed read removed the resource from state; it must leave it for the next refresh")
			}
		})
	}
}

// TestBudgetDeleteIsStateOnlyAndCallsNothing: TokenFuse Cloud has no
// budget-delete route, and inventing one would 404 everywhere, so Delete
// must make no backend call at all and must not fail.
func TestBudgetDeleteIsStateOnlyAndCallsNothing(t *testing.T) {
	for _, b := range budgetResources {
		t.Run(b.name, func(t *testing.T) {
			fb := newFakeBackend(t, refuseEveryCall(t))
			r := b.newRes(newTestClient(fb.URL))
			s := resourceSchema(t, r)
			prior := stateOf(t, s, b.model("run-1", 5))

			resp := resource.DeleteResponse{State: prior}
			r.Delete(context.Background(), resource.DeleteRequest{State: prior}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("Delete: %v", resp.Diagnostics)
			}
			if len(fb.requests) != 0 {
				t.Errorf("Delete called the backend %d times, want 0", len(fb.requests))
			}
		})
	}
}

// TestBudgetImportSetsTheKeyAttribute: `terraform import` takes the run or
// unit id, and it must land on the key attribute Read then looks up.
func TestBudgetImportSetsTheKeyAttribute(t *testing.T) {
	for _, b := range budgetResources {
		t.Run(b.name, func(t *testing.T) {
			r := b.newRes(nil)
			s := resourceSchema(t, r)
			resp := resource.ImportStateResponse{State: nullState(s)}
			r.(resource.ResourceWithImportState).ImportState(context.Background(), resource.ImportStateRequest{ID: "imported-42"}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("ImportState: %v", resp.Diagnostics)
			}
			var got types.String
			if diags := resp.State.GetAttribute(context.Background(), path.Root(b.keyAttr), &got); diags.HasError() {
				t.Fatalf("read %s: %v", b.keyAttr, diags)
			}
			if got.ValueString() != "imported-42" {
				t.Errorf("%s = %q, want imported-42", b.keyAttr, got.ValueString())
			}
		})
	}
}

// TestBudgetApplyRejectsAPlanItCannotDecode: a plan whose schema does not
// match the model must stop at the decode, before any backend call.
func TestBudgetApplyRejectsAPlanItCannotDecode(t *testing.T) {
	for i, b := range budgetResources {
		t.Run(b.name, func(t *testing.T) {
			fb := newFakeBackend(t, refuseEveryCall(t))
			r := b.newRes(newTestClient(fb.URL))
			other := budgetResources[1-i]
			otherSchema := resourceSchema(t, other.newRes(nil))
			wrongPlan := planOf(t, otherSchema, other.model("x", 1))

			_, diags := budgetApplies["Create"](r, wrongPlan, nullState(resourceSchema(t, r)))
			if !diags.HasError() {
				t.Fatal("a plan of the wrong shape decoded without error")
			}
			if len(fb.requests) != 0 {
				t.Errorf("backend called %d times on an undecodable plan", len(fb.requests))
			}

			for _, call := range []func(st tfsdk.State) diag.Diagnostics{
				func(st tfsdk.State) diag.Diagnostics {
					resp := resource.ReadResponse{State: st}
					r.Read(context.Background(), resource.ReadRequest{State: st}, &resp)
					return resp.Diagnostics
				},
				func(st tfsdk.State) diag.Diagnostics {
					resp := resource.DeleteResponse{State: st}
					r.Delete(context.Background(), resource.DeleteRequest{State: st}, &resp)
					return resp.Diagnostics
				},
			} {
				wrongState := stateOf(t, otherSchema, other.model("x", 1))
				if d := call(wrongState); !d.HasError() {
					t.Error("a state of the wrong shape decoded without error")
				}
			}
			if len(fb.requests) != 0 {
				t.Errorf("backend called %d times on an undecodable state", len(fb.requests))
			}
		})
	}
}
