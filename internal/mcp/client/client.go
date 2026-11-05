// Package client is the REST client behind the MCP adapter. It applies the
// adapter's safety checks: loopback HTTP (or explicitly allowed reader-only
// HTTPS), private key and socket files, no proxy, no redirects, bounded
// responses and sanitized refusals.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// MaxResponseBytes is the largest response body the client accepts.
const MaxResponseBytes = 24 << 20

// Options configures a Client. KeyFile is an owner key; ReaderKeyFile and
// KeyValue (a key already read, 64 lowercase hex characters) are reader keys
// and make the client read-only, as does SocketPath.
type Options struct {
	URL                    string
	KeyFile                string
	ReaderKeyFile          string
	SocketPath             string
	KeyValue               string
	AllowRemoteReaderHTTPS bool
	// Editor restricts an owner-key client to reading, preparing and
	// writing articles: agents keep knowledge current without the
	// administrative tools.
	Editor bool
}

// Client talks to the VEDA REST service.
type Client struct {
	base     string
	keyFile  string
	keyValue string
	socket   string
	readOnly bool
	editor   bool
	http     *http.Client
}

// New validates the configuration and builds a Client.
func New(opts Options) (*Client, error) {
	if opts.KeyFile != "" && opts.ReaderKeyFile != "" {
		return nil, errors.New("choose one key file")
	}
	keyFile := opts.KeyFile
	if keyFile == "" {
		keyFile = opts.ReaderKeyFile
	}
	readOnly := opts.ReaderKeyFile != "" || opts.SocketPath != "" || opts.KeyValue != ""
	parsed, err := url.Parse(opts.URL)
	if err != nil {
		return nil, errors.New("invalid REST URL")
	}
	host := strings.ToLower(parsed.Hostname())
	loopbackHTTP := parsed.Scheme == "http" && (host == "localhost" || host == "127.0.0.1" || host == "::1")
	remoteReaderHTTPS := opts.AllowRemoteReaderHTTPS && parsed.Scheme == "https" && host != "" &&
		opts.ReaderKeyFile != "" && opts.SocketPath == "" && opts.KeyValue == ""
	if !(loopbackHTTP || remoteReaderHTTPS) || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") ||
		parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return nil, errors.New("MCP requires loopback HTTP or explicitly enabled reader-only HTTPS")
	}
	if port := parsed.Port(); port != "" {
		if n, err := strconv.Atoi(port); err != nil || n > 65535 {
			return nil, errors.New("invalid REST URL port")
		}
	}
	if opts.SocketPath != "" && (!filepath.IsAbs(opts.SocketPath) || opts.KeyFile != "") {
		return nil, errors.New("invalid read-only socket configuration")
	}
	if opts.SocketPath == "" && keyFile == "" {
		return nil, errors.New("a key file is required")
	}
	if opts.KeyValue != "" && (opts.SocketPath == "" || keyFile != "") {
		return nil, errors.New("invalid preamble key configuration")
	}
	if opts.KeyValue != "" && !validKey(opts.KeyValue) {
		return nil, errors.New("invalid preamble key")
	}
	if opts.Editor && (opts.KeyFile == "" || readOnly) {
		return nil, errors.New("editor mode requires the owner key file")
	}
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, TLSClientConfig: tlsConfig()}
	if opts.SocketPath != "" {
		transport.DialContext = dialSocket(opts.SocketPath)
	}
	return &Client{
		base: strings.TrimRight(opts.URL, "/"), keyFile: keyFile, keyValue: opts.KeyValue,
		socket: opts.SocketPath, readOnly: readOnly, editor: opts.Editor,
		http: &http.Client{
			Transport:     transport,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

// ReadOnly reports whether the client holds a reader credential or socket.
func (c *Client) ReadOnly() bool { return c.readOnly }

// Editor reports whether the owner-key client is limited to article tools.
func (c *Client) Editor() bool { return c.editor }

// Timeout returns the request deadline: 360 s for a GitHub connection that
// adopts an existing source, 120 s for source reconciliation and mirror
// changes, 15 s otherwise.
func Timeout(method, route string, body any) time.Duration {
	if method != http.MethodPost {
		return 15 * time.Second
	}
	switch route {
	case "/api/v1/sources/reconcile", "/api/v1/sources/mirrors":
		return 120 * time.Second
	case "/api/v1/github/connections":
		if body != nil {
			if raw, err := json.Marshal(body); err == nil {
				var fields map[string]any
				if json.Unmarshal(raw, &fields) == nil && fields["adopt_existing"] == true {
					return 360 * time.Second
				}
			}
		}
	}
	return 15 * time.Second
}

func (c *Client) key() (string, error) {
	if c.keyValue != "" {
		return c.keyValue, nil
	}
	return readKeyFile(c.keyFile)
}

// Do sends one request and returns the JSON response body. An HTTP status of
// 300 or more is returned as Refused; any other failure is a plain error.
func (c *Client) Do(ctx context.Context, method, route string, body any) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout(method, route, body))
	defer cancel()
	target := c.base + route
	var token string
	var err error
	if c.socket != "" {
		if err = checkSocket(c.socket); err != nil {
			return nil, err
		}
		target = "http://localhost" + route
		if c.keyFile != "" || c.keyValue != "" {
			if token, err = c.key(); err != nil {
				return nil, err
			}
		}
	} else if token, err = c.key(); err != nil {
		return nil, err
	}
	var reader io.Reader
	if body != nil {
		var buf bytes.Buffer
		encoder := json.NewEncoder(&buf)
		encoder.SetEscapeHTML(false)
		if err = encoder.Encode(body); err != nil {
			return nil, err
		}
		reader = bytes.NewReader(bytes.TrimRight(buf.Bytes(), "\n"))
	}
	request, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		payload, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return nil, newRefused(response.StatusCode, payload)
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, MaxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(payload) > MaxResponseBytes {
		return nil, errors.New("response exceeds MCP limit")
	}
	if !json.Valid(payload) {
		return nil, errors.New("response is not JSON")
	}
	return json.RawMessage(payload), nil
}
