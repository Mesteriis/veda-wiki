package importer

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// PreviewComparison counts checkout articles and assets against a running
// VEDA by state: same, different, checkout_only and veda_only. Only wiki
// originals are compared: program listing hashes can describe translations.
// This is aggregate read-only evidence, not an import, deletion or conflict plan.
type PreviewComparison struct {
	Articles map[string]int
	Assets   map[string]int
}

// ComparePreview reads the source at a loopback VEDA endpoint and compares the
// batch with it.
func ComparePreview(batch Batch, endpoint string) (*PreviewComparison, error) {
	if batch.Kind != "wiki" || !domain.IDPattern.MatchString(batch.SourceID) {
		return nil, domain.ErrInvalid
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.Port() == "" {
		return nil, domain.ErrInvalid
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return nil, domain.ErrInvalid
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
	var sources []domain.Source
	if err = get("/api/v1/sources", &sources); err != nil {
		return nil, err
	}
	var selected *domain.Source
	for _, source := range sources {
		if source.ID == batch.SourceID && source.Kind == "wiki" {
			if selected != nil {
				return nil, domain.ErrInvalid
			}
			selected = &source
		}
	}
	if selected == nil {
		return nil, domain.ErrNotFound
	}
	var pages []domain.Page
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
		if !ok || !domain.ArticlePath(name, "wiki") {
			return nil, domain.ErrInvalid
		}
		if _, duplicate := remote[name]; duplicate {
			return nil, domain.ErrInvalid
		}
		if _, err := domain.HexKey(page.SHA); err != nil {
			return nil, domain.ErrInvalid
		}
		remote[name] = page.SHA
	}
	result := map[string]int{"same": 0, "different": 0, "checkout_only": 0, "veda_only": 0}
	for name, text := range batch.Files {
		sha, exists := remote[name]
		if !exists {
			result["checkout_only"]++
		} else if sha == domain.Digest([]byte(text)) {
			result["same"]++
		} else {
			result["different"]++
		}
		delete(remote, name)
	}
	result["veda_only"] = len(remote)
	assets := map[string]int{"same": 0, "different": 0, "checkout_only": 0, "veda_only": 0}
	remoteAssets := make(map[string]string, len(selected.Assets))
	for name, ref := range selected.Assets {
		if !domain.RemoteAssetPath(name, "wiki") {
			return nil, domain.ErrInvalid
		}
		if _, err := domain.HexKey(ref.SHA); err != nil {
			return nil, domain.ErrInvalid
		}
		remoteAssets[name] = ref.SHA
	}
	for name, data := range batch.Assets {
		sha, exists := remoteAssets[name]
		if !exists {
			assets["checkout_only"]++
		} else if sha == domain.Digest(data) {
			assets["same"]++
		} else {
			assets["different"]++
		}
		delete(remoteAssets, name)
	}
	assets["veda_only"] = len(remoteAssets)
	return &PreviewComparison{Articles: result, Assets: assets}, nil
}
