// Package x402 contains the HTTP client for x402 v2 facilitators.
//
// This is a hand-rolled Go implementation of the small subset of the x402
// facilitator protocol we actually need:
//
//   POST /verify  → "is this signed payment payload valid?"
//   POST /settle  → "execute the payment on-chain"
//   GET  /supported → "what schemes/networks do you support?"
//
// All three are POST/GET to a base URL with `Authorization: Bearer <api_key>`.
// The default base URL is the OpenZeppelin Built-on-Stellar hosted
// facilitator at https://channels.openzeppelin.com/x402, but the Client
// works with any spec-compliant x402 facilitator.
package x402

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ----- Wire types (x402 v2) -----

// ResourceInfo is the URL/metadata of the protected resource. It lives at the
// top level of the PaymentRequired envelope (not inside each accepts entry).
type ResourceInfo struct {
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// PaymentRequirements is one entry in the accepts[] array of a PaymentRequired
// response — describes ONE way the resource can be paid for. The asset field
// on Stellar is the Soroban contract address (C-prefix), not the classic
// asset issuer (G-prefix).
type PaymentRequirements struct {
	Scheme            string                 `json:"scheme"`
	Network           string                 `json:"network"`
	Asset             string                 `json:"asset"`
	Amount            string                 `json:"amount"`
	PayTo             string                 `json:"payTo"`
	MaxTimeoutSeconds int                    `json:"maxTimeoutSeconds"`
	Extra             map[string]interface{} `json:"extra"`
}

// PaymentRequired is the body of a 402 response.
type PaymentRequired struct {
	X402Version int                    `json:"x402Version"`
	Error       string                 `json:"error,omitempty"`
	Resource    ResourceInfo           `json:"resource"`
	Accepts     []PaymentRequirements  `json:"accepts"`
	Extensions  map[string]interface{} `json:"extensions,omitempty"`
}

// PaymentPayload is what a client sends in the X-PAYMENT header (after
// base64 decoding). The payload field is network-specific: for Stellar
// it carries `{"transaction": "<base64 xdr>"}`.
type PaymentPayload struct {
	X402Version int                 `json:"x402Version"`
	Resource    *ResourceInfo       `json:"resource,omitempty"`
	Accepted    PaymentRequirements `json:"accepted"`
	Payload     json.RawMessage     `json:"payload"`
}

// VerifyRequest is the body POSTed to /verify and /settle.
type VerifyRequest struct {
	X402Version         int                 `json:"x402Version"`
	PaymentPayload      PaymentPayload      `json:"paymentPayload"`
	PaymentRequirements PaymentRequirements `json:"paymentRequirements"`
}

// VerifyResponse is the body returned by /verify.
type VerifyResponse struct {
	IsValid        bool                   `json:"isValid"`
	InvalidReason  string                 `json:"invalidReason,omitempty"`
	InvalidMessage string                 `json:"invalidMessage,omitempty"`
	Payer          string                 `json:"payer,omitempty"`
	Extensions     map[string]interface{} `json:"extensions,omitempty"`
}

// SettleResponse is the body returned by /settle.
type SettleResponse struct {
	Success      bool                   `json:"success"`
	ErrorReason  string                 `json:"errorReason,omitempty"`
	ErrorMessage string                 `json:"errorMessage,omitempty"`
	Payer        string                 `json:"payer,omitempty"`
	Transaction  string                 `json:"transaction"`
	Network      string                 `json:"network"`
	Amount       string                 `json:"amount,omitempty"`
	Extensions   map[string]interface{} `json:"extensions,omitempty"`
}

// SupportedResponse is the body returned by /supported.
type SupportedResponse struct {
	Kinds      []SupportedKind     `json:"kinds"`
	Extensions []string            `json:"extensions"`
	Signers    map[string][]string `json:"signers"`
}

type SupportedKind struct {
	X402Version int                    `json:"x402Version"`
	Scheme      string                 `json:"scheme"`
	Network     string                 `json:"network"`
	Extra       map[string]interface{} `json:"extra,omitempty"`
}

// ----- Client -----

// Client is an HTTP client for an x402 v2 facilitator.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// New constructs a Client. The baseURL should NOT include a trailing slash
// or any of the /verify, /settle, /supported paths — those are appended.
func New(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Verify asks the facilitator whether the payment payload is valid for the
// given requirements. Network errors and non-2xx HTTP responses are wrapped
// as errors; an isValid=false reply is returned as a successful call with
// the response struct populated (callers MUST check resp.IsValid).
func (c *Client) Verify(ctx context.Context, req VerifyRequest) (*VerifyResponse, error) {
	var out VerifyResponse
	if err := c.post(ctx, "/verify", req, &out); err != nil {
		return nil, fmt.Errorf("facilitator verify: %w", err)
	}
	return &out, nil
}

// Settle asks the facilitator to execute the verified payment on-chain.
// Same error semantics as Verify.
func (c *Client) Settle(ctx context.Context, req VerifyRequest) (*SettleResponse, error) {
	var out SettleResponse
	if err := c.post(ctx, "/settle", req, &out); err != nil {
		return nil, fmt.Errorf("facilitator settle: %w", err)
	}
	return &out, nil
}

// Supported lists the (scheme, network) combinations the facilitator
// understands. Useful at startup as a health check.
func (c *Client) Supported(ctx context.Context) (*SupportedResponse, error) {
	httpReq, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/supported", nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Accept", "application/json")

	httpResp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http call: %w", err)
	}
	defer httpResp.Body.Close()

	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if httpResp.StatusCode >= 400 {
		return nil, fmt.Errorf("facilitator returned %d: %s", httpResp.StatusCode, string(body))
	}
	var out SupportedResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode supported: %w (body=%s)", err, string(body))
	}
	return &out, nil
}

// post is the shared POST helper for /verify and /settle.
func (c *Client) post(ctx context.Context, path string, body interface{}, out interface{}) error {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+path, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	httpResp, err := c.http.Do(httpReq)
	if err != nil {
		return fmt.Errorf("http call: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if httpResp.StatusCode >= 400 {
		return fmt.Errorf("facilitator returned %d: %s", httpResp.StatusCode, string(respBody))
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("decode response: %w (body=%s)", err, string(respBody))
	}
	return nil
}
