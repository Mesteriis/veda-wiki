package main

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
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type GitHubAccount struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Login     string `json:"login"`
	SecretRef string `json:"secret_ref,omitempty"`
}

// SecretRef is an opaque filename, not credential material. It is removed from
// API responses; only the private transaction file retains the reference.
type GitHubSecret struct {
	Kind           string `json:"kind"`
	Token          string `json:"token,omitempty"`
	AppID          int64  `json:"app_id,omitempty"`
	InstallationID int64  `json:"installation_id,omitempty"`
	PrivateKey     string `json:"private_key,omitempty"`
}
type GitHubConnection struct {
	ID             string `json:"id"`
	Repository     string `json:"repository"`
	RepositoryID   int64  `json:"repository_id"`
	Branch         string `json:"branch"`
	AccountID      string `json:"account_id,omitempty"`
	Write          bool   `json:"write"`
	Private        bool   `json:"private"`
	Verification   string `json:"verification"`
	VerifiedAt     string `json:"verified_at"`
	State          string `json:"state"`
	LastError      string `json:"last_error,omitempty"`
	LastPull       string `json:"last_pull,omitempty"`
	NextAttempt    string `json:"next_attempt,omitempty"`
	Attempts       int    `json:"attempts"`
	Paused         bool   `json:"paused"`
	ConfigRevision int    `json:"config_revision"`
	TreeSHA        string `json:"tree_sha,omitempty"`
}
type repositoryInfo struct {
	ID            int64  `json:"id"`
	FullName      string `json:"full_name"`
	Private       *bool  `json:"private"`
	DefaultBranch string `json:"default_branch"`
	Archived      bool   `json:"archived"`
	Permissions   struct {
		Push bool `json:"push"`
	} `json:"permissions"`
	Visibility string `json:"visibility"`
}
type gitTreeEntry struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
	Size int64  `json:"size"`
}
type gitTree struct {
	SHA       string         `json:"sha"`
	Tree      []gitTreeEntry `json:"tree"`
	Truncated bool           `json:"truncated"`
}
type gitCommit struct {
	SHA  string `json:"sha"`
	Tree struct {
		SHA string `json:"sha"`
	} `json:"tree"`
}
type remoteError struct {
	Code   string
	Status int
}

func (e *remoteError) Error() string { return e.Code }

