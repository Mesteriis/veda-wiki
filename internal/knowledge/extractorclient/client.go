// Package extractorclient is the HTTP client of the knowledge extractor
// service (wire contract v1, see knowledge.ExtractRequest). It sends the
// service key in X-Veda-Extractor-Key, bounds time and response size, and
// returns only responses that pass knowledge.ValidateResult. Errors never
// carry the key, the URL or article text.
package extractorclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/knowledge"
)

// KeyHeader carries the service key.
const KeyHeader = "X-Veda-Extractor-Key"

// Bounds of one call. RequestTimeout exceeds the service's own 120 s limit
// so the service answers 504 first.
const (
	RequestTimeout   = 150 * time.Second
	ReadyTimeout     = 5 * time.Second
	MaxResponseBytes = 32 << 20
)

// Client calls one extractor service.
type Client struct {
	base *url.URL
	key  string
	http *http.Client
}

var _ knowledge.Extractor = (*Client)(nil)

// ValidateURL accepts http on a numeric loopback address (127.0.0.0/8 or
// ::1) with an explicit port, or https on any host; no user info, query or
// fragment. Plain http to another host would expose the key and article
// text on the network.
func ValidateURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("extractor URL must be http://127.0.0.1:PORT or https://HOST[:PORT]")
	}
	switch u.Scheme {
	case "https":
	case "http":
		ip := net.ParseIP(u.Hostname())
		if ip == nil || !ip.IsLoopback() || u.Port() == "" {
			return nil, errors.New("plain http extractor URL must be a numeric loopback address with a port")
		}
	default:
		return nil, errors.New("extractor URL must use http or https")
	}
	u.Path = ""
	return u, nil
}

// New returns a client for the service at rawURL with the service key.
func New(rawURL, key string) (*Client, error) {
	u, err := ValidateURL(rawURL)
	if err != nil {
		return nil, err
	}
	if key == "" || strings.ContainsAny(key, "\r\n") {
		return nil, errors.New("invalid extractor key")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &Client{base: u, key: key, http: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

type errorBody struct {
	Error struct {
		Code string `json:"code"`
	} `json:"error"`
}

// Extract implements knowledge.Extractor. Status codes map to the
// knowledge errors: 401/403/429/502/503 and transport failures are
// ErrUnavailable; 400/415/422 ErrRejected; 413 ErrRejected and
// ErrBlockTooLarge; 504 and other failures are plain errors (retried with
// backoff).
func (c *Client) Extract(ctx context.Context, req knowledge.ExtractRequest) (knowledge.ExtractResult, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return knowledge.ExtractResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, RequestTimeout)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base.String()+"/v1/extract", bytes.NewReader(body))
	if err != nil {
		return knowledge.ExtractResult{}, err
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set(KeyHeader, c.key)
	resp, err := c.http.Do(r)
	if err != nil {
		return knowledge.ExtractResult{}, transportError(ctx, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	if err != nil {
		return knowledge.ExtractResult{}, transportError(ctx, err)
	}
	if len(data) > MaxResponseBytes {
		return knowledge.ExtractResult{}, fmt.Errorf("%w: response too large", knowledge.ErrInvalidResponse)
	}
	if resp.StatusCode != http.StatusOK {
		var e errorBody
		_ = json.Unmarshal(data, &e)
		return knowledge.ExtractResult{}, statusError(resp.StatusCode, e.Error.Code)
	}
	var res knowledge.ExtractResult
	if err = json.Unmarshal(data, &res); err != nil {
		return knowledge.ExtractResult{}, fmt.Errorf("%w: undecodable body", knowledge.ErrInvalidResponse)
	}
	if err = knowledge.ValidateResult(req, res); err != nil {
		return knowledge.ExtractResult{}, err
	}
	return res, nil
}

// Ready implements knowledge.Extractor: GET /ready answers 200 with status
// "ready", the pinned model and its pipeline settings object.
func (c *Client) Ready(ctx context.Context) (knowledge.ModelInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, ReadyTimeout)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base.String()+"/ready", nil)
	if err != nil {
		return knowledge.ModelInfo{}, err
	}
	resp, err := c.http.Do(r)
	if err != nil {
		return knowledge.ModelInfo{}, transportError(ctx, err)
	}
	defer resp.Body.Close()
	var ready struct {
		Status string              `json:"status"`
		Model  knowledge.ModelInfo `json:"model"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&ready); err != nil || resp.StatusCode != http.StatusOK || ready.Status != "ready" {
		return knowledge.ModelInfo{}, fmt.Errorf("%w: not ready", knowledge.ErrUnavailable)
	}
	if _, err = knowledge.ExtractorDigest(ready.Model.Pipeline); err != nil {
		return knowledge.ModelInfo{}, fmt.Errorf("%w: no pipeline settings", knowledge.ErrUnavailable)
	}
	return ready.Model, nil
}

func transportError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return fmt.Errorf("%w: transport failure", knowledge.ErrUnavailable)
}

func statusError(status int, code string) error {
	code = safeCode(code)
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable:
		return fmt.Errorf("%w: status %d %s", knowledge.ErrUnavailable, status, code)
	case http.StatusRequestEntityTooLarge:
		return fmt.Errorf("%w: %w: status %d %s", knowledge.ErrRejected, knowledge.ErrBlockTooLarge, status, code)
	case http.StatusBadRequest, http.StatusUnsupportedMediaType, http.StatusUnprocessableEntity:
		return fmt.Errorf("%w: status %d %s", knowledge.ErrRejected, status, code)
	}
	return fmt.Errorf("extractor: status %d %s", status, code)
}

// safeCode keeps a short error code of [a-z_] only; anything else is dropped.
func safeCode(code string) string {
	if len(code) > 40 {
		return ""
	}
	for _, r := range code {
		if (r < 'a' || r > 'z') && r != '_' {
			return ""
		}
	}
	return code
}
