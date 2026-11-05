// Package github is the GitHub REST client used by synchronization: token
// exchange for GitHub App and personal credentials, repository and tree
// reads, and the write operations behind publication.
package github

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// Client talks to the GitHub REST and GraphQL APIs.
type Client struct {
	base string
	http *http.Client
}

// New returns a client for api.github.com with bounded timeouts and no
// redirects or proxies.
func New() *Client {
	return &Client{base: "https://api.github.com", http: &http.Client{
		Timeout:       30 * time.Second,
		Transport:     &http.Transport{Proxy: nil},
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// NewForTest returns a client for a local test server.
func NewForTest(base string, hc *http.Client) *Client { return &Client{base: base, http: hc} }

func (g *Client) call(ctx context.Context, method, route, token string, body, out any) error {
	var reader io.Reader
	if body != nil {
		b, e := json.Marshal(body)
		if e != nil {
			return e
		}
		reader = bytes.NewReader(b)
	}
	req, e := http.NewRequestWithContext(ctx, method, g.base+route, reader)
	if e != nil {
		return domain.ErrInvalid
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	req.Header.Set("User-Agent", "VEDA")
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, e := g.http.Do(req)
	if e != nil {
		return &RemoteError{Code: "upstream_unavailable"}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		code := "upstream_rejected"
		switch resp.StatusCode {
		case 301, 307, 308:
			// GitHub redirects a renamed or transferred repository. The
			// redirect is never followed: the caller resolves the repository
			// by its numeric identity and verifies it.
			code = "repository_moved"
		case 401, 403:
			code = "permission_denied"
			if resp.Header.Get("X-RateLimit-Remaining") == "0" || resp.Header.Get("Retry-After") != "" {
				code = "rate_limited"
			}
		case 429:
			code = "rate_limited"
		case 404:
			code = "repository_unavailable"
		case 409, 422:
			code = "upstream_conflict"
		}
		if resp.StatusCode >= 500 {
			code = "upstream_unavailable"
		}
		err := &RemoteError{Code: code, Status: resp.StatusCode}
		if code == "rate_limited" {
			err.RetryAt = githubRetryAt(resp.Header, time.Now())
		}
		return err
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, (20<<20)+1))
	if e != nil || len(b) > 20<<20 {
		return &RemoteError{Code: "response_limit"}
	}
	if out == nil {
		return nil
	}
	if e = json.Unmarshal(b, out); e != nil {
		return &RemoteError{Code: "invalid_upstream_response"}
	}
	return nil
}

// Respect both primary and secondary rate-limit deadlines. The one-minute
// floor handles missing or malformed headers; cap untrusted values at 24 hours.
func githubRetryAt(headers http.Header, now time.Time) time.Time {
	delay := time.Minute
	if seconds, err := strconv.ParseInt(headers.Get("Retry-After"), 10, 64); err == nil && seconds > 0 {
		if seconds > 86400 {
			seconds = 86400
		}
		delay = max(delay, time.Duration(seconds)*time.Second)
	}
	if headers.Get("X-RateLimit-Remaining") == "0" {
		if seconds, err := strconv.ParseInt(headers.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			resetDelay := time.Unix(seconds, 0).Sub(now)
			delay = max(delay, min(resetDelay, 24*time.Hour))
		}
	}
	return now.Add(delay)
}

// AppJWT signs the short-lived JWT that authenticates a GitHub App.
func AppJWT(secret Secret) (string, error) {
	block, _ := pem.Decode([]byte(secret.PrivateKey))
	if block == nil || len(secret.PrivateKey) > 16384 || secret.AppID < 1 || secret.InstallationID < 1 {
		return "", domain.ErrInvalid
	}
	var key *rsa.PrivateKey
	if k, e := x509.ParsePKCS1PrivateKey(block.Bytes); e == nil {
		key = k
	} else {
		k, e := x509.ParsePKCS8PrivateKey(block.Bytes)
		if e != nil {
			return "", domain.ErrInvalid
		}
		var ok bool
		key, ok = k.(*rsa.PrivateKey)
		if !ok {
			return "", domain.ErrInvalid
		}
	}
	if key.N.BitLen() < 2048 {
		return "", domain.ErrInvalid
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]any{"iat": time.Now().Add(-time.Minute).Unix(), "exp": time.Now().Add(9 * time.Minute).Unix(), "iss": strconv.FormatInt(secret.AppID, 10)})
	input := header + "." + base64.RawURLEncoding.EncodeToString(claims)
	hash := sha256.Sum256([]byte(input))
	signature, e := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
	if e != nil {
		return "", domain.ErrInvalid
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

// VerifyAccount checks the credential against GitHub and returns the account
// login it belongs to.
func (g *Client) VerifyAccount(ctx context.Context, secret Secret) (string, error) {
	switch secret.Kind {
	case "token":
		if secret.PrivateKey != "" || secret.AppID != 0 || secret.InstallationID != 0 {
			return "", domain.ErrInvalid
		}
		if len(secret.Token) < 20 || len(secret.Token) > 4096 || strings.ContainsAny(secret.Token, "\r\n") {
			return "", domain.ErrInvalid
		}
		var user struct {
			Login string `json:"login"`
		}
		if e := g.call(ctx, "GET", "/user", secret.Token, nil, &user); e != nil {
			return "", e
		}
		if !domain.ValidGitHubName(user.Login) {
			return "", domain.ErrInvalid
		}
		return user.Login, nil
	case "app":
		if secret.Token != "" {
			return "", domain.ErrInvalid
		}
		jwt, e := AppJWT(secret)
		if e != nil {
			return "", e
		}
		var installation struct {
			AppID   int64 `json:"app_id"`
			Account struct {
				Login string `json:"login"`
			} `json:"account"`
			SuspendedAt *string `json:"suspended_at"`
		}
		if e = g.call(ctx, "GET", fmt.Sprintf("/app/installations/%d", secret.InstallationID), jwt, nil, &installation); e != nil {
			return "", e
		}
		if installation.AppID != secret.AppID || !domain.ValidGitHubName(installation.Account.Login) || installation.SuspendedAt != nil {
			return "", domain.ErrInvalid
		}
		return installation.Account.Login, nil
	}
	return "", domain.ErrInvalid
}

// Token returns the bearer token for repo: the personal token itself, or a
// short-lived installation token for a GitHub App credential. A nil secret
// means anonymous read access.
func (g *Client) Token(ctx context.Context, secret *Secret, repo string, write bool) (string, error) {
	if secret == nil {
		return "", nil
	}
	if secret.Kind == "token" {
		return secret.Token, nil
	}
	jwt, e := AppJWT(*secret)
	if e != nil {
		return "", e
	}
	parts := strings.Split(repo, "/")
	permission := "read"
	if write {
		permission = "write"
	}
	body := map[string]any{"repositories": []string{parts[1]}, "permissions": map[string]string{"contents": permission}}
	var token struct {
		Token   string `json:"token"`
		Expires string `json:"expires_at"`
	}
	if e = g.call(ctx, "POST", fmt.Sprintf("/app/installations/%d/access_tokens", secret.InstallationID), jwt, body, &token); e != nil {
		return "", e
	}
	expiry, e := time.Parse(time.RFC3339, token.Expires)
	if e != nil || len(token.Token) < 20 || expiry.Before(time.Now()) {
		return "", domain.ErrInvalid
	}
	return token.Token, nil
}

// Repository reads the repository metadata: default branch, visibility and
// archive state.
func (g *Client) Repository(ctx context.Context, repo, token string) (RepositoryInfo, error) {
	var result RepositoryInfo
	e := g.call(ctx, "GET", "/repos/"+repo, token, nil, &result)
	if e != nil {
		return result, e
	}
	canonical, e := domain.RepositoryName(result.FullName)
	if e != nil || !strings.EqualFold(canonical, repo) {
		return result, &RemoteError{Code: "invalid_upstream_response"}
	}
	return checkedRepository(result)
}

// RepositoryByID reads the repository metadata by its numeric identity, the
// handle that survives a rename on GitHub. The answer must describe that
// identity.
func (g *Client) RepositoryByID(ctx context.Context, id int64, token string) (RepositoryInfo, error) {
	var result RepositoryInfo
	if id < 1 {
		return result, domain.ErrInvalid
	}
	e := g.call(ctx, "GET", "/repositories/"+strconv.FormatInt(id, 10), token, nil, &result)
	if e != nil {
		return result, e
	}
	if _, e = domain.RepositoryName(result.FullName); e != nil || result.ID != id {
		return result, &RemoteError{Code: "invalid_upstream_response"}
	}
	return checkedRepository(result)
}

// checkedRepository validates the shared fields of a repository answer and
// settles its privacy from both the flag and the visibility.
func checkedRepository(result RepositoryInfo) (RepositoryInfo, error) {
	if result.ID < 1 || result.Private == nil || !domain.ValidBranch(result.DefaultBranch) {
		return result, &RemoteError{Code: "invalid_upstream_response"}
	}
	if result.Visibility != "public" && result.Visibility != "private" && result.Visibility != "internal" {
		return result, &RemoteError{Code: "invalid_upstream_response"}
	}
	private := *result.Private || result.Visibility != "public"
	result.Private = &private
	return result, nil
}

// Head returns the commit ID at the tip of branch.
func (g *Client) Head(ctx context.Context, repo, branch, token string) (string, error) {
	var ref struct {
		Object struct {
			SHA  string `json:"sha"`
			Type string `json:"type"`
		} `json:"object"`
	}
	e := g.call(ctx, "GET", "/repos/"+repo+"/git/ref/heads/"+url.PathEscape(branch), token, nil, &ref)
	if e != nil {
		return "", e
	}
	if ref.Object.Type != "commit" || !domain.ValidCommitID(ref.Object.SHA) {
		return "", domain.ErrInvalid
	}
	return ref.Object.SHA, nil
}

// Tree returns the recursive file tree of the commit head.
func (g *Client) Tree(ctx context.Context, repo, head, token string) (Tree, error) {
	var commit Commit
	if e := g.call(ctx, "GET", "/repos/"+repo+"/git/commits/"+head, token, nil, &commit); e != nil {
		return Tree{}, e
	}
	if !domain.ValidCommitID(commit.Tree.SHA) {
		return Tree{}, domain.ErrInvalid
	}
	var tree Tree
	e := g.call(ctx, "GET", "/repos/"+repo+"/git/trees/"+commit.Tree.SHA+"?recursive=1", token, nil, &tree)
	if e != nil {
		return tree, e
	}
	if tree.Truncated || len(tree.Tree) > 20000 || tree.SHA != commit.Tree.SHA {
		return tree, &RemoteError{Code: "repository_limit"}
	}
	return tree, nil
}

// Blob downloads the blob sha, bounded in size, and decodes its content.
func (g *Client) Blob(ctx context.Context, repo, sha, token string) ([]byte, error) {
	if !domain.ValidCommitID(sha) {
		return nil, domain.ErrInvalid
	}
	var b struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
		Size     int    `json:"size"`
	}
	if e := g.call(ctx, "GET", "/repos/"+repo+"/git/blobs/"+sha, token, nil, &b); e != nil {
		return nil, e
	}
	if b.Encoding != "base64" || b.Size > 4<<20 || len(b.Content) > 6<<20 {
		return nil, &RemoteError{Code: "blob_limit"}
	}
	data, e := base64.StdEncoding.DecodeString(strings.ReplaceAll(b.Content, "\n", ""))
	if e != nil || len(data) != b.Size {
		return nil, domain.ErrInvalid
	}
	return data, nil
}