var githubPart = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,99}$`)

func repositoryName(value string) (string, error) {
	if strings.HasPrefix(value, "https://") {
		u, e := url.Parse(value)
		if e != nil || !strings.EqualFold(u.Host, "github.com") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return "", ErrInvalid
		}
		value = strings.TrimPrefix(u.Path, "/")
	}
	value = strings.TrimSuffix(value, ".git")
	parts := strings.Split(value, "/")
	if len(parts) != 2 || !githubPart.MatchString(parts[0]) || !githubPart.MatchString(parts[1]) || strings.HasSuffix(parts[0], ".") || strings.HasSuffix(parts[1], ".") {
		return "", ErrInvalid
	}
	return strings.Join(parts, "/"), nil
}
func validBranch(branch string) bool {
	return len(branch) > 0 && len(branch) < 200 && !strings.HasPrefix(branch, "-") && !strings.HasPrefix(branch, "/") && !strings.HasSuffix(branch, "/") && !strings.HasSuffix(branch, ".") && !strings.HasSuffix(branch, ".lock") && !strings.Contains(branch, "..") && !strings.Contains(branch, "//") && !strings.ContainsAny(branch, " ~^:?*[\\\r\n\x00") && !strings.Contains(branch, "@{")
}

type GitHubClient struct {
	base string
	http *http.Client
}

func newGitHubClient() *GitHubClient {
	return &GitHubClient{base: "https://api.github.com", http: &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (g *GitHubClient) call(ctx context.Context, method, route, token string, body, out any) error {
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
		return ErrInvalid
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
		return &remoteError{Code: "upstream_unavailable"}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		code := "upstream_rejected"
		switch resp.StatusCode {
		case 401, 403:
			code = "permission_denied"
			if resp.Header.Get("X-RateLimit-Remaining") == "0" || resp.Header.Get("Retry-After") != "" {
				code = "rate_limited"
			}
		case 404:
			code = "repository_unavailable"
		case 409, 422:
			code = "upstream_conflict"
		}
		if resp.StatusCode >= 500 {
			code = "upstream_unavailable"
		}
		return &remoteError{Code: code, Status: resp.StatusCode}
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, (20<<20)+1))
	if e != nil || len(b) > 20<<20 {
		return &remoteError{Code: "response_limit"}
	}
	if out == nil {
		return nil
	}
	if e = json.Unmarshal(b, out); e != nil {
		return &remoteError{Code: "invalid_upstream_response"}
	}
	return nil
}
func appJWT(secret GitHubSecret) (string, error) {
	block, _ := pem.Decode([]byte(secret.PrivateKey))
	if block == nil || len(secret.PrivateKey) > 16384 || secret.AppID < 1 || secret.InstallationID < 1 {
		return "", ErrInvalid
	}
	var key *rsa.PrivateKey
	if k, e := x509.ParsePKCS1PrivateKey(block.Bytes); e == nil {
		key = k
	} else {
		k, e := x509.ParsePKCS8PrivateKey(block.Bytes)
		if e != nil {
			return "", ErrInvalid
		}
		var ok bool
		key, ok = k.(*rsa.PrivateKey)
		if !ok {
			return "", ErrInvalid
		}
	}
	if key.N.BitLen() < 2048 {
		return "", ErrInvalid
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]any{"iat": time.Now().Add(-time.Minute).Unix(), "exp": time.Now().Add(9 * time.Minute).Unix(), "iss": strconv.FormatInt(secret.AppID, 10)})
	input := header + "." + base64.RawURLEncoding.EncodeToString(claims)
	hash := sha256.Sum256([]byte(input))
	signature, e := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
	if e != nil {
		return "", ErrInvalid
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}
func (g *GitHubClient) verifyAccount(ctx context.Context, secret GitHubSecret) (string, error) {
	switch secret.Kind {
	case "token":
		if secret.PrivateKey != "" || secret.AppID != 0 || secret.InstallationID != 0 {
			return "", ErrInvalid
		}
		if len(secret.Token) < 20 || len(secret.Token) > 4096 || strings.ContainsAny(secret.Token, "\r\n") {
			return "", ErrInvalid
		}
		var user struct {
			Login string `json:"login"`
		}
		if e := g.call(ctx, "GET", "/user", secret.Token, nil, &user); e != nil {
			return "", e
		}
		if !githubPart.MatchString(user.Login) {
			return "", ErrInvalid
		}
		return user.Login, nil
	case "app":
		if secret.Token != "" {
			return "", ErrInvalid
		}
		jwt, e := appJWT(secret)
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
		if installation.AppID != secret.AppID || !githubPart.MatchString(installation.Account.Login) || installation.SuspendedAt != nil {
			return "", ErrInvalid
		}
		return installation.Account.Login, nil
	}
	return "", ErrInvalid
}
func (g *GitHubClient) token(ctx context.Context, secret *GitHubSecret, repo string, write bool) (string, error) {
	if secret == nil {
		return "", nil
	}
	if secret.Kind == "token" {
		return secret.Token, nil
	}
	jwt, e := appJWT(*secret)
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
		return "", ErrInvalid
	}
	return token.Token, nil
}
func (g *GitHubClient) repository(ctx context.Context, repo, token string) (repositoryInfo, error) {
	var result repositoryInfo
	e := g.call(ctx, "GET", "/repos/"+repo, token, nil, &result)
	if e != nil {
		return result, e
	}
	canonical, e := repositoryName(result.FullName)
	if e != nil || !strings.EqualFold(canonical, repo) || result.ID < 1 || result.Private == nil || !validBranch(result.DefaultBranch) {
		return result, &remoteError{Code: "invalid_upstream_response"}
	}
	if result.Visibility != "public" && result.Visibility != "private" && result.Visibility != "internal" {
		return result, &remoteError{Code: "invalid_upstream_response"}
	}
	private := *result.Private || result.Visibility != "public"
	result.Private = &private
	return result, nil
}
func (g *GitHubClient) head(ctx context.Context, repo, branch, token string) (string, error) {
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
	if ref.Object.Type != "commit" || !commitID.MatchString(ref.Object.SHA) {
		return "", ErrInvalid
	}
	return ref.Object.SHA, nil
}
func (g *GitHubClient) tree(ctx context.Context, repo, head, token string) (gitTree, error) {
	var commit gitCommit
	if e := g.call(ctx, "GET", "/repos/"+repo+"/git/commits/"+head, token, nil, &commit); e != nil {
		return gitTree{}, e
	}
	if !commitID.MatchString(commit.Tree.SHA) {
		return gitTree{}, ErrInvalid
	}
	var tree gitTree
	e := g.call(ctx, "GET", "/repos/"+repo+"/git/trees/"+commit.Tree.SHA+"?recursive=1", token, nil, &tree)
	if e != nil {
		return tree, e
	}
	if tree.Truncated || len(tree.Tree) > 20000 || tree.SHA != commit.Tree.SHA {
		return tree, &remoteError{Code: "repository_limit"}
	}
	return tree, nil
}
func (g *GitHubClient) blob(ctx context.Context, repo, sha, token string) ([]byte, error) {
	if !commitID.MatchString(sha) {
		return nil, ErrInvalid
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
		return nil, &remoteError{Code: "blob_limit"}
	}
	data, e := base64.StdEncoding.DecodeString(strings.ReplaceAll(b.Content, "\n", ""))
	if e != nil || len(data) != b.Size {
		return nil, ErrInvalid
	}
	return data, nil
}

// updateBranch uses an exact old-head precondition; REST's non-force update
// alone also accepts a branch rewound to an ancestor of the prepared commit.
func (g *GitHubClient) updateBranch(ctx context.Context, repo, branch, before, after, token string) error {
	parts := strings.Split(repo, "/")
	if len(parts) != 2 || !validBranch(branch) || !commitID.MatchString(before) || !commitID.MatchString(after) {
		return ErrInvalid
	}
	var lookup struct {
		Data struct {
			Repository *struct {
				ID string `json:"id"`
			} `json:"repository"`
		} `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	query := `query($owner:String!, $name:String!) { repository(owner:$owner, name:$name) { id } }`
	if err := g.call(ctx, "POST", "/graphql", token, map[string]any{"query": query, "variables": map[string]string{"owner": parts[0], "name": parts[1]}}, &lookup); err != nil {
		return err
	}
	if len(lookup.Errors) != 0 || lookup.Data.Repository == nil || lookup.Data.Repository.ID == "" {
		return &remoteError{Code: "repository_unavailable"}
	}
	var result struct {
		Data struct {
			UpdateRefs *struct {
				ClientMutationID *string `json:"clientMutationId"`
			} `json:"updateRefs"`
		} `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	mutation := `mutation($input:UpdateRefsInput!) { updateRefs(input:$input) { clientMutationId } }`
	input := map[string]any{"repositoryId": lookup.Data.Repository.ID, "refUpdates": []map[string]any{{"name": "refs/heads/" + branch, "beforeOid": before, "afterOid": after, "force": false}}}
	if err := g.call(ctx, "POST", "/graphql", token, map[string]any{"query": mutation, "variables": map[string]any{"input": input}}, &result); err != nil {
		return err
	}
	// GraphQL may return errors alongside HTTP 200. Leave the prepared intent
	// intact: a later complete pull must resolve an uncertain result.
	if len(result.Errors) != 0 || result.Data.UpdateRefs == nil {
		return &remoteError{Code: "upstream_conflict"}
	}
	return nil
}
func (g *GitHubClient) push(ctx context.Context, repo, branch, base, treeSHA, p, content, token string, prepare func(string) error) (string, error) {
	return g.pushFiles(ctx, repo, branch, base, treeSHA, map[string][]byte{p: []byte(content)}, token, prepare)
}
func (g *GitHubClient) pushFiles(ctx context.Context, repo, branch, base, treeSHA string, files map[string][]byte, token string, prepare func(string) error) (string, error) {
	paths := make([]string, 0, len(files))
	for name := range files {
		if !safeRelative(name) {
			return "", ErrInvalid
		}
		paths = append(paths, name)
	}
	sort.Strings(paths)
	entries := make([]map[string]string, 0, len(paths))
	created := map[string]string{}
	for _, name := range paths {
		digestKey := digest(files[name])
		sha, ok := created[digestKey]
		if !ok {
			var blob struct {
				SHA string `json:"sha"`
			}
			if e := g.call(ctx, "POST", "/repos/"+repo+"/git/blobs", token, map[string]string{"content": base64.StdEncoding.EncodeToString(files[name]), "encoding": "base64"}, &blob); e != nil {
				return "", e
			}
			if !commitID.MatchString(blob.SHA) || gitBlobDigest(files[name], len(blob.SHA)) != blob.SHA {
				return "", ErrInvalid
			}
			sha = blob.SHA
			created[digestKey] = sha
		}
		entries = append(entries, map[string]string{"path": name, "mode": "100644", "type": "blob", "sha": sha})
	}
	var tree struct {
		SHA string `json:"sha"`
	}
	if e := g.call(ctx, "POST", "/repos/"+repo+"/git/trees", token, map[string]any{"base_tree": treeSHA, "tree": entries}, &tree); e != nil {
		return "", e
	}
	if !commitID.MatchString(tree.SHA) {
		return "", ErrInvalid
	}
	var commit gitCommit
	if e := g.call(ctx, "POST", "/repos/"+repo+"/git/commits", token, map[string]any{"message": "Update documentation through VEDA", "tree": tree.SHA, "parents": []string{base}}, &commit); e != nil {
		return "", e
	}
	if !commitID.MatchString(commit.SHA) {
		return "", ErrInvalid
	}
	if e := prepare(commit.SHA); e != nil {
		return "", e
	}
	if e := g.updateBranch(ctx, repo, branch, base, commit.SHA, token); e != nil {
		return "", e
	}
	return commit.SHA, nil
}

// remove creates a deletion commit only when the complete source tree contains
// exactly the expected regular file. The caller must persist the prepared
// commit and observe the deletion in a subsequent complete pull.
func verifiedTreeFile(source gitTree, p, expected string) (string, error) {
	if source.Truncated || !commitID.MatchString(source.SHA) {
		return "", ErrInvalid
	}
	matches := 0
	mode := ""
	for _, item := range source.Tree {
		if item.Path != p {
			continue
		}
		matches++
		if item.Type != "blob" || (item.Mode != "100644" && item.Mode != "100755") || !commitID.MatchString(item.SHA) || gitBlobDigest([]byte(expected), len(item.SHA)) != item.SHA {
			return "", ErrConflict
		}
		mode = item.Mode
	}
	if matches != 1 {
		return "", ErrConflict
	}
	return mode, nil
}

func (g *GitHubClient) remove(ctx context.Context, repo, branch, base string, source gitTree, p, expected, token string, prepare func(string) error) (string, error) {
	if !commitID.MatchString(base) {
		return "", ErrInvalid
	}
	mode, err := verifiedTreeFile(source, p, expected)
	if err != nil {
		return "", err
	}
	var tree struct {
		SHA string `json:"sha"`
	}
	entry := map[string]any{"path": p, "mode": mode, "type": "blob", "sha": nil}
	if e := g.call(ctx, "POST", "/repos/"+repo+"/git/trees", token, map[string]any{"base_tree": source.SHA, "tree": []map[string]any{entry}}, &tree); e != nil {
		return "", e
	}
	if !commitID.MatchString(tree.SHA) {
		return "", ErrInvalid
	}
	var commit gitCommit
	if e := g.call(ctx, "POST", "/repos/"+repo+"/git/commits", token, map[string]any{"message": "Move documentation through VEDA", "tree": tree.SHA, "parents": []string{base}}, &commit); e != nil {
		return "", e
	}
	if !commitID.MatchString(commit.SHA) {
		return "", ErrInvalid
	}
	if e := prepare(commit.SHA); e != nil {
		return "", e
	}
	if e := g.updateBranch(ctx, repo, branch, base, commit.SHA, token); e != nil {
		return "", e
	}
	return commit.SHA, nil
}
func errorCode(e error) string {
	var remote *remoteError
	if errors.As(e, &remote) {
		return remote.Code
	}
	if errors.Is(e, ErrConflict) {
		return "source_conflict"
	}
	if errors.Is(e, ErrNoDocs) {
		return "no_documentation"
	}
	return "sync_failed"
}
