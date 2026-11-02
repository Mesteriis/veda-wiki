package main

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Compare only wiki originals: program listing hashes can describe translations.
// This is aggregate read-only evidence, not an import, deletion or conflict plan.
func compareWikiPreview(batch ImportBatch, endpoint string) (map[string]int, error) {
	if batch.Kind != "wiki" || !identifier.MatchString(batch.SourceID) {
		return nil, ErrInvalid
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.Port() == "" {
		return nil, ErrInvalid
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return nil, ErrInvalid
	}
	client := &http.Client{Timeout: 45 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(route string, result any) error {
		resp, err := client.Get("http://" + u.Host + route)
		if err != nil {
			return errors.New("read-only VEDA comparison unavailable")
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return errors.New("read-only VEDA comparison unavailable")
		}
		reader := &io.LimitedReader{R: resp.Body, N: (8 << 20) + 1}
		dec := json.NewDecoder(reader)
		if dec.Decode(result) != nil {
			return errors.New("invalid VEDA comparison response")
		}
		var extra any
		if dec.Decode(&extra) != io.EOF || reader.N <= 0 {
			return errors.New("invalid VEDA comparison response")
		}
		return nil
	}
	var sources []Source
	if err = get("/api/v1/sources", &sources); err != nil {
		return nil, err
	}
	found := false
	for _, source := range sources {
		if source.ID == batch.SourceID && source.Kind == "wiki" {
			found = true
		}
	}
	if !found {
		return nil, ErrNotFound
	}
	var pages []Page
	if err = get("/api/v1/articles", &pages); err != nil {
		return nil, err
	}
	remote := map[string]string{}
	prefix := batch.SourceID + "/"
	for _, page := range pages {
		if page.Environment != batch.SourceID {
			continue
		}
		name, ok := strings.CutPrefix(page.Path, prefix)
		if !ok || !articlePath(name, "wiki") {
			return nil, ErrInvalid
		}
		if _, duplicate := remote[name]; duplicate {
			return nil, ErrInvalid
		}
		if _, err := hexKey(page.SHA); err != nil {
			return nil, ErrInvalid
		}
		remote[name] = page.SHA
	}
	result := map[string]int{"same": 0, "different": 0, "checkout_only": 0, "veda_only": 0}
	for name, text := range batch.Files {
		sha, exists := remote[name]
		if !exists {
			result["checkout_only"]++
		} else if sha == digest([]byte(text)) {
			result["same"]++
		} else {
			result["different"]++
		}
		delete(remote, name)
	}
	result["veda_only"] = len(remote)
	return result, nil
}
