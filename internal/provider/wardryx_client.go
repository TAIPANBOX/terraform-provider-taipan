package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// WardryxClient is a minimal HTTP client for Wardryx's admin policy-as-code
// API (internal/api in the wardryx repo). It carries the operator's own
// admin-role bearer key and talks to the /v1/policies routes the
// taipan_policy resource needs. Matched byte-for-byte against wardryx's
// internal/api/api.go handlePutPolicy/handleGetPolicy/handleDeletePolicy.
type WardryxClient struct {
	// BaseURL has no trailing slash, e.g. https://wardryx.acme.example.
	BaseURL string
	// APIKey is sent verbatim as "Authorization: Bearer <APIKey>". This is
	// just the key segment of one WARDRYX_KEYS entry (key:org:role) --
	// wardryx's ParseKeys maps the wire token to its Principal by that
	// segment alone (internal/api/auth.go), so the org and role are never
	// part of what goes over the wire. Every /v1/policies route requires
	// the key's role to be admin (Server.requireAdmin), enforced
	// server-side, not by this client.
	APIKey     string
	HTTPClient *http.Client
}

// WardryxPolicyDocument is one policy body, sent as the PUT /v1/policies/{id}
// request and decoded from its (and GET's) response. Mirrors wardryx's
// policy.Policy JSON tags exactly -- see wardryx/internal/policy/policy.go
// -- so a field added there only needs adding here to reach the wire.
type WardryxPolicyDocument struct {
	Name                 string   `json:"name,omitempty"`
	Target               string   `json:"target"`
	DenyTool             []string `json:"deny_tool,omitempty"`
	AllowDomains         []string `json:"allow_domains,omitempty"`
	RequireHumanAboveUSD float64  `json:"require_human_above_usd,omitempty"`
	DenyAboveUSD         float64  `json:"deny_above_usd,omitempty"`
	MaxSteps             int64    `json:"max_steps,omitempty"`
	DenyIfUnattested     bool     `json:"deny_if_unattested,omitempty"`
}

// WardryxPolicyRecord is PUT/GET /v1/policies/{id}'s response shape:
// WardryxPolicyDocument's fields (embedded, so its JSON tags flatten onto
// this one) plus the id and last-write timestamp wardryx's policyDTO adds
// on top. Mirrors wardryx's internal/api.policyDTO.
type WardryxPolicyRecord struct {
	ID string `json:"id"`
	WardryxPolicyDocument
	UpdatedAt string `json:"updated_at,omitempty"`
}

// PutPolicy creates or replaces the policy stored under id via
// PUT /v1/policies/{id}. Upsert semantics: a second PutPolicy under the
// same id replaces the first, matching wardryx's handlePutPolicy.
func (c *WardryxClient) PutPolicy(ctx context.Context, id string, doc WardryxPolicyDocument) (*WardryxPolicyRecord, error) {
	reqBody, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("encode policy document: %w", err)
	}

	url := fmt.Sprintf("%s/v1/policies/%s", c.BaseURL, id)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("build put policy request: %w", err)
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

	var result WardryxPolicyRecord
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("decode put policy response: %w", err)
	}
	return &result, nil
}

// GetPolicy reads the policy stored under id via GET /v1/policies/{id}.
// Returns an *APIError with StatusCode 404 (wrapped, use errors.As) when no
// such policy exists -- callers implementing Terraform's Read semantics
// check for that specifically to drop the resource from state.
func (c *WardryxClient) GetPolicy(ctx context.Context, id string) (*WardryxPolicyRecord, error) {
	url := fmt.Sprintf("%s/v1/policies/%s", c.BaseURL, id)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build get policy request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)

	respBody, status, err := c.do(httpReq)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, &APIError{StatusCode: status, Body: string(respBody)}
	}

	var result WardryxPolicyRecord
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("decode get policy response: %w", err)
	}
	return &result, nil
}

// DeletePolicy removes the policy stored under id via DELETE
// /v1/policies/{id}. Unlike TokenFuse Cloud's budget endpoint (no
// server-side delete, see CloudClient), wardryx's policy API has a real
// DELETE, so this performs one -- destroying a taipan_policy resource
// actually removes the rule from wardryx, not just from Terraform state.
func (c *WardryxClient) DeletePolicy(ctx context.Context, id string) error {
	url := fmt.Sprintf("%s/v1/policies/%s", c.BaseURL, id)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("build delete policy request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)

	respBody, status, err := c.do(httpReq)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return &APIError{StatusCode: status, Body: string(respBody)}
	}
	return nil
}

// do executes an HTTP request and returns the fully-drained response body
// and status code. Centralized so PutPolicy/GetPolicy/DeletePolicy share the
// same transport error wrapping and body-close handling, mirroring
// CloudClient.do.
func (c *WardryxClient) do(httpReq *http.Request) ([]byte, int, error) {
	httpResp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, 0, fmt.Errorf("call wardryx API: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("read wardryx API response: %w", err)
	}
	return respBody, httpResp.StatusCode, nil
}
