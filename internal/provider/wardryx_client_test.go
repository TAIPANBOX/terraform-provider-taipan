package provider

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestWardryxClient points a WardryxClient at an httptest server so
// these tests never need a live Wardryx deployment.
func newTestWardryxClient(url string) *WardryxClient {
	return &WardryxClient{
		BaseURL:    url,
		APIKey:     "testorg:admin:key",
		HTTPClient: http.DefaultClient,
	}
}

// TestPutPolicy_RequestShape asserts PutPolicy PUTs to exactly
// /v1/policies/{id} with the policy document as the body and a Bearer auth
// header, matching wardryx's handlePutPolicy byte-for-byte: the id is a URL
// path segment, never a body field.
func TestPutPolicy_RequestShape(t *testing.T) {
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
		_, _ = w.Write([]byte(`{"id":"ops-guard","target":"agent://acme.example/ops/*","deny_tool":["shell_exec"],"updated_at":"2026-07-13T16:00:00Z"}`))
	}))
	defer server.Close()

	client := newTestWardryxClient(server.URL)
	doc := WardryxPolicyDocument{Target: "agent://acme.example/ops/*", DenyTool: []string{"shell_exec"}}
	result, err := client.PutPolicy(t.Context(), "ops-guard", doc)
	if err != nil {
		t.Fatalf("PutPolicy: %v", err)
	}

	if gotMethod != http.MethodPut {
		t.Errorf("method = %q, want PUT", gotMethod)
	}
	if gotPath != "/v1/policies/ops-guard" {
		t.Errorf("path = %q, want /v1/policies/ops-guard", gotPath)
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
	if _, ok := reqBody["id"]; ok {
		t.Errorf("request body unexpectedly contains id: %v (id is a URL path segment, not a body field)", reqBody)
	}
	if reqBody["target"] != "agent://acme.example/ops/*" {
		t.Errorf("request body target = %v, want agent://acme.example/ops/*", reqBody["target"])
	}
	denyTool, ok := reqBody["deny_tool"].([]interface{})
	if !ok || len(denyTool) != 1 || denyTool[0] != "shell_exec" {
		t.Errorf("request body deny_tool = %v, want [shell_exec]", reqBody["deny_tool"])
	}

	if result.ID != "ops-guard" {
		t.Errorf("ID = %q, want ops-guard", result.ID)
	}
	if result.Target != "agent://acme.example/ops/*" {
		t.Errorf("Target = %q, want agent://acme.example/ops/*", result.Target)
	}
	if result.UpdatedAt != "2026-07-13T16:00:00Z" {
		t.Errorf("UpdatedAt = %q, want 2026-07-13T16:00:00Z", result.UpdatedAt)
	}
}

// TestPutPolicy_OmitsEmptyOptionalFields asserts the request body omits
// unset optional fields entirely (omitempty), rather than sending zero
// values that could be mistaken for an explicit 0/false/[] by the server.
func TestPutPolicy_OmitsEmptyOptionalFields(t *testing.T) {
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody = body
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"minimal","target":"agent://x/*","updated_at":"2026-07-13T16:00:00Z"}`))
	}))
	defer server.Close()

	client := newTestWardryxClient(server.URL)
	if _, err := client.PutPolicy(t.Context(), "minimal", WardryxPolicyDocument{Target: "agent://x/*"}); err != nil {
		t.Fatalf("PutPolicy: %v", err)
	}

	var reqBody map[string]interface{}
	if err := json.Unmarshal(gotBody, &reqBody); err != nil {
		t.Fatalf("request body not valid json: %v", err)
	}
	for _, field := range []string{"name", "deny_tool", "allow_domains", "require_human_above_usd", "deny_above_usd", "max_steps", "deny_if_unattested"} {
		if _, present := reqBody[field]; present {
			t.Errorf("field %q present in minimal request, want omitted: %s", field, gotBody)
		}
	}
}

// TestPutPolicy_ErrorResponse asserts a non-2xx response (e.g. the 400 real
// wardryx returns for an invalid policy body) surfaces as an *APIError
// carrying the exact response body.
func TestPutPolicy_ErrorResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid policy: policy \"bad\": target is required"}`))
	}))
	defer server.Close()

	client := newTestWardryxClient(server.URL)
	_, err := client.PutPolicy(t.Context(), "bad", WardryxPolicyDocument{})
	if err == nil {
		t.Fatal("PutPolicy: expected an error, got nil")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v (%T), want *APIError", err, err)
	}
	if apiErr.StatusCode != http.StatusBadRequest {
		t.Errorf("StatusCode = %d, want 400", apiErr.StatusCode)
	}
}

