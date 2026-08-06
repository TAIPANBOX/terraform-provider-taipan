package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// CloudClient is a minimal HTTP client for the TokenFuse Cloud control-plane
// API (crates/cloud in the tokenfuse repo). It carries the operator's own
// bearer key and talks to the budget endpoints the taipan provider needs.
// Nothing here is TAIPANBOX-internal: it is a plain REST client against an
// API the operator already runs, matched byte-for-byte against
// crates/cloud/src/http.rs's set_budget and budgets handlers.
type CloudClient struct {
	// BaseURL has no trailing slash, e.g. https://cloud.tokenfuse.example.
	BaseURL string
	// APIKey is sent verbatim as "Authorization: Bearer <APIKey>". The Cloud
	// API's bearer format is key:org[:role]; set_budget additionally
	// requires the key's role to be admin (authorize_mutation), enforced
	// server-side, not by this client.
	APIKey     string
	HTTPClient *http.Client
}

// setBudgetRequest is the JSON body POST /v1/runs/{run}/budget expects.
// Mirrors BudgetBody in tokenfuse's crates/cloud/src/http.rs exactly: the
// handler deserializes only a budget_usd float and itself derives
// microdollars (budget_usd * 1_000_000) for storage. There is no mode,
// breaker, or other field in the real request body, so this client sends
// only budget_usd.
type setBudgetRequest struct {
	BudgetUSD float64 `json:"budget_usd"`
}

// SetBudgetResult is the JSON body POST /v1/runs/{run}/budget returns on
// success. Mirrors BudgetResponse in http.rs: the run id echoed back and the
// budget the server actually stored, in microdollars.
type SetBudgetResult struct {
	Run          string `json:"run"`
	BudgetMicros int64  `json:"budget_micros"`
}

// APIError is returned when the Cloud API responds with a non-2xx status.
// It carries the raw response body (the API's own ErrorResponse{error} or,
// for a 402, the nested plan_required envelope) so callers can surface the
// server's own message verbatim in a Terraform diagnostic instead of
// swallowing it.
type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("taipan cloud API returned %d: %s", e.StatusCode, e.Body)
}

// SetBudget creates or overwrites the central budget for runID via
// POST /v1/runs/{run}/budget. limitUSD is US dollars; the server itself
// derives and stores microdollars, so this client does no unit conversion
// on the way in.
func (c *CloudClient) SetBudget(ctx context.Context, runID string, limitUSD float64) (*SetBudgetResult, error) {
	reqBody, err := json.Marshal(setBudgetRequest{BudgetUSD: limitUSD})
	if err != nil {
		return nil, fmt.Errorf("encode set_budget request: %w", err)
	}

	url := fmt.Sprintf("%s/v1/runs/%s/budget", c.BaseURL, runID)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("build set_budget request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)

	respBody, status, err := c.do(httpReq)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, &APIError{StatusCode: status, Body: string(respBody)}
	}

	var result SetBudgetResult
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("decode set_budget response: %w", err)
	}
	return &result, nil
}

// SetUnitBudgetResult is the JSON body POST /v1/units/{id}/budget returns
// on success. Mirrors UnitBudgetResponse in http.rs: the same shape as
// SetBudgetResult with the field named unit instead of run, since this sets
// a business unit's central monthly cap (docs/20-identity-map.md section 4)
// rather than one run's budget -- a distinct governance control, not a
// filter over the same one.
type SetUnitBudgetResult struct {
	Unit         string `json:"unit"`
	BudgetMicros int64  `json:"budget_micros"`
}

// SetUnitBudget creates or overwrites a business unit's central monthly
// budget via POST /v1/units/{id}/budget. limitUSD is US dollars; the server
// derives and stores microdollars itself (parsed.budget_usd * 1e6 in
// set_unit_budget), the same conversion set_budget does for a run, so this
// client sends the wire-identical request body (setBudgetRequest, reused
// verbatim) and does no unit conversion on the way in. Requires the
// caller's key to carry the admin role, enforced server-side by
// authorize_mutation, not by this client.
func (c *CloudClient) SetUnitBudget(ctx context.Context, unitID string, limitUSD float64) (*SetUnitBudgetResult, error) {
	reqBody, err := json.Marshal(setBudgetRequest{BudgetUSD: limitUSD})
	if err != nil {
		return nil, fmt.Errorf("encode set_unit_budget request: %w", err)
	}

	url := fmt.Sprintf("%s/v1/units/%s/budget", c.BaseURL, unitID)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("build set_unit_budget request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)

	respBody, status, err := c.do(httpReq)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, &APIError{StatusCode: status, Body: string(respBody)}
	}

	var result SetUnitBudgetResult
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("decode set_unit_budget response: %w", err)
	}
	return &result, nil
}

// ListUnitBudgets reads every business unit -> budget-microdollars override
// visible to the caller's org via GET /v1/unit-budgets. A deliberately
// separate endpoint from GET /v1/budgets, not a filtered view of it
// (docs/20-identity-map.md section 4: the run-budget payload is a flat
// run_id -> i64 map old gateways parse verbatim and cannot grow a nested
// key without breaking them), so this is its own request against its own
// path, mirroring ListBudgets' shape one-for-one (map[string]int64, not an
// array or envelope).
func (c *CloudClient) ListUnitBudgets(ctx context.Context) (map[string]int64, error) {
	url := fmt.Sprintf("%s/v1/unit-budgets", c.BaseURL)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build unit-budgets request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)

	respBody, status, err := c.do(httpReq)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, &APIError{StatusCode: status, Body: string(respBody)}
	}

	var budgets map[string]int64
	if err := json.Unmarshal(respBody, &budgets); err != nil {
		return nil, fmt.Errorf("decode unit-budgets response: %w", err)
	}
	return budgets, nil
}

// ListBudgets reads every run -> budget-microdollars override visible to the
// caller's org via GET /v1/budgets. The Cloud API returns a flat JSON object
// (HashMap<String, i64> server-side, one entry per run with a central
// budget), not an array, so the Go shape is map[string]int64.
func (c *CloudClient) ListBudgets(ctx context.Context) (map[string]int64, error) {
	url := fmt.Sprintf("%s/v1/budgets", c.BaseURL)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build budgets request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)

	respBody, status, err := c.do(httpReq)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, &APIError{StatusCode: status, Body: string(respBody)}
	}

	var budgets map[string]int64
	if err := json.Unmarshal(respBody, &budgets); err != nil {
		return nil, fmt.Errorf("decode budgets response: %w", err)
	}
	return budgets, nil
}

// do executes an HTTP request and returns the fully-drained response body
// and status code. Centralized so SetBudget and ListBudgets share the same
// transport error wrapping and body-close handling.
func (c *CloudClient) do(httpReq *http.Request) ([]byte, int, error) {
	httpResp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, 0, fmt.Errorf("call taipan cloud API: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("read taipan cloud API response: %w", err)
	}
	return respBody, httpResp.StatusCode, nil
}
