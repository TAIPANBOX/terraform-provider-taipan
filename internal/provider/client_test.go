package provider

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestClient points a CloudClient at an httptest server so these tests
// never need a live TokenFuse Cloud.
func newTestClient(url string) *CloudClient {
	return &CloudClient{
		BaseURL:    url,
		APIKey:     "testorg:admin:key",
		HTTPClient: http.DefaultClient,
	}
}

// TestSetBudget_RequestShape asserts SetBudget POSTs to exactly
// /v1/runs/{run}/budget with a {"budget_usd": <dollars>} body and a Bearer
// auth header, matching BudgetBody and the set_budget handler in
// tokenfuse's crates/cloud/src/http.rs byte-for-byte: the wire field is
// budget_usd (dollars, a float), never budget_micros -- the server derives
// microdollars itself.
func TestSetBudget_RequestShape(t *testing.T) {
	var gotMethod, gotPath, gotAuth, gotContentType string
	var gotBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		gotBody = body

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"run":"run-123","budget_micros":12500000}`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	result, err := client.SetBudget(t.Context(), "run-123", 12.5)
	if err != nil {
		t.Fatalf("SetBudget: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/v1/runs/run-123/budget" {
		t.Errorf("path = %q, want /v1/runs/run-123/budget", gotPath)
	}
	if gotAuth != "Bearer testorg:admin:key" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer testorg:admin:key")
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotContentType)
	}

	var reqBody map[string]interface{}
	if err := json.Unmarshal(gotBody, &reqBody); err != nil {
		t.Fatalf("request body not valid json: %v (body=%s)", err, gotBody)
	}
	if len(reqBody) != 1 {
		t.Errorf("request body has %d fields, want exactly 1 (budget_usd): %v", len(reqBody), reqBody)
	}
	if v, ok := reqBody["budget_usd"]; !ok || v != 12.5 {
		t.Errorf("request body budget_usd = %v (present=%v), want 12.5", v, ok)
	}

	if result.Run != "run-123" {
		t.Errorf("Run = %q, want run-123", result.Run)
	}
	if result.BudgetMicros != 12500000 {
		t.Errorf("BudgetMicros = %d, want 12500000", result.BudgetMicros)
	}
}

// TestSetBudget_ErrorResponse asserts a non-2xx response (e.g. the 403 the
// real authorize_mutation returns for a non-admin key) surfaces as an
// *APIError carrying the exact response body, not a swallowed generic error.
func TestSetBudget_ErrorResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"admin role required"}`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	_, err := client.SetBudget(t.Context(), "run-123", 12.5)
	if err == nil {
		t.Fatal("SetBudget: expected an error, got nil")
	}

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v (%T), want *APIError", err, err)
	}
	if apiErr.StatusCode != http.StatusForbidden {
		t.Errorf("StatusCode = %d, want 403", apiErr.StatusCode)
	}
	if apiErr.Body != `{"error":"admin role required"}` {
		t.Errorf("Body = %q, want the raw error response body", apiErr.Body)
	}
}

// TestListBudgets_ReadShape asserts ListBudgets GETs /v1/budgets and decodes
// the flat run -> budget_micros JSON object the real budgets handler
// returns (Store::budgets is a HashMap<String, i64>, serialized directly,
// not wrapped in an array or an envelope field).
func TestListBudgets_ReadShape(t *testing.T) {
	var gotMethod, gotPath, gotAuth string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"run-1":5000000,"run-2":2000000}`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	budgets, err := client.ListBudgets(t.Context())
	if err != nil {
		t.Fatalf("ListBudgets: %v", err)
	}

	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	if gotPath != "/v1/budgets" {
		t.Errorf("path = %q, want /v1/budgets", gotPath)
	}
	if gotAuth != "Bearer testorg:admin:key" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer testorg:admin:key")
	}

	want := map[string]int64{"run-1": 5000000, "run-2": 2000000}
	if len(budgets) != len(want) {
		t.Fatalf("ListBudgets = %v, want %v", budgets, want)
	}
	for k, v := range want {
		if budgets[k] != v {
			t.Errorf("ListBudgets[%q] = %d, want %d", k, budgets[k], v)
		}
	}
}

// TestListBudgets_RunAbsent covers the taipan_budget Read contract: a run
// missing from the /v1/budgets response (never set, or cleared) must be
// distinguishable from a run present with a zero budget.
func TestListBudgets_RunAbsent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"run-1":5000000}`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	budgets, err := client.ListBudgets(t.Context())
	if err != nil {
		t.Fatalf("ListBudgets: %v", err)
	}
	if _, ok := budgets["run-missing"]; ok {
		t.Error("run-missing unexpectedly present in budgets map")
	}
}

// TestListBudgets_ErrorResponse mirrors TestSetBudget_ErrorResponse for the
// read path (e.g. an invalid key: 401 from org_for).
func TestListBudgets_ErrorResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid api key"}`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	_, err := client.ListBudgets(t.Context())
	if err == nil {
		t.Fatal("ListBudgets: expected an error, got nil")
	}

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v (%T), want *APIError", err, err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode = %d, want 401", apiErr.StatusCode)
	}
}

func TestMicrosToUSD(t *testing.T) {
	cases := []struct {
		micros int64
		want   float64
	}{
		{0, 0},
		{5_000_000, 5},
		{12_345_678, 12.345678},
		{1, 0.000001},
	}
	for _, c := range cases {
		if got := microsToUSD(c.micros); got != c.want {
			t.Errorf("microsToUSD(%d) = %v, want %v", c.micros, got, c.want)
		}
	}
}
