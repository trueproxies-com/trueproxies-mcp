package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// catalogTTL bounds reuse of the public catalog. The hosted server sends every
// anonymous caller's catalog request from one address.
const catalogTTL = 5 * time.Minute

// maxResponse caps an API response body; 1,000 endpoints fit well inside it.
const maxResponse = 4 << 20

// apiClient calls the TrueProxies customer API. It holds no credentials: each
// call carries the caller's Authorization header value.
type apiClient struct {
	base      string
	userAgent string
	http      *http.Client

	mu        sync.Mutex
	catalog   []byte
	catalogAt time.Time
}

// apiError is a non-2xx API response. The API's body is {"code","message"}.
type apiError struct {
	Status     int
	Code       string
	Message    string
	RequestID  string
	RetryAfter string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("HTTP %d %s: %s", e.Status, e.Code, e.Message)
}

func newAPIClient(base, version string) *apiClient {
	return &apiClient{
		base:      strings.TrimRight(base, "/"),
		userAgent: "trueproxies-mcp/" + version,
		http:      &http.Client{Timeout: 60 * time.Second},
	}
}

// do sends one request and returns the response body and X-Request-ID.
// auth is the whole Authorization header value; empty sends none.
func (c *apiClient) do(ctx context.Context, method, path string, query url.Values, body any, auth string) ([]byte, string, error) {
	u := c.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, "", err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	rid := resp.Header.Get("X-Request-ID")
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return nil, rid, err
	}
	if resp.StatusCode >= 300 {
		e := &apiError{Status: resp.StatusCode, RequestID: rid, RetryAfter: resp.Header.Get("Retry-After")}
		var body struct{ Code, Message string }
		if json.Unmarshal(data, &body) == nil {
			e.Code, e.Message = body.Code, body.Message
		}
		if e.Message == "" {
			e.Message = http.StatusText(resp.StatusCode)
		}
		return nil, rid, e
	}
	return data, rid, nil
}

// publicCatalog returns the public catalog, reused for catalogTTL.
func (c *apiClient) publicCatalog(ctx context.Context) ([]byte, string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.catalog != nil && time.Since(c.catalogAt) < catalogTTL {
		return c.catalog, "", nil
	}
	data, rid, err := c.do(ctx, http.MethodGet, "/v1/catalog", nil, nil, "")
	if err != nil {
		return nil, rid, err
	}
	c.catalog, c.catalogAt = data, time.Now()
	return data, rid, nil
}
