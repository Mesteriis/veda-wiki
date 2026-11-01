package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// localClient intentionally ignores HTTP_PROXY and refuses redirects: owner
// credentials must never leave the explicitly selected loopback service.
type localClient struct {
	base  string
	token string
	http  *http.Client
}

func newLocalClient(base, keyFile string) (*localClient, error) {
	u, e := url.Parse(base)
	if e != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, ErrInvalid
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return nil, errors.New("CLI requires loopback HTTP")
	}
	info, e := os.Lstat(keyFile)
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("owner key must be a private regular file")
	}
	token, e := os.ReadFile(keyFile)
	if e != nil || len(token) != 64 {
		return nil, ErrInvalid
	}
	if _, e := hexKey(string(token)); e != nil {
		return nil, e
	}
	return &localClient{base: strings.TrimRight(base, "/"), token: string(token), http: &http.Client{Timeout: 45 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func hexKey(s string) ([]byte, error) {
	if len(s) != 64 {
		return nil, ErrInvalid
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return nil, ErrInvalid
		}
	}
	return []byte(s), nil
}
func (c *localClient) request(method, route string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		b, e := json.Marshal(body)
		if e != nil {
			return e
		}
		reader = bytes.NewReader(b)
	}
	req, e := http.NewRequest(method, c.base+route, reader)
	if e != nil {
		return e
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, e := c.http.Do(req)
	if e != nil {
		return errors.New("local VEDA service unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		if resp.StatusCode == 409 {
			return ErrConflict
		}
		return fmt.Errorf("VEDA refused request (HTTP %d)", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}
func importCommand(args []string) error {
	flags := flag.NewFlagSet("import", flag.ContinueOnError)
	checkout := flags.String("checkout", "", "Selected Git checkout")
	sourceID := flags.String("source", "", "Stable source ID")
	base := flags.String("url", "http://127.0.0.1:8080", "Loopback VEDA endpoint")
	key := flags.String("key-file", ".veda/owner.key", "Owner key file")
	create := flags.Bool("create", false, "Create source when it does not exist")
	visibility := flags.String("visibility", "private", "Explicit local source visibility when creating")
	if e := flags.Parse(args); e != nil {
		return e
	}
	if flags.NArg() != 0 || *checkout == "" || *sourceID == "" {
		return ErrInvalid
	}
	batch, e := prepareCheckout(*checkout, *sourceID)
	if errors.Is(e, ErrNoDocs) {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"status": "skipped", "reason": "no veda.yaml or docs/"})
	}
	if e != nil {
		return e
	}
	client, e := newLocalClient(*base, *key)
	if e != nil {
		return e
	}
	var sources []Source
	if e = client.request("GET", "/api/v1/sources", nil, &sources); e != nil {
		return e
	}
	expected := ""
	found := false
	for _, source := range sources {
		if source.ID == *sourceID {
			expected = source.ImportGeneration
			found = true
			break
		}
	}
	if !found && !*create {
		return ErrNotFound
	}
	batch.ExpectedImport = &expected
	batch.Create = !found && *create
	batch.Visibility = *visibility
	var result ImportResult
	if e = client.request("POST", "/api/v1/import", batch, &result); e != nil {
		return e
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}
