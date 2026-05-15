package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"aegis/cli/internal/cli/clierr"
)

// APIResponse is the standard response envelope returned by the aegislab API.
type APIResponse[T any] struct {
	Code      int    `json:"code"`
	Message   string `json:"message"`
	Data      T      `json:"data,omitempty"`
	Timestamp int64  `json:"timestamp,omitempty"`
	Errors    []any  `json:"errors,omitempty"`
}

const (
	exitCodeServer = 10
	exitCodeDecode = 11
	genericServerMessage = "An unexpected error occurred"
)

// Pagination contains pagination metadata.
type Pagination struct {
	Page       int `json:"page"`
	Size       int `json:"size"`
	Total      int `json:"total"`
	TotalPages int `json:"total_pages"`
}

// PaginatedData wraps a list of items together with pagination info.
type PaginatedData[T any] struct {
	Items      []T        `json:"items"`
	Pagination Pagination `json:"pagination"`
}

// APIError is returned when the server responds with a non-2xx status.
type APIError struct {
	StatusCode int
	Code       int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("API error %d: %s", e.StatusCode, e.Message)
}

// Client is the core HTTP client for the aegislab API.
type Client struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

// NewClient creates a new API client.
func NewClient(baseURL, token string, timeout time.Duration) *Client {
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &Client{
		BaseURL: baseURL,
		Token:   token,
		HTTPClient: &http.Client{
			Timeout:   timeout,
			Transport: DefaultTransport(),
		},
	}
}

// doRequest executes an HTTP request and decodes the JSON response into dest.
func (c *Client) doRequest(method, path string, body any, headers map[string]string, dest any) error {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	url := c.BaseURL + path
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	requestID := resp.Header.Get("X-Request-Id")
	bodySummary := summarizeBody(respBody)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
			cause := summarizeServerCause(respBody)
			message := fmt.Sprintf("server returned HTTP %d", resp.StatusCode)
			if cause != "" {
				message += fmt.Sprintf("; cause: %s", cause)
			}
			if requestID != "" {
				message += fmt.Sprintf("; request_id=%s", requestID)
			}
			return &clierr.CLIError{
				Type:       "server",
				Message:    message,
				Cause:      cause,
				RequestID:  requestID,
				Suggestion: "The request failed on the server side. Retry if this is a transient incident.",
				Retryable:  true,
				ExitCode:   exitCodeServer,
			}
		}

		var apiResp APIResponse[any]
		if json.Unmarshal(respBody, &apiResp) == nil && apiResp.Message != "" {
			return &APIError{
				StatusCode: resp.StatusCode,
				Code:       apiResp.Code,
				Message:    apiResp.Message,
			}
		}
		return &APIError{
			StatusCode: resp.StatusCode,
			Message:    string(respBody),
		}
	}

	if dest != nil {
		if err := json.Unmarshal(respBody, dest); err != nil {
			return decodeError(err, bodySummary, requestID)
		}
	}
	return nil
}

func decodeError(err error, bodySummary string, requestID string) error {
	cause := decodeCause(err, bodySummary)
	return &clierr.CLIError{
		Type:       "decode",
		Message:    "decode response: failed to decode server JSON payload",
		Cause:      cause,
		RequestID:  requestID,
		Suggestion: "Check that client and server response contracts are aligned.",
		Retryable:  false,
		ExitCode:   exitCodeDecode,
	}
}

func decodeCause(err error, bodySummary string) string {
	if ute, ok := err.(*json.UnmarshalTypeError); ok {
		expected := "unknown"
		if ute.Type != nil {
			expected = ute.Type.String()
		}
		actual := ute.Value
		if actual == "" {
			actual = "unknown"
		}
		if ute.Field != "" {
			return fmt.Sprintf("field %q: expected %s, got %s", ute.Field, expected, actual)
		}
		return fmt.Sprintf("expected %s, got %s", expected, actual)
	}

	if len(bodySummary) == 0 {
		return err.Error()
	}
	return fmt.Sprintf("%s; body=%s", err.Error(), bodySummary)
}

func summarizeBody(body []byte) string {
	summary := strings.TrimSpace(string(body))
	if summary == "" {
		return "empty response body"
	}
	summary = strings.ReplaceAll(summary, "\n", " ")
	const maxBodySummary = 200
	if len(summary) <= maxBodySummary {
		return summary
	}
	return summary[:maxBodySummary] + "..."
}

func summarizeServerCause(body []byte) string {
	summary := summarizeBody(body)
	if summary == "empty response body" {
		return summary
	}

	var payload struct {
		Code    any    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &payload); err == nil {
		if strings.EqualFold(strings.TrimSpace(payload.Message), genericServerMessage) {
			if payload.Code != nil {
				return fmt.Sprintf("generic internal server error payload (code=%v)", payload.Code)
			}
			return "generic internal server error payload"
		}
	}

	if strings.Contains(summary, genericServerMessage) {
		return "generic internal server error payload"
	}
	return summary
}

// Get sends a GET request.
func (c *Client) Get(path string, dest any) error {
	return c.doRequest(http.MethodGet, path, nil, nil, dest)
}

// Post sends a POST request.
func (c *Client) Post(path string, body any, dest any) error {
	return c.doRequest(http.MethodPost, path, body, nil, dest)
}

// PostWithHeaders sends a POST request with additional headers.
func (c *Client) PostWithHeaders(path string, headers map[string]string, dest any) error {
	return c.doRequest(http.MethodPost, path, nil, headers, dest)
}

// Put sends a PUT request.
func (c *Client) Put(path string, body any, dest any) error {
	return c.doRequest(http.MethodPut, path, body, nil, dest)
}

// Patch sends a PATCH request.
func (c *Client) Patch(path string, body any, dest any) error {
	return c.doRequest(http.MethodPatch, path, body, nil, dest)
}

// Delete sends a DELETE request.
func (c *Client) Delete(path string, dest any) error {
	return c.doRequest(http.MethodDelete, path, nil, nil, dest)
}

// GetRaw streams the raw response body to `out`. Used by the
// `aegisctl share download` command which downloads through the
// auth-free /s/:code endpoint.
func (c *Client) GetRaw(path string, out io.Writer) (int64, error) {
	url := c.BaseURL + path
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("create request: %w", err)
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return 0, &APIError{StatusCode: resp.StatusCode, Message: strings.TrimSpace(string(body))}
	}
	return io.Copy(out, resp.Body)
}
