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

// ValidateURL accepts http://127.0.0.1:PORT, http://[::1]:PORT or
// http://localhost:PORT, or https on any host; no user info, path, query or
// fragment. Plain http to another host would expose the key and article
// text on the network.
func ValidateURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("extractor URL must be http://127.0.0.1:PORT, http://[::1]:PORT, http://localhost:PORT or https://HOST[:PORT]")
	}
	switch u.Scheme {
	case "https":
	case "http":
		switch u.Hostname() {
		case "127.0.0.1", "::1", "localhost":
		default:
			return nil, errors.New("plain http extractor URL must be 127.0.0.1, [::1] or localhost")
		}
		if u.Port() == "" {
			return nil, errors.New("plain http extractor URL needs a port")
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
// knowledge errors:
//   - 401/403/429/502/503, transport failures, and a 400 without a JSON
//     error code (a proxy or a host check, not the service) are
//     ErrUnavailable;
//   - 400/415/422 with an error code are ErrRejected; 413 is ErrRejected
//     and ErrBlockTooLarge;
//   - 504 and running out of RequestTimeout are ErrTimeout;
//   - other statuses (5xx) are ErrExtractorError;
//   - a body that breaks the contract is ErrInvalidResponse.
func (c *Client) Extract(parent context.Context, req knowledge.ExtractRequest) (knowledge.ExtractResult, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return knowledge.ExtractResult{}, err
	}
	ctx, cancel := context.WithTimeout(parent, RequestTimeout)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base.String()+"/v1/extract", bytes.NewReader(body))
	if err != nil {
		return knowledge.ExtractResult{}, err
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set(KeyHeader, c.key)
	resp, err := c.http.Do(r)
	if err != nil {
		return knowledge.ExtractResult{}, transportError(parent, ctx)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	if err != nil {
		return knowledge.ExtractResult{}, transportError(parent, ctx)
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
		return knowledge.ModelInfo{}, fmt.Errorf("%w: transport failure", knowledge.ErrUnavailable)
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

// transportError classifies a failed exchange: the caller's cancellation as
// is, the call's own deadline as ErrTimeout, anything else as unavailable.
func transportError(parent, call context.Context) error {
	switch {
	case parent.Err() != nil:
		return parent.Err()
	case call.Err() != nil:
		return fmt.Errorf("%w: no answer within %s", knowledge.ErrTimeout, RequestTimeout)
	}
	return fmt.Errorf("%w: transport failure", knowledge.ErrUnavailable)
}

func statusError(status int, code string) error {
	code = safeCode(code)
	if status == http.StatusBadRequest && code == "" {
		return fmt.Errorf("%w: status 400 without a service error", knowledge.ErrUnavailable)
	}
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable:
		return fmt.Errorf("%w: status %d %s", knowledge.ErrUnavailable, status, code)
	case http.StatusRequestEntityTooLarge:
		return fmt.Errorf("%w: %w: status %d %s", knowledge.ErrRejected, knowledge.ErrBlockTooLarge, status, code)
	case http.StatusBadRequest, http.StatusUnsupportedMediaType, http.StatusUnprocessableEntity:
		return fmt.Errorf("%w: status %d %s", knowledge.ErrRejected, status, code)
	case http.StatusGatewayTimeout:
		return fmt.Errorf("%w: status %d %s", knowledge.ErrTimeout, status, code)
	}
	return fmt.Errorf("%w: status %d %s", knowledge.ErrExtractorError, status, code)
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
