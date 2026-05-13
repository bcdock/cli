package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bcdock/cli/internal/exitcode"
)

// errorBodyMaxLen caps the response body excerpt included in error messages
// when the API returns non-JSON content (DeveloperExceptionPage HTML in Dev,
// Stripe.net stack traces, etc.) — enough to identify the cause without
// flooding the terminal.
const errorBodyMaxLen = 500

// formatNonJsonErrorBody returns "HTTP <status>: <truncated body>" when the
// response body has actionable content, or just "HTTP <status>" when the body
// is empty / whitespace-only. Strips HTML angle-tag noise to keep the excerpt
// readable when ASP.NET's developer exception page slips through.
func formatNonJsonErrorBody(status int, body []byte) string {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return fmt.Sprintf("HTTP %d", status)
	}
	// Coarse HTML strip: drop tags and collapse whitespace. Not a parser —
	// just enough to make the developer-exception-page payload skimmable.
	if strings.HasPrefix(text, "<!DOCTYPE") || strings.Contains(text, "<html") {
		var sb strings.Builder
		inTag := false
		for _, r := range text {
			switch {
			case r == '<':
				inTag = true
			case r == '>':
				inTag = false
			case !inTag:
				sb.WriteRune(r)
			}
		}
		text = strings.Join(strings.Fields(sb.String()), " ")
	}
	if len(text) > errorBodyMaxLen {
		text = text[:errorBodyMaxLen] + "… (truncated, " + fmt.Sprintf("%d", len(body)) + " bytes total)"
	}
	return fmt.Sprintf("HTTP %d: %s", status, text)
}

type Client struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

func New(baseURL, token string, timeout time.Duration) *Client {
	return &Client{
		BaseURL: baseURL,
		Token:   token,
		HTTPClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// APIError is returned when the server responds with a non-2xx status.
//
// The Platform API emits the canonical shape `{ error, code, details? }` (see
// docs/BCDOCK_CLI.md → API versioning). Older internal endpoints sometimes use
// `{ message, code }` instead — both shapes deserialize cleanly here, with the
// human-readable text taken from whichever field is populated.
type APIError struct {
	Code    string `json:"code"`
	ErrorText string `json:"error"`
	Message string `json:"message"`
	Status  int    `json:"-"`
	RawBody []byte `json:"-"`
}

// HumanText returns the populated human-readable error text from either field.
func (e *APIError) HumanText() string {
	if e.ErrorText != "" {
		return e.ErrorText
	}
	return e.Message
}

func (e *APIError) Error() string {
	text := e.HumanText()
	if e.Code != "" {
		return fmt.Sprintf("%s: %s", e.Code, text)
	}
	return text
}

// ExitCode maps the API error to the CLI exit code convention.
func (e *APIError) ExitCode() int {
	switch e.Status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return exitcode.AuthFailure
	case http.StatusNotFound:
		return exitcode.NotFound
	case http.StatusTooManyRequests:
		return exitcode.RateLimited
	default:
		return exitcode.GeneralError
	}
}

// Stream opens a GET request and returns the response body for streaming reads (plain text or SSE).
// The caller is responsible for closing the returned ReadCloser.
func (c *Client) Stream(ctx context.Context, path string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "*/*")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		apiErr := &APIError{Status: resp.StatusCode, RawBody: body}
		var wrapper struct {
			Error *APIError `json:"error"`
		}
		if json.Unmarshal(body, &wrapper) == nil && wrapper.Error != nil {
			wrapper.Error.Status = resp.StatusCode
			wrapper.Error.RawBody = body
			return nil, wrapper.Error
		}
		if json.Unmarshal(body, apiErr) == nil && apiErr.HumanText() != "" {
			return nil, apiErr
		}
		apiErr.Message = formatNonJsonErrorBody(resp.StatusCode, body)
		return nil, apiErr
	}

	return resp.Body, nil
}

// Do executes an HTTP request against the Platform API.
// body is marshaled to JSON if non-nil. out is decoded from the JSON response if non-nil.
func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reqBody)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		apiErr := &APIError{Status: resp.StatusCode, RawBody: respBody}
		// Try to decode { "error": { "code", "message" } } or { "code", "message" }
		var wrapper struct {
			Error *APIError `json:"error"`
		}
		if json.Unmarshal(respBody, &wrapper) == nil && wrapper.Error != nil {
			wrapper.Error.Status = resp.StatusCode
			wrapper.Error.RawBody = respBody
			return wrapper.Error
		}
		if json.Unmarshal(respBody, apiErr) == nil && apiErr.HumanText() != "" {
			return apiErr
		}
		// Body isn't JSON in our canonical shape (could be DeveloperExceptionPage
		// HTML in Dev, a Stripe.net stack trace, an upstream 5xx page, etc).
		// Surface a truncated body alongside the status code instead of just
		// "HTTP 500" so the operator has something actionable. Without this the
		// CLI silently swallowed Stripe-side 500 reasons surfaced via webhooks.
		apiErr.Message = formatNonJsonErrorBody(resp.StatusCode, respBody)
		return apiErr
	}

	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}

	return nil
}