// TestGetPolicy_RequestShape asserts GetPolicy GETs /v1/policies/{id}.
func TestGetPolicy_RequestShape(t *testing.T) {
	var gotMethod, gotPath, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"ops-guard","target":"agent://acme.example/ops/*","max_steps":5,"updated_at":"2026-07-13T16:00:00Z"}`))
	}))
	defer server.Close()

	client := newTestWardryxClient(server.URL)
	result, err := client.GetPolicy(t.Context(), "ops-guard")
	if err != nil {
		t.Fatalf("GetPolicy: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	if gotPath != "/v1/policies/ops-guard" {
		t.Errorf("path = %q, want /v1/policies/ops-guard", gotPath)
	}
	if gotAuth != "Bearer testorg:admin:key" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer testorg:admin:key")
	}
	if result.MaxSteps != 5 {
		t.Errorf("MaxSteps = %d, want 5", result.MaxSteps)
	}
}

// TestGetPolicy_NotFound asserts a 404 surfaces as an *APIError with
// StatusCode 404, the exact signal wardryxPolicyResource.Read checks for
// to drop a deleted-out-of-band policy from Terraform state.
func TestGetPolicy_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"policy not found"}`))
	}))
	defer server.Close()

	client := newTestWardryxClient(server.URL)
	_, err := client.GetPolicy(t.Context(), "does-not-exist")
	if err == nil {
		t.Fatal("GetPolicy: expected an error, got nil")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v (%T), want *APIError", err, err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want 404", apiErr.StatusCode)
	}
}

// TestDeletePolicy_RequestShape asserts DeletePolicy DELETEs
// /v1/policies/{id} and treats 2xx as success with no response body to
// decode (wardryx's handleDeletePolicy returns 204 No Content).
func TestDeletePolicy_RequestShape(t *testing.T) {
	var gotMethod, gotPath, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := newTestWardryxClient(server.URL)
	if err := client.DeletePolicy(t.Context(), "ops-guard"); err != nil {
		t.Fatalf("DeletePolicy: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Errorf("method = %q, want DELETE", gotMethod)
	}
	if gotPath != "/v1/policies/ops-guard" {
		t.Errorf("path = %q, want /v1/policies/ops-guard", gotPath)
	}
	if gotAuth != "Bearer testorg:admin:key" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer testorg:admin:key")
	}
}

// TestDeletePolicy_NotFound mirrors TestGetPolicy_NotFound for the delete
// path: wardryxPolicyResource.Delete treats this as already-achieved, not
// an error, but the client itself must still report it accurately.
func TestDeletePolicy_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"policy not found"}`))
	}))
	defer server.Close()

	client := newTestWardryxClient(server.URL)
	err := client.DeletePolicy(t.Context(), "does-not-exist")
	if err == nil {
		t.Fatal("DeletePolicy: expected an error, got nil")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v (%T), want *APIError", err, err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want 404", apiErr.StatusCode)
	}
}

// TestDeletePolicy_ErrorResponse mirrors TestSetBudget_ErrorResponse for
// the delete path (e.g. a non-admin key: 403).
func TestDeletePolicy_ErrorResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"admin role required"}`))
	}))
	defer server.Close()

	client := newTestWardryxClient(server.URL)
	err := client.DeletePolicy(t.Context(), "ops-guard")
	if err == nil {
		t.Fatal("DeletePolicy: expected an error, got nil")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v (%T), want *APIError", err, err)
	}
	if apiErr.StatusCode != http.StatusForbidden {
		t.Errorf("StatusCode = %d, want 403", apiErr.StatusCode)
	}
}
